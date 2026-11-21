package usage

import (
	"context"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
	"cpa-ext/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKeySyncFailureKeepsCache(t *testing.T) {
	body := `["sk-test-secret"]`
	status := 200
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte(body)) }))
	defer s.Close()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := billing.New(db)
	if err != nil {
		t.Fatal(err)
	}
	c := New(db, cpa.Client{Base: s.URL, HTTP: s.Client()}, l)
	if err = c.SyncKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := c.KeySyncStatus()
	if before.Count != 1 || before.LastSuccess.IsZero() {
		t.Fatal(before)
	}
	status = 503
	if err = c.SyncKeys(context.Background()); err == nil {
		t.Fatal("missing failure")
	}
	after := c.KeySyncStatus()
	if after.Count != 1 || after.Error == "" || !after.LastSuccess.Equal(before.LastSuccess) {
		t.Fatal("failed sync mutated cache", after)
	}
	if !l.Enabled(cpa.KeyID("sk-test-secret")) {
		t.Fatal("failed sync disabled key")
	}
	var masked string
	db.QueryRow("SELECT masked_key FROM cpa_keys").Scan(&masked)
	if strings.Contains(masked, "sk-test-secret") {
		t.Fatal("raw key stored")
	}
	status = 200
	body = `null`
	if err = c.SyncKeys(context.Background()); err == nil {
		t.Fatal("null accepted")
	}
	if !l.Enabled(cpa.KeyID("sk-test-secret")) {
		t.Fatal("null cleared cache")
	}
	body = `[]`
	if err = c.SyncKeys(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.KeySyncStatus().Count != 0 || l.Enabled(cpa.KeyID("sk-test-secret")) {
		t.Fatal("empty snapshot not applied")
	}
}
