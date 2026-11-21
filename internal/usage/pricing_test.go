package usage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
	"cpa-ext/internal/store"
)

const catalogFixture = `{
 "reseller": {"models": {"claude-x": {"cost": {"input": 1, "output": 1}}, "only-reseller": {"cost": {"input": 3, "output": 6}}}},
 "anthropic": {"models": {
  "claude-x": {"cost": {"input": 8, "output": 40, "cache_read": 0.2, "cache_write": 10,
    "tiers": [{"input": 16, "output": 80, "cache_read": 0.4, "cache_write": 20, "tier": {"type": "context", "size": 200000}}]},
    "limit": {"context": 1000000, "output": 128000}},
  "free-model": {"cost": {"input": 0, "output": 0}}
 }}%s
}`

func usageEvent(id, model, billed string, uncached, read, write, output int64) string {
	return fmt.Sprintf(`{"api_key":"sk-test","trace_id":%q,"request_id":%q,"execution_id":%q,"session_id":"cpa-session","provider":"devin",
 "model":%q,"alias":%q,"response_model":%q,"timestamp":%q,
 "token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":%d,"input":{"uncached_tokens":%d,"cache_read_tokens":%d,"cache_write_tokens":%d},"output":{"total_tokens":%d}}}`,
		id, id, "exec-"+id, model, model, billed, time.Now().UTC().Format(time.RFC3339Nano), uncached+read+write+output, uncached, read, write, output)
}

