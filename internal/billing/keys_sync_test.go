package billing

import (
	"cpa-ext/internal/store"
	"strings"
	"testing"
	"time"
)

func TestKeySyncPolicyDeletionAndRestore(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	k := SyncedKey{id, "sk-a…test"}
	if err = l.SyncKeys([]SyncedKey{k, k}); err != nil {
		t.Fatal(err)
	}
	if !l.Enabled(id) {
		t.Fatal("new key not enabled")
	}
	if _, err = l.Admit(id, "chat", "unknown", 0, false, Day(time.Now())); err != nil {
		t.Fatal("new key not unlimited", err)
	}
	if err = l.UpdateKey(Key{ID: id, Name: "Alice", Enabled: false, Daily: USD, Total: 2 * USD}); err != nil {
		t.Fatal(err)
	}
	if err = l.SyncKeys([]SyncedKey{k}); err != nil {
		t.Fatal(err)
	}
	var name string
	var enabled bool
	var daily, total int64
	if err = db.QueryRow("SELECT name,enabled,daily,total FROM keys WHERE id=?", id).Scan(&name, &enabled, &daily, &total); err != nil {
		t.Fatal(err)
	}
	if name != "Alice" || enabled || daily != USD || total != 2*USD {
		t.Fatal("policy replaced")
	}
	if err = l.UpdateKey(Key{ID: id, Name: name, Enabled: true, Daily: daily, Total: total}); err != nil {
		t.Fatal(err)
	}
	l.SetHealthy(true)
	if err = l.AddPrice(Price{Protocol: "chat", Model: "m", BilledModel: "m", MaxInput: 1, MaxOutput: 1, Output: 100, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if err = l.Refresh(); err != nil {
		t.Fatal(err)
	}
	grant, err := l.Admit(id, "chat", "m", 1, true, Day(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if err = l.SyncKeys([]SyncedKey{}); err != nil {
		t.Fatal(err)
	}
	if l.Enabled(id) {
		t.Fatal("deleted key enabled")
	}
	if _, err = l.Admit(id, "chat", "m", 1, true, Day(time.Now())); err != ErrDisabled {
		t.Fatal("deleted key admitted", err)
	}
	var held int64
	if err = db.QueryRow("SELECT held FROM leases WHERE id=?", grant.LeaseID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != grant.Amount {
		t.Fatalf("inflight lost: held=%d grant=%d", held, grant.Amount)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM keys WHERE id=?", id).Scan(&count)
	if count != 1 {
		t.Fatal("history/policy key deleted")
	}
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.InitializeKeySync(); err != nil {
		t.Fatal(err)
	}
	if restarted.Enabled(id) {
		t.Fatal("deleted key resurrected on restart")
	}
	if err = l.SyncKeys([]SyncedKey{k}); err != nil {
		t.Fatal(err)
	}
	if !l.Enabled(id) {
		t.Fatal("restored key not enabled")
	}
}
func TestKeySyncFirstStartAndFailedSnapshot(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 64)
	if err = l.UpdateKey(Key{ID: id, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = l.InitializeKeySync(); err != nil {
		t.Fatal(err)
	}
	if l.Enabled(id) {
		t.Fatal("unsynced legacy key allowed")
	}
	if err = l.SyncKeys([]SyncedKey{{id, "mask"}}); err != nil {
		t.Fatal(err)
	}
	if err = l.SyncKeys([]SyncedKey{{"bad", "mask"}}); err == nil {
		t.Fatal("malformed snapshot accepted")
	}
	if !l.Enabled(id) {
		t.Fatal("failed snapshot cleared cached key")
	}
	if err = l.SyncKeys([]SyncedKey{}); err != nil {
		t.Fatal(err)
	}
	var last string
	if err = db.QueryRow("SELECT last_success FROM cpa_keys_sync WHERE id=1").Scan(&last); err != nil || last == "" {
		t.Fatal("empty snapshot marker missing", err)
	}
}
