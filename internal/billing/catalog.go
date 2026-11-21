package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// CatalogURL is the public list-price catalog synchronized into price_catalog.
const CatalogURL = "https://models.dev/api.json"

// CatalogMinimum rejects a truncated or malformed download instead of
// replacing a good table with it.
var CatalogMinimum = 100

// Rate is nanoUSD per token, i.e. USD per million tokens multiplied by 1000.
type Rate struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write"`
}

// Tier replaces the base rate once the prompt exceeds Over tokens.
type Tier struct {
	Over int64 `json:"over"`
	Rate
}

type CatalogPrice struct {
	Model, Provider string
	Rate
	Tiers              []Tier
	Context, MaxOutput int64
}

func (p CatalogPrice) rate(prompt int64) Rate {
	r, over := p.Rate, int64(-1)
	for _, t := range p.Tiers {
		if prompt > t.Over && t.Over > over {
			r, over = t.Rate, t.Over
		}
	}
	return r
}

func (p CatalogPrice) fingerprint() string {
	tiers, _ := json.Marshal(p.Tiers)
	return fmt.Sprintf("%d/%d/%d/%d/%s", p.Input, p.Output, p.Cached, p.CacheWrite, tiers)
}

type CatalogStatus struct {
	Checked string `json:"checked"`
	Changed string `json:"changed"`
	Models  int    `json:"models"`
	Error   string `json:"error"`
	etag    string
}

// Usage is one CPA usage event to be priced.
type Usage struct {
	Event, Key, Day, Model, Billed    string
	Input, Output, Cached, CacheWrite int64
}

var effortSuffix = regexp.MustCompile(`-(none|minimal|low|medium|high|xhigh|max|thinking)$`)

// names lists the identifiers to try for a billed model, most specific first:
// the reported ID, the ID without a provider prefix, then without
// reasoning-effort suffixes such as "-high".
func names(ids ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		add(id)
		if i := strings.LastIndex(id, "/"); i >= 0 {
			id = id[i+1:]
			add(id)
		}
		for effortSuffix.MatchString(id) {
			id = effortSuffix.ReplaceAllString(id, "")
			add(id)
		}
	}
	return out
}

// quote resolves a price: manual overrides, then the synchronized catalog,
// then the built-in reference rows. The returned ID is 0 for catalog prices
// that still need a versioned row.
func (l *Ledger) quote(billed, model string) (CatalogPrice, int64, bool) {
	candidates := names(billed, model)
	for _, source := range []map[string]Price{l.manual, nil, l.reference} {
		for _, name := range candidates {
			if source == nil {
				if p, ok := l.catalog[name]; ok {
					return p, 0, true
				}
				continue
			}
			if p, ok := source[name]; ok {
				return CatalogPrice{Model: p.Model, Provider: p.Source, Rate: Rate{p.Input, p.Output, p.Cached, p.CacheWrite}}, p.ID, true
			}
		}
	}
	return CatalogPrice{}, 0, false
}

type catalogRow struct {
	id          int64
	fingerprint string
}

// row returns the versioned prices row for a catalog price, adding a new one
// whenever the synchronized rate changes so historical charges keep their ID.
func (l *Ledger) row(p CatalogPrice) (int64, error) {
	fp := p.fingerprint()
	if r, ok := l.rows[p.Model]; ok && r.fingerprint == fp {
		return r.id, nil
	}
	tiers := ""
	if len(p.Tiers) > 0 {
		raw, _ := json.Marshal(p.Tiers)
		tiers = string(raw)
	}
	result, err := l.db.Exec(`INSERT INTO prices(protocol,model,billed_model,input,output,cached,cache_write,max_input,max_output,verified,source,source_url,source_date,is_reference,note,tiers)
 VALUES('*',?,?,?,?,?,?,?,?,0,?,?,?,1,?,?)`, p.Model, p.Model, p.Input, p.Output, p.Cached, p.CacheWrite, p.Context, p.MaxOutput,
		"models.dev/"+p.Provider, "https://models.dev", l.now().In(Shanghai).Format("2006-01-02"), "Synchronized list price; an estimate, not the upstream debit.", tiers)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	l.rows[p.Model] = catalogRow{id, fp}
	return id, nil
}