func TestEventsArePricedFromSynchronizedCatalog(t *testing.T) {
	queue := []string{}
	cpaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "[")
		for i, e := range queue {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprint(w, e)
		}
		fmt.Fprint(w, "]")
		queue = nil
	}))
	defer cpaServer.Close()
	extra, downloads := "", 0
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := fmt.Sprintf(`"v%d"`, len(extra))
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		downloads++
		w.Header().Set("ETag", tag)
		fmt.Fprintf(w, catalogFixture, extra)
	}))
	defer catalog.Close()

	defer func(n int) { billing.CatalogMinimum = n }(billing.CatalogMinimum)
	billing.CatalogMinimum = 2
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := billing.New(db)
	if err != nil {
		t.Fatal(err)
	}
	key := cpa.KeyID("sk-test")
	if err = ledger.UpdateKey(billing.Key{ID: key, Name: "test"}); err != nil {
		t.Fatal(err)
	}
	c := New(db, cpa.Client{Base: cpaServer.URL, HTTP: cpaServer.Client()}, ledger)
	ctx := context.Background()
	step := func() {
		t.Helper()
		if err := c.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cost := func(event string) (amount, price int64, turn string, priced bool) {
		t.Helper()
		var a, tid any
		if err := db.QueryRow("SELECT cost,price_id,turn_id FROM events WHERE id=?", "exec-"+event).Scan(&a, &price, &tid); err != nil {
			t.Fatal(event, err)
		}
		if tid != nil {
			turn = tid.(string)
		}
		if a == nil {
			return 0, price, turn, false
		}
		return a.(int64), price, turn, true
	}

	// The gateway recorded this turn without a manual price.
	if _, err = db.Exec(`INSERT INTO turns(id,key_id,day,started,protocol,model,session,parent_response,response_id,trace,price_id,status,audit_state,latency,ttfb)
 VALUES('turn-1',?,?,?,'messages','claude-x','turn-1','','','20261121-node-req-1',0,200,'complete',1,1)`, key, billing.Day(time.Now()), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	queue = []string{usageEvent("req-1", "claude-x", "claude-x-high", 2, 100, 10, 5)}
	step()
	if _, _, turn, priced := cost("req-1"); priced || turn != "turn-1" {
		t.Fatalf("before any price: priced=%v turn=%q", priced, turn)
	}

	if err = ledger.SyncCatalog(ctx, catalog.Client(), catalog.URL); err != nil {
		t.Fatal(err)
	}
	if s := ledger.CatalogStatus(); s.Models != 2 || s.Error != "" || s.Changed == "" {
		t.Fatalf("catalog status: %+v", s)
	}
	queue = []string{
		usageEvent("req-2", "claude-x", "devin/claude-x-medium", 1000, 250000, 0, 10), // long prompt, bypassed the gateway
		usageEvent("req-3", "only-reseller", "only-reseller", 10, 0, 0, 10),
		usageEvent("req-4", "mystery", "mystery-high", 10, 0, 0, 10),
		usageEvent("req-5", "mystery", "mystery", 0, 0, 0, 0), // failed call without tokens
	}
	step()
	// First-party price, effort suffix ignored: 2*8000 + 5*40000 + 100*200 + 10*10000.
	amount, firstPrice, _, priced := cost("req-1")
	if !priced || amount != 336_000 || firstPrice == 0 {
		t.Fatalf("req-1: %d price %d priced=%v", amount, firstPrice, priced)
	}
	// Above 200K prompt tokens the tier applies: 1000*16000 + 250000*400 + 10*80000.
	if amount, price, turn, priced := cost("req-2"); !priced || amount != 116_800_000 || price != firstPrice || turn != "" {
		t.Fatalf("req-2: %d price %d turn %q priced=%v", amount, price, turn, priced)
	}
	if amount, _, _, priced := cost("req-3"); !priced || amount != 10*3000+10*6000 {
		t.Fatalf("req-3: %d priced=%v", amount, priced)
	}
	if _, _, _, priced := cost("req-4"); priced {
		t.Fatal("unknown model was priced")
	}
	if amount, _, _, priced := cost("req-5"); !priced || amount != 0 {
		t.Fatal("zero-token event should close at no cost")
	}
	var spent int64
	var session string
	var settled bool
	db.QueryRow("SELECT sum(amount) FROM spend WHERE key_id=?", key).Scan(&spent)
	db.QueryRow("SELECT session,settled FROM turns WHERE id='turn-1'").Scan(&session, &settled)
	if spent != 336_000+116_800_000+90_000 || session != "cpa-session" || !settled {
		t.Fatalf("spend %d session %q settled %v", spent, session, settled)
	}

	// Unchanged catalog: a 304 keeps everything. A new model prices the backlog.
	if err = ledger.SyncCatalog(ctx, catalog.Client(), catalog.URL); err != nil || downloads != 1 {
		t.Fatalf("conditional request: %v downloads=%d", err, downloads)
	}
	extra = `, "openai": {"models": {"mystery": {"cost": {"input": 2, "output": 4}}}}`
	if err = ledger.SyncCatalog(ctx, catalog.Client(), catalog.URL); err != nil || downloads != 2 {
		t.Fatalf("changed catalog: %v downloads=%d", err, downloads)
	}
	step()
	if amount, _, _, priced := cost("req-4"); !priced || amount != 10*2000+10*4000 {
		t.Fatalf("req-4 after sync: %d priced=%v", amount, priced)
	}

	// A manual price overrides the catalog for new usage; history keeps its price.
	if err = ledger.AddPrice(billing.Price{Protocol: "messages", Model: "claude-x", BilledModel: "claude-x", Input: 1, Output: 1, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	queue = []string{usageEvent("req-6", "claude-x", "claude-x-high", 100, 0, 0, 100)}
	step()
	if amount, price, _, _ := cost("req-6"); amount != 200 || price == firstPrice {
		t.Fatalf("manual override: %d price %d", amount, price)
	}
	if amount, price, _, _ := cost("req-1"); amount != 336_000 || price != firstPrice {
		t.Fatal("historical charge changed")
	}

	// The catalog and its price rows survive a restart without a second row.
	reopened, err := billing.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if s := reopened.CatalogStatus(); s.Models != 3 {
		t.Fatalf("reloaded catalog: %+v", s)
	}
	var rows int
	db.QueryRow("SELECT count(*) FROM prices WHERE protocol='*' AND model='claude-x'").Scan(&rows)
	if rows != 1 {
		t.Fatalf("catalog price rows: %d", rows)
	}
}
