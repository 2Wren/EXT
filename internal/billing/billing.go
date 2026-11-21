package billing

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

const USD int64 = 1_000_000_000
const maxLimit = 100_000 * USD

var (
	ErrDisabled = errors.New("key is not registered in CPA")
	ErrModel    = errors.New("model is not allowed for this key")
	ErrWeekly   = errors.New("weekly limit reached; it resets on Monday 00:00 (UTC+8)")
	ErrTotal    = errors.New("total limit reached")
)

var Shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func Day(now time.Time) string { return now.In(Shanghai).Format("2006-01-02") }

// Week is the day the current week began: Monday, Beijing time.
func Week(now time.Time) string {
	now = now.In(Shanghai)
	return now.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7)).Format("2006-01-02")
}

type Key struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Weekly and Total cap the key's spend in nano-USD; zero means no cap.
	Weekly int64 `json:"weekly"`
	Total  int64 `json:"total"`
	// Models lists the model names this key may call; empty allows every model.
	Models []string `json:"models"`
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

type keyState struct {
	Key
	present bool
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
	now       func() time.Time
}

func New(db *sql.DB) (*Ledger, error) {
	if err := SeedReferences(db); err != nil {
		return nil, err
	}
	l := &Ledger{db: db, keys: map[string]*keyState{}, prices: map[[2]string]Price{}, now: time.Now,
		manual: map[string]Price{}, reference: map[string]Price{}, catalog: map[string]CatalogPrice{}, rows: map[string]catalogRow{}}
	rows, err := db.Query("SELECT k.id,k.name,k.weekly,k.total,k.models,coalesce(c.cpa_present,1) FROM keys k LEFT JOIN cpa_keys c ON c.id=k.id")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k Key
		var present bool
		var models string
		if err = rows.Scan(&k.ID, &k.Name, &k.Weekly, &k.Total, &models, &present); err != nil {
			rows.Close()
			return nil, err
		}
		k.Models = SplitModels(models)
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

// Enabled reports whether CPA still has the key.
func (l *Ledger) Enabled(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.keys[key] != nil && l.keys[key].present
}

// Models returns the key's model restriction; empty means unrestricted.
func (l *Ledger) Models(key string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.keys[key] == nil {
		return nil
	}
	return l.keys[key].Models
}

// SplitModels reads the stored newline-separated restriction.
func SplitModels(stored string) []string {
	models := []string{}
	for _, m := range strings.Split(stored, "\n") {
		if m != "" {
			models = append(models, m)
		}
	}
	return models
}

func cleanModels(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || seen[strings.ToLower(m)] {
			continue
		}
		if len(m) > 200 || strings.ContainsAny(m, "\r\n") || len(out) == 200 {
			return nil, errors.New("invalid model restriction")
		}
		seen[strings.ToLower(m)] = true
		out = append(out, m)
	}
	return out, nil
}

// AllowsModel reports whether a model passes a restriction. Names compare
// without case, "*" matches any run of characters, and a routing prefix
// ("devin/claude-opus-5", "models/gemini-pro") may be omitted from the entry.
func AllowsModel(allowed []string, model string) bool {
	if len(allowed) == 0 {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	bare := model[strings.LastIndex(model, "/")+1:]
	for _, entry := range allowed {
		entry = strings.ToLower(entry)
		if wildcard(entry, model) || wildcard(entry, bare) {
			return true
		}
	}
	return false
}

func wildcard(pattern, text string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == text
	}
	if !strings.HasPrefix(text, parts[0]) {
		return false
	}
	text = text[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		at := strings.Index(text, part)
		if at < 0 {
			return false
		}
		text = text[at+len(part):]
	}
	return strings.HasSuffix(text, parts[len(parts)-1])
}

// Admit decides whether a key may start a request and returns the manual price
// row recorded with the turn. Limits count the spend already recorded, so a
// request in flight when a limit is reached still completes.
func (l *Ledger) Admit(key, protocol, model string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.keys[key]
	if k == nil || !k.present {
		return 0, ErrDisabled
	}
	for _, limit := range []struct {
		amount int64
		since  string
		err    error
	}{{k.Total, "", ErrTotal}, {k.Weekly, Week(l.now()), ErrWeekly}} {
		if limit.amount == 0 {
			continue
		}
		var spent int64
		if err := l.db.QueryRow("SELECT coalesce(sum(amount),0) FROM spend WHERE key_id=? AND day>=?", key, limit.since).Scan(&spent); err != nil {
			return 0, err
		}
		if spent >= limit.amount {
			return 0, limit.err
		}
	}
	return l.prices[[2]string{protocol, model}].ID, nil
}

// UpdateKey stores a key's limits and model restriction. The name only applies
// to a key that is new; names of known keys follow CPAMC through NameKeys.
func (l *Ledger) UpdateKey(key Key) error {
	if len(key.ID) != 64 || len(key.Name) > 120 || key.Weekly < 0 || key.Total < 0 || key.Weekly > maxLimit || key.Total > maxLimit {
		return errors.New("invalid key policy")
	}
	models, err := cleanModels(key.Models)
	if err != nil {
		return err
	}
	key.Models = models
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err = l.db.Exec("INSERT INTO keys(id,name,weekly,total,models) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET weekly=excluded.weekly,total=excluded.total,models=excluded.models",
		key.ID, key.Name, key.Weekly, key.Total, strings.Join(models, "\n")); err != nil {
		return err
	}
	present := true
	if old := l.keys[key.ID]; old != nil {
		present, key.Name = old.present, old.Name
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

// Link attaches a usage event to the turn that produced it. The
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
