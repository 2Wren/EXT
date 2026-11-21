package billing

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const USD int64 = 1_000_000_000
const maxLimit = 100_000 * USD

var (
	ErrDisabled    = errors.New("key is disabled or unregistered")
	ErrExceeded    = errors.New("key quota exhausted")
	ErrUnavailable = errors.New("verified billing limit unavailable")
	ErrWarming     = errors.New("quota capacity is being refreshed")
)

var Shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func Day(now time.Time) string { return now.In(Shanghai).Format("2006-01-02") }

type Key struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Daily   int64  `json:"daily"`
	Total   int64  `json:"total"`
}

type Price struct {
	ID          int64  `json:"id"`
	Protocol    string `json:"protocol"`
	Model       string `json:"model"`
	BilledModel string `json:"billed_model"`
	Input       int64  `json:"input"`
	Output      int64  `json:"output"`
	Cached      int64  `json:"cached"`
	CacheWrite  int64  `json:"cache_write"`
	MaxInput    int64  `json:"max_input"`
	MaxOutput   int64  `json:"max_output"`
	Verified    bool   `json:"verified"`
	Source      string `json:"source"`
	SourceURL   string `json:"source_url"`
	SourceDate  string `json:"source_date"`
	IsReference bool   `json:"is_reference"`
	Note        string `json:"note"`
}

func (p Price) Bound(output int64) int64 {
	return p.MaxInput*(p.Input+p.Cached+p.CacheWrite) + output*p.Output
}

type Grant struct{ PriceID, LeaseID, Amount int64 }
type lease struct {
	id, free, inFlight int64
	day                string
}
type keyState struct {
	Key
	present bool
	room    int64
	lease   *lease
}
type Ledger struct {
	db     *sql.DB
	mu     sync.Mutex
	keys   map[string]*keyState
	prices map[[2]string]Price
	// Event pricing: manual overrides, synchronized catalog, built-in references.
	manual    map[string]Price
	reference map[string]Price
	catalog   map[string]CatalogPrice
	rows      map[string]catalogRow
	sync      CatalogStatus
	rev       int64
	active    map[int64]*lease
	healthy   bool
	now       func() time.Time
}

