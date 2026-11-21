package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
)

type Collector struct {
	keysMu     sync.Mutex
	keysStatus KeySyncStatus
	db         *sql.DB
	cpa        cpa.Client
	ledger     *billing.Ledger
	mu         sync.Mutex
	last       time.Time
	issue      string
	invalid    int64
	pending    []json.RawMessage
	pricedRev  int64
	pricedAt   time.Time
}

func New(db *sql.DB, client cpa.Client, ledger *billing.Ledger) *Collector {
	return &Collector{db: db, cpa: client, ledger: ledger}
}
func (c *Collector) Status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"last_poll": c.last, "error": c.issue, "invalid_records": c.invalid}
}

func (c *Collector) Run(ctx context.Context) {
	keysDone := make(chan struct{})
	go func() { defer close(keysDone); c.runKeys(ctx) }()
	defer func() { <-keysDone }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := c.step(ctx)
		c.ledger.SetHealthy(err == nil)
		c.mu.Lock()
		if err != nil {
			if c.issue != err.Error() {
				log.Printf("usage collector: %v", err)
			}
			c.issue = err.Error()
		} else {
			c.last = time.Now()
			c.issue = ""
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Collector) step(ctx context.Context) error {
	if len(c.pending) == 0 {
		poll, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := c.cpa.Get(poll, "observability/usage/queue?count=100", &c.pending); err != nil {
			return err
		}
	}
	arrived := len(c.pending) > 0
	if arrived {
		if err := c.persist(c.pending); err != nil {
			return err
		}
		c.pending = nil
	}
	if err := c.match(); err != nil {
		return err
	}
	// Pending events are retried when usage arrives, when the price table
	// changes, and periodically for keys that wait on their reserved turn.
	if rev, now := c.ledger.PriceRev(), time.Now(); arrived || rev != c.pricedRev || now.Sub(c.pricedAt) > 30*time.Second {
		if err := c.price(now); err != nil {
			return err
		}
		c.pricedRev, c.pricedAt = rev, now
	}
	return c.ledger.Refresh()
}

func (c *Collector) persist(raw []json.RawMessage) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var invalid int64
	for _, data := range raw {
		e, err := cpa.Decode(data)
		if err != nil {
			invalid++
			continue
		}
		// approx marks usage with dimensions the token price cannot express
		// (tools, media, service tiers); it is still priced as an estimate.
		if _, err = tx.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,approx)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, e.ID, e.KeyID, e.Trace, e.Request, e.Session, e.Provider, e.Model, e.BilledModel, e.AuthIndex, e.Input, e.Output, e.Cached, e.CacheWrite, e.Total, e.Latency, e.TTFT, e.Failed, e.Happened, e.Body, e.Unsupported); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if invalid > 0 {
		c.mu.Lock()
		c.invalid += invalid
		c.mu.Unlock()
		log.Printf("CPA queue returned %d invalid usage records", invalid)
	}
	return nil
}

type candidate struct {
	turn, key, day, trace, model string
	grant                        billing.Grant
}

