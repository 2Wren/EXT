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
	db      *sql.DB
	cpa     cpa.Client
	ledger  *billing.Ledger
	mu      sync.Mutex
	last    time.Time
	issue   string
	invalid int64
	pending []json.RawMessage
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
	if len(c.pending) > 0 {
		if err := c.persist(c.pending); err != nil {
			return err
		}
		c.pending = nil
	}
	if err := c.match(); err != nil {
		return err
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
		var blocked any
		if e.Unsupported {
			blocked = "usage includes a billing dimension outside the configured price"
		}
		if _, err = tx.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,blocked)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, e.ID, e.KeyID, e.Trace, e.Request, e.Session, e.Provider, e.Model, e.BilledModel, e.AuthIndex, e.Input, e.Output, e.Cached, e.CacheWrite, e.Total, e.Latency, e.TTFT, e.Failed, e.Happened, e.Body, blocked); err != nil {
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
	rows, err := c.db.Query(`SELECT t.id,t.key_id,t.day,t.trace,t.model,t.price_id,t.lease_id,t.reserved FROM turns t
 WHERE t.settled=0 AND t.trace!='' AND t.price_id>0 AND EXISTS (
 SELECT 1 FROM events e WHERE e.key_id=t.key_id AND (e.trace=t.trace OR (e.request_id!='' AND substr(t.trace,-length(e.request_id)-1)='-'||e.request_id))
 AND e.turn_id IS NULL AND e.blocked IS NULL) ORDER BY t.started LIMIT 100`)
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
		if err = c.settle(x); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collector) settle(x candidate) error {
	reqID := x.trace
	parts := strings.SplitN(x.trace, "-", 3)
	if len(parts) == 3 {
		reqID = parts[2]
	}
	var id, model, session string
	var input, output, cached, write int64
	var count int
	var blocked sql.NullString
	err := c.db.QueryRow(`SELECT count(*),coalesce(min(id),''),coalesce(min(billed_model),''),coalesce(min(session),''),coalesce(sum(input),0),coalesce(sum(output),0),coalesce(sum(cached),0),coalesce(sum(cache_write),0),max(blocked)
 FROM events WHERE key_id=? AND (trace=? OR (request_id!='' AND request_id=?))`, x.key, x.trace, reqID).Scan(&count, &id, &model, &session, &input, &output, &cached, &write, &blocked)
	if err != nil {
		return err
	}
	if count != 1 || blocked.Valid {
		_, err = c.db.Exec("UPDATE events SET blocked=coalesce(blocked,'ambiguous CPA execution') WHERE key_id=? AND (trace=? OR request_id=?)", x.key, x.trace, reqID)
		return err
	}
	var p billing.Price
	err = c.db.QueryRow("SELECT billed_model,input,output,cached,cache_write,max_input,max_output FROM prices WHERE id=?", x.grant.PriceID).Scan(&p.BilledModel, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &p.MaxInput, &p.MaxOutput)
	if err != nil {
		return err
	}
	if model != p.BilledModel || input < 0 || output < 0 || cached < 0 || write < 0 || input > p.MaxInput || cached > p.MaxInput || write > p.MaxInput || output > p.MaxOutput {
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
