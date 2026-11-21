package billing

import (
	"cpa-ext/internal/store"
	"testing"
)

func TestReferenceCatalogVersionAndManualOverride(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	p := l.prices[[2]string{"chat", "gpt-4o"}]
	if !p.IsReference || p.Verified || p.Input != 2500 || p.SourceURL == "" || p.SourceDate == "" {
		t.Fatalf("bad reference: %+v", p)
	}
	var before, after int
	db.QueryRow("SELECT count(*) FROM prices").Scan(&before)
	if err = SeedReferences(db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT count(*) FROM prices").Scan(&after)
	if before != after {
		t.Fatal("reference seed not idempotent")
	}
	err = l.AddPrice(Price{Protocol: "chat", Model: "gpt-4o", BilledModel: "gpt-4o", Input: 3000, Output: 11000, Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	oldID := l.prices[[2]string{"chat", "gpt-4o"}].ID
	reopened, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.prices[[2]string{"chat", "gpt-4o"}]
	if got.ID != oldID || got.IsReference {
		t.Fatal("manual price overwritten")
	}
	var oldInput int64
	if err = db.QueryRow("SELECT input FROM prices WHERE id=?", p.ID).Scan(&oldInput); err != nil || oldInput != 2500 {
		t.Fatal("historical price changed")
	}
	if err = l.AddPrice(Price{Protocol: "chat", Model: "bad", BilledModel: "bad", Output: 1, Verified: true}); err == nil {
		t.Fatal("certified price without bounds accepted")
	}
}