func New(db *sql.DB) (*Ledger, error) {
	if err := SeedReferences(db); err != nil {
		return nil, err
	}
	l := &Ledger{db: db, keys: map[string]*keyState{}, prices: map[[2]string]Price{}, active: map[int64]*lease{}, now: time.Now,
		manual: map[string]Price{}, reference: map[string]Price{}, catalog: map[string]CatalogPrice{}, rows: map[string]catalogRow{}}
	rows, err := db.Query("SELECT k.id,k.name,k.enabled,k.daily,k.total,coalesce(c.cpa_present,1) FROM keys k LEFT JOIN cpa_keys c ON c.id=k.id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k Key
		var present bool
		if err = rows.Scan(&k.ID, &k.Name, &k.Enabled, &k.Daily, &k.Total, &present); err != nil {
			rows.Close()
			return nil, err
		}
		l.keys[k.ID] = &keyState{Key: k, present: present}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.Query("SELECT id,protocol,model,billed_model,input,output,cached,cache_write,max_input,max_output,verified,source,source_url,source_date,is_reference,note FROM prices ORDER BY is_reference DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Price
		if err = rows.Scan(&p.ID, &p.Protocol, &p.Model, &p.BilledModel, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &p.MaxInput, &p.MaxOutput, &p.Verified, &p.Source, &p.SourceURL, &p.SourceDate, &p.IsReference, &p.Note); err != nil {
			return nil, err
		}
		l.remember(p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return l, l.loadCatalog()
}

// remember indexes a price for admission and for event pricing.
func (l *Ledger) remember(p Price) {
	if p.Protocol == "*" {
		return
	}
	l.prices[[2]string{p.Protocol, p.Model}] = p
	index := l.manual
	if p.IsReference {
		index = l.reference
	}
	index[strings.ToLower(p.Model)] = p
	index[strings.ToLower(p.BilledModel)] = p
}

func (l *Ledger) SetHealthy(ok bool) { l.mu.Lock(); defer l.mu.Unlock(); l.healthy = ok }

func (l *Ledger) Enabled(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.keys[key] != nil && l.keys[key].Enabled && l.keys[key].present
}

func (l *Ledger) Admit(key, protocol, model string, output int64, strictShape bool, day string) (Grant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.keys[key]
	if k == nil || !k.Enabled || !k.present {
		return Grant{}, ErrDisabled
	}
	p := l.prices[[2]string{protocol, model}]
	if k.Daily == 0 && k.Total == 0 {
		return Grant{PriceID: p.ID}, nil
	}
	if !l.healthy {
		return Grant{}, ErrWarming
	}
	if !strictShape || !p.Verified || p.ID == 0 || output < 0 || output > p.MaxOutput {
		return Grant{}, ErrUnavailable
	}
	if output == 0 {
		output = p.MaxOutput
	}
	amount := p.Bound(output)
	if amount <= 0 {
		return Grant{}, ErrUnavailable
	}
	if k.lease == nil || k.lease.day != day {
		return Grant{}, ErrWarming
	}
	if amount > k.lease.free {
		if amount-k.lease.free > k.room {
			return Grant{}, ErrExceeded
		}
		return Grant{}, ErrWarming
	}
	k.lease.free -= amount
	k.lease.inFlight += amount
	return Grant{p.ID, k.lease.id, amount}, nil
}

func (l *Ledger) UpdateKey(key Key) error {
	if len(key.ID) != 64 || len(key.Name) > 120 || key.Daily < 0 || key.Total < 0 || key.Daily > maxLimit || key.Total > maxLimit {
		return errors.New("invalid key policy")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO keys VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,daily=excluded.daily,total=excluded.total", key.ID, key.Name, key.Enabled, key.Daily, key.Total)
	if err != nil {
		return err
	}
	old := l.keys[key.ID]
	if old != nil && old.lease != nil {
		if _, err = tx.Exec("UPDATE leases SET held=held-? WHERE id=?", old.lease.free, old.lease.id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if old != nil && old.lease != nil {
		old.lease.free = 0
	}
	present := true
	if old != nil {
		present = old.present
	}
	l.keys[key.ID] = &keyState{Key: key, present: present}
	return nil
}

func (l *Ledger) AddPrice(p Price) error {
	if len(p.Model) > 200 || len(p.BilledModel) > 200 || len(p.Note) > 1000 || len(p.SourceURL) > 500 || len(p.Source) > 120 || len(p.SourceDate) > 30 {
		return errors.New("price metadata too long")
	}
	if p.Protocol == "" || p.Model == "" || p.BilledModel == "" || p.MaxInput < 0 || p.MaxInput > 10_000_000 || p.MaxOutput < 0 || p.MaxOutput > 10_000_000 {
		return errors.New("model, billed model and valid token limits are required")
	}
	for _, v := range []int64{p.Input, p.Output, p.Cached, p.CacheWrite} {
		if v < 0 || v > 10*USD {
			return errors.New("invalid token price")
		}
	}
	if p.Verified && (p.MaxInput == 0 || p.MaxOutput == 0 || p.Bound(p.MaxOutput) <= 0) {
		return errors.New("verified price must have a positive bound")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	result, err := l.db.Exec("INSERT INTO prices(protocol,model,billed_model,input,output,cached,cache_write,max_input,max_output,verified,source,source_url,source_date,is_reference,note) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		p.Protocol, p.Model, p.BilledModel, p.Input, p.Output, p.Cached, p.CacheWrite, p.MaxInput, p.MaxOutput, p.Verified, p.Source, p.SourceURL, p.SourceDate, p.IsReference, p.Note)
	if err != nil {
		return err
	}
	p.ID, err = result.LastInsertId()
	if err == nil {
		l.remember(p)
		l.rev++
	}
	return err
}

func (l *Ledger) Refresh() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	day := Day(l.now())
	target := USD
	for _, p := range l.prices {
		if p.Verified {
			target = max(target, p.Bound(p.MaxOutput))
		}
	}
	for _, k := range l.keys {
		if !k.Enabled || !k.present || (k.Daily == 0 && k.Total == 0) {
			continue
		}
		if k.lease != nil && k.lease.day != day {
			if err := l.releaseFree(k.lease); err != nil {
				return err
			}
			k.lease = nil
		}
		if k.lease != nil && k.lease.free >= target {
			continue
		}
		var total, daily int64
		err := l.db.QueryRow("SELECT coalesce(sum(amount),0),coalesce(sum(CASE WHEN day=? THEN amount END),0) FROM (SELECT amount,day FROM spend WHERE key_id=? UNION ALL SELECT held,day FROM leases WHERE key_id=?)", day, k.ID, k.ID).Scan(&total, &daily)
		if err != nil {
			return err
		}
		room := maxLimit
		if k.Total > 0 {
			room = min(room, k.Total-total)
		}
		if k.Daily > 0 {
			room = min(room, k.Daily-daily)
		}
		k.room = max(0, room)
		if k.lease == nil {
			result, err := l.db.Exec("INSERT INTO leases(key_id,day,held) VALUES(?,?,0)", k.ID, day)
			if err != nil {
				return err
			}
			id, err := result.LastInsertId()
			if err != nil {
				return err
			}
			k.lease = &lease{id: id, day: day}
			l.active[id] = k.lease
		}
		more := min(k.room, target-k.lease.free)
		if more <= 0 {
			continue
		}
		if _, err = l.db.Exec("UPDATE leases SET held=held+? WHERE id=?", more, k.lease.id); err != nil {
			return err
		}
		k.lease.free += more
		k.room -= more
	}
	return nil
}

func (l *Ledger) releaseFree(a *lease) error {
	if a.free == 0 {
		return nil
	}
	if _, err := l.db.Exec("UPDATE leases SET held=held-? WHERE id=?", a.free, a.id); err != nil {
		return err
	}
	a.free = 0
	return nil
}

func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.active {
		if err := l.releaseFree(p); err != nil {
			return err
		}
	}
	return nil
}

// Settle moves actual usage into spend and releases the balance of a matched grant.
func (l *Ledger) Settle(turn, event, key, day string, grant Grant, amount int64, session string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if amount < 0 || (grant.LeaseID != 0 && amount > grant.Amount) {
		return fmt.Errorf("amount outside reserved bound")
	}
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = closeTurn(tx, turn, key, session); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE events SET turn_id=?,cost=?,price_id=? WHERE id=? AND turn_id IS NULL", turn, amount, grant.PriceID, event); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO spend(event_id,key_id,day,amount) VALUES(?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET amount=excluded.amount,day=excluded.day", event, key, day, amount); err != nil {
		return err
	}
	var a *lease
	var reusable bool
	var result sql.Result
	var n int64
	if grant.LeaseID != 0 {
		a = l.active[grant.LeaseID]
		k := l.keys[key]
		reusable = a != nil && k != nil && k.lease == a && k.Enabled && k.present && a.day == Day(l.now())
		release := grant.Amount
		if reusable {
			release = amount
		}
		result, err = tx.Exec("UPDATE leases SET held=held-? WHERE id=? AND held>=?", release, grant.LeaseID, release)
		if err != nil {
			return err
		}
		if n, err = result.RowsAffected(); err != nil || n != 1 {
			return errors.New("missing lease capacity")
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if a != nil {
		a.inFlight -= grant.Amount
		if reusable {
			a.free += grant.Amount - amount
		}
	}
	return nil
}

// closeTurn marks a turn as matched to its usage event and adopts the session
// CPA reported when the gateway had to fall back to the turn ID.
func closeTurn(tx *sql.Tx, turn, key, session string) error {
	result, err := tx.Exec("UPDATE turns SET settled=1, session=CASE WHEN session=id AND ?!='' THEN ? ELSE session END WHERE id=? AND settled=0", session, session, turn)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errors.New("turn already settled")
	}
	if session != "" && session != turn {
		if _, err = tx.Exec("UPDATE OR IGNORE conversation_history SET session=? WHERE key_id=? AND session=? AND EXISTS (SELECT 1 FROM turns WHERE id=? AND session=?)", session, key, turn, turn, session); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE turns SET session=? WHERE key_id=? AND session=? AND EXISTS (SELECT 1 FROM turns WHERE id=? AND session=?)", session, key, turn, turn, session); err != nil {
			return err
		}
	}
	return nil
}

// Link attaches a usage event to the unreserved turn that produced it. The
// event is priced separately by Charge, so no balance moves here.
func (l *Ledger) Link(turn, event, key, session string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = closeTurn(tx, turn, key, session); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE events SET turn_id=? WHERE id=? AND turn_id IS NULL", turn, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (l *Ledger) Snapshot(key string) (free, inflight int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if k := l.keys[key]; k != nil && k.lease != nil {
		return k.lease.free, k.lease.inFlight
	}
	return 0, 0
}