func (c *Collector) match() error {
	// CPA keeps usage for at most an hour, so older turns can no longer match.
	rows, err := c.db.Query(`SELECT t.id,t.key_id,t.day,t.trace,t.model,t.price_id,t.lease_id,t.reserved FROM turns t
 WHERE t.settled=0 AND t.trace!='' AND t.started>? AND EXISTS (
 SELECT 1 FROM events e WHERE e.key_id=t.key_id AND (e.trace=t.trace OR (e.request_id!='' AND substr(t.trace,-length(e.request_id)-1)='-'||e.request_id))
 AND e.turn_id IS NULL AND e.blocked IS NULL) ORDER BY t.started LIMIT 100`, time.Now().Add(-6*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	var batch []candidate
	for rows.Next() {
		var x candidate
		if err = rows.Scan(&x.turn, &x.key, &x.day, &x.trace, &x.model, &x.grant.PriceID, &x.grant.LeaseID, &x.grant.Amount); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range batch {
		// Reserved turns settle against their lease at the admitted price;
		// every other turn is only linked and priced from the price table.
		if x.grant.LeaseID != 0 {
			err = c.settle(x)
		} else {
			err = c.link(x)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func requestID(trace string) string {
	if parts := strings.SplitN(trace, "-", 3); len(parts) == 3 {
		return parts[2]
	}
	return trace
}

func (c *Collector) link(x candidate) error {
	var id, session string
	err := c.db.QueryRow(`SELECT id,session FROM events WHERE key_id=? AND (trace=? OR (request_id!='' AND request_id=?))
 AND turn_id IS NULL AND blocked IS NULL ORDER BY happened,id LIMIT 1`, x.key, x.trace, requestID(x.trace)).Scan(&id, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = c.ledger.Link(x.turn, id, x.key, session); err != nil {
		return fmt.Errorf("link %s: %w", x.turn, err)
	}
	return nil
}

// price charges every pending event the current price table can price.
func (c *Collector) price(now time.Time) error {
	type pending struct {
		billing.Usage
		trace, request string
		happened       int64
	}
	var afterTime int64 = -1
	afterID := ""
	for {
		rows, err := c.db.Query(`SELECT id,key_id,happened,trace,request_id,model,billed_model,input,output,cached,cache_write FROM events
 WHERE cost IS NULL AND blocked IS NULL AND (happened,id)>(?,?) ORDER BY happened,id LIMIT 500`, afterTime, afterID)
		if err != nil {
			return err
		}
		var batch []pending
		for rows.Next() {
			var e pending
			if err = rows.Scan(&e.Event, &e.Key, &e.happened, &e.trace, &e.request, &e.Model, &e.Billed, &e.Input, &e.Output, &e.Cached, &e.CacheWrite); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range batch {
			afterTime, afterID = e.happened, e.Event
			if c.ledger.Limited(e.Key) {
				// A quota key normally settles through its reserved turn, which
				// is stored shortly after the response ends. Only usage that
				// has no such turn (it bypassed the gateway) is priced here.
				if now.UnixMilli()-e.happened < 120_000 {
					continue
				}
				var reserved bool
				if err = c.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM turns t WHERE t.key_id=? AND t.lease_id>0 AND t.settled=0 AND t.trace!=''
 AND (t.trace=? OR (?!='' AND substr(t.trace,-length(?)-1)='-'||?)))`, e.Key, e.trace, e.request, e.request, e.request).Scan(&reserved); err != nil {
					return err
				}
				if reserved {
					continue
				}
			}
			e.Day = billing.Day(time.UnixMilli(e.happened))
			if _, err = c.ledger.Charge(e.Usage); err != nil {
				return fmt.Errorf("price %s: %w", e.Event, err)
			}
		}
		if len(batch) < 500 {
			return nil
		}
	}
}

func (c *Collector) settle(x candidate) error {
	reqID := requestID(x.trace)
	var id, model, session string
	var input, output, cached, write int64
	var count int
	var approx bool
	var blocked sql.NullString
	err := c.db.QueryRow(`SELECT count(*),coalesce(min(id),''),coalesce(min(billed_model),''),coalesce(min(session),''),coalesce(sum(input),0),coalesce(sum(output),0),coalesce(sum(cached),0),coalesce(sum(cache_write),0),max(blocked),coalesce(max(approx),0)
 FROM events WHERE key_id=? AND (trace=? OR (request_id!='' AND request_id=?))`, x.key, x.trace, reqID).Scan(&count, &id, &model, &session, &input, &output, &cached, &write, &blocked, &approx)
	if err != nil {
		return err
	}
	if count != 1 || blocked.Valid {
		_, err = c.db.Exec("UPDATE events SET blocked=coalesce(blocked,'ambiguous CPA execution') WHERE key_id=? AND (trace=? OR request_id=?)", x.key, x.trace, reqID)
		return err
	}
	if approx {
		// A reserved bound only covers plain token usage.
		_, err = c.db.Exec("UPDATE events SET blocked='usage includes a billing dimension outside the configured price' WHERE id=?", id)
		return err
	}
	var p billing.Price
	err = c.db.QueryRow("SELECT billed_model,input,output,cached,cache_write,max_input,max_output FROM prices WHERE id=?", x.grant.PriceID).Scan(&p.BilledModel, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &p.MaxInput, &p.MaxOutput)
	if err != nil {
		return err
	}
	if model != p.BilledModel || input < 0 || output < 0 || cached < 0 || write < 0 || (p.MaxInput > 0 && input+cached+write > p.MaxInput) || (p.MaxOutput > 0 && output > p.MaxOutput) {
		_, err = c.db.Exec("UPDATE events SET blocked='billed model or token count outside configured price' WHERE id=?", id)
		return err
	}
	amount := input*p.Input + output*p.Output + cached*p.Cached + write*p.CacheWrite
	if x.grant.LeaseID != 0 && amount > x.grant.Amount {
		_, err = c.db.Exec("UPDATE events SET blocked='actual charge exceeded the reserved bound' WHERE id=?", id)
		return err
	}
	if err = c.ledger.Settle(x.turn, id, x.key, x.day, x.grant, amount, session); err != nil {
		return fmt.Errorf("settle %s: %w", x.turn, err)
	}
	return nil
}

type Credential struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	Disabled    bool   `json:"disabled"`
	Unavailable bool   `json:"unavailable"`
}

func (c *Collector) Credentials(ctx context.Context) ([]Credential, error) {
	var result struct {
		Files []Credential `json:"files"`
	}
	err := c.cpa.Get(ctx, "credentials", &result)
	if err != nil {
		return nil, err
	}
	if result.Files == nil {
		return nil, errors.New("CPA returned an unexpected credentials payload")
	}
	return result.Files, nil
}