// Charge prices one usage event from the current price table and records the
// spend. It reports false when no price is known yet; the event stays pending
// and is retried after the price table changes.
func (l *Ledger) Charge(u Usage) (bool, error) {
	if u.Input < 0 || u.Output < 0 || u.Cached < 0 || u.CacheWrite < 0 {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var amount, priceID int64
	if u.Input+u.Output+u.Cached+u.CacheWrite > 0 {
		p, id, ok := l.quote(u.Billed, u.Model)
		if !ok {
			return false, nil
		}
		r := p.rate(u.Input + u.Cached + u.CacheWrite)
		amount = u.Input*r.Input + u.Output*r.Output + u.Cached*r.Cached + u.CacheWrite*r.CacheWrite
		if id == 0 {
			var err error
			if id, err = l.row(p); err != nil {
				return false, err
			}
		}
		priceID = id
	}
	tx, err := l.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE events SET cost=?,price_id=? WHERE id=? AND cost IS NULL", amount, priceID, u.Event)
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return false, err
	}
	if _, err = tx.Exec("INSERT INTO spend(event_id,key_id,day,amount) VALUES(?,?,?,?) ON CONFLICT(event_id) DO UPDATE SET amount=excluded.amount", u.Event, u.Key, u.Day, amount); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Limited reports whether a key has a quota and therefore reserves before forwarding.
func (l *Ledger) Limited(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.keys[key]
	return k != nil && (k.Daily > 0 || k.Total > 0)
}

// PriceRev changes whenever the price table does, so pending events are retried.
func (l *Ledger) PriceRev() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rev
}

func (l *Ledger) CatalogStatus() CatalogStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	status := l.sync
	status.Models = len(l.catalog)
	return status
}

func (l *Ledger) loadCatalog() error {
	rows, err := l.db.Query("SELECT model,provider,input,output,cached,cache_write,tiers,context,max_output FROM price_catalog")
	if err != nil {
		return err
	}
	for rows.Next() {
		var p CatalogPrice
		var tiers string
		if err = rows.Scan(&p.Model, &p.Provider, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &tiers, &p.Context, &p.MaxOutput); err != nil {
			rows.Close()
			return err
		}
		if tiers != "" {
			if err = json.Unmarshal([]byte(tiers), &p.Tiers); err != nil {
				rows.Close()
				return err
			}
		}
		l.catalog[p.Model] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = l.db.Query("SELECT id,model,input,output,cached,cache_write,tiers FROM prices WHERE protocol='*' ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var p CatalogPrice
		var tiers string
		if err = rows.Scan(&id, &p.Model, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &tiers); err != nil {
			rows.Close()
			return err
		}
		if tiers != "" {
			if err = json.Unmarshal([]byte(tiers), &p.Tiers); err != nil {
				rows.Close()
				return err
			}
		}
		l.rows[p.Model] = catalogRow{id, p.fingerprint()}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	err = l.db.QueryRow("SELECT etag,checked,changed,error FROM price_sync WHERE id=1").Scan(&l.sync.etag, &l.sync.Checked, &l.sync.Changed, &l.sync.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

type devRate struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// rate converts USD per million tokens. Missing cache rates fall back to the
// input rate so cached tokens are never free by omission.
func (d devRate) rate() (Rate, bool) {
	if d.Input == nil || d.Output == nil {
		return Rate{}, false
	}
	nano := func(v *float64, fallback int64) (int64, bool) {
		if v == nil {
			return fallback, true
		}
		if math.IsNaN(*v) || *v < 0 || *v > 100_000 {
			return 0, false
		}
		return int64(math.Round(*v * 1000)), true
	}
	var r Rate
	var ok [4]bool
	r.Input, ok[0] = nano(d.Input, 0)
	r.Output, ok[1] = nano(d.Output, 0)
	r.Cached, ok[2] = nano(d.CacheRead, r.Input)
	r.CacheWrite, ok[3] = nano(d.CacheWrite, r.Input)
	return r, ok[0] && ok[1] && ok[2] && ok[3]
}

type devModel struct {
	Cost *struct {
		devRate
		Tiers []struct {
			devRate
			Tier struct {
				Type string  `json:"type"`
				Size float64 `json:"size"`
			} `json:"tier"`
		} `json:"tiers"`
		Over200k *devRate `json:"context_over_200k"`
	} `json:"cost"`
	Limit struct {
		Context float64 `json:"context"`
		Output  float64 `json:"output"`
	} `json:"limit"`
}

type devProvider struct {
	Models map[string]devModel `json:"models"`
}

// First-party list prices win over resellers that publish the same model ID.
var firstParty = []string{"anthropic", "openai", "google", "xai", "deepseek", "moonshotai", "zhipuai", "zai", "alibaba", "mistral", "meta", "minimax"}

func buildCatalog(data map[string]devProvider) map[string]CatalogPrice {
	order := append([]string{}, firstParty...)
	preferred := map[string]bool{}
	for _, id := range firstParty {
		preferred[id] = true
	}
	var rest []string
	for id := range data {
		if !preferred[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	out := map[string]CatalogPrice{}
	for _, provider := range append(order, rest...) {
		for id, m := range data[provider].Models {
			key := strings.ToLower(strings.TrimSpace(id))
			if _, taken := out[key]; taken || key == "" || len(key) > 200 || m.Cost == nil {
				continue
			}
			base, ok := m.Cost.rate()
			if !ok || base.Input+base.Output == 0 {
				continue
			}
			p := CatalogPrice{Model: key, Provider: provider, Rate: base, Context: int64(m.Limit.Context), MaxOutput: int64(m.Limit.Output)}
			for _, t := range m.Cost.Tiers {
				if r, ok := t.rate(); ok && t.Tier.Type == "context" && t.Tier.Size > 0 {
					p.Tiers = append(p.Tiers, Tier{int64(t.Tier.Size), r})
				}
			}
			if len(p.Tiers) == 0 && m.Cost.Over200k != nil {
				if r, ok := m.Cost.Over200k.rate(); ok {
					p.Tiers = []Tier{{200_000, r}}
				}
			}
			sort.Slice(p.Tiers, func(i, j int) bool { return p.Tiers[i].Over < p.Tiers[j].Over })
			out[key] = p
		}
	}
	return out
}

// SyncCatalog refreshes the list-price catalog. An unchanged upstream answers
// 304 and costs one small request.
func (l *Ledger) SyncCatalog(ctx context.Context, client *http.Client, url string) (err error) {
	l.mu.Lock()
	etag, loaded := l.sync.etag, len(l.catalog) > 0
	l.mu.Unlock()
	checked := l.now().UTC().Format(time.RFC3339)
	changed := false
	defer func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.sync.Checked, l.sync.Error = checked, ""
		if err != nil {
			l.sync.Error = err.Error()
		}
		if changed {
			l.sync.Changed = checked
		}
		if _, e := l.db.Exec(`INSERT INTO price_sync(id,etag,checked,changed,error) VALUES(1,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET etag=excluded.etag,checked=excluded.checked,changed=excluded.changed,error=excluded.error`, l.sync.etag, l.sync.Checked, l.sync.Changed, l.sync.Error); e != nil && err == nil {
			err = e
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cpa-ext")
	if etag != "" && loaded {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("price catalog: status %d", resp.StatusCode)
	}
	var data map[string]devProvider
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&data); err != nil {
		return fmt.Errorf("price catalog: %w", err)
	}
	catalog := buildCatalog(data)
	if len(catalog) < CatalogMinimum {
		return fmt.Errorf("price catalog: only %d priced models, keeping the previous table", len(catalog))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM price_catalog"); err != nil {
		return err
	}
	insert, err := tx.Prepare("INSERT INTO price_catalog(model,provider,input,output,cached,cache_write,tiers,context,max_output) VALUES(?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, p := range catalog {
		tiers := ""
		if len(p.Tiers) > 0 {
			raw, _ := json.Marshal(p.Tiers)
			tiers = string(raw)
		}
		if _, err = insert.Exec(p.Model, p.Provider, p.Input, p.Output, p.Cached, p.CacheWrite, tiers, p.Context, p.MaxOutput); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	l.catalog = catalog
	l.sync.etag = resp.Header.Get("ETag")
	l.rev++
	changed = true
	return nil
}

// RunCatalog keeps the catalog fresh until ctx ends.
func (l *Ledger) RunCatalog(ctx context.Context, client *http.Client, url string, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	var last string
	for {
		err := l.SyncCatalog(ctx, client, url)
		if ctx.Err() != nil {
			return
		}
		switch {
		case err != nil && err.Error() != last:
			log.Printf("price catalog sync: %v", err)
			last = err.Error()
		case err == nil:
			last = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
