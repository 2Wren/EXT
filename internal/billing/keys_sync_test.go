package billing

import (
	"cpa-ext/internal/store"
	"strings"
	"testing"
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
	if _, err = l.Admit(id, "chat", "unknown"); err != nil {
		t.Fatal("new key not unlimited", err)
	}
	if err = l.UpdateKey(Key{ID: id, Name: "ignored for a known key", Weekly: USD, Total: 2 * USD}); err != nil {
		t.Fatal(err)
	}
	if err = l.SyncKeys([]SyncedKey{k}); err != nil {
		t.Fatal(err)
	}
	var name string
	var weekly, total int64
	if err = db.QueryRow("SELECT name,weekly,total FROM keys WHERE id=?", id).Scan(&name, &weekly, &total); err != nil {
		t.Fatal(err)
	}
	if name != k.MaskedKey || weekly != USD || total != 2*USD {
		t.Fatal("policy replaced", name, weekly, total)
	}
	if _, err = l.Admit(id, "chat", "m"); err != nil {
		t.Fatal(err)
	}
	if err = l.SyncKeys([]SyncedKey{}); err != nil {
		t.Fatal(err)
	}
	if l.Enabled(id) {
		t.Fatal("deleted key enabled")
	}
	if _, err = l.Admit(id, "chat", "m"); err != ErrDisabled {
		t.Fatal("deleted key admitted", err)
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
	if err = l.UpdateKey(Key{ID: id}); err != nil {
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

// Keep the last-success marker current without rewriting unchanged keys.
func TestUnchangedKeySyncWrites(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	var keys []SyncedKey
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		keys = append(keys, SyncedKey{strings.Repeat(c, 64), "mask-" + c})
	}
	syncKeys := func(keys []SyncedKey) {
		t.Helper()
		if err := ledger.SyncKeys(keys); err != nil {
			t.Fatal(err)
		}
	}
	changes := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT total_changes()").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	syncKeys(keys)
	syncKeys(keys[:4])
	before := changes()
	syncKeys(keys[:4])
	if n := changes() - before; n != 1 {
		t.Fatalf("unchanged snapshot changed %d rows, want only the sync marker", n)
	}
	before = changes()
	syncKeys([]SyncedKey{keys[1], keys[0], keys[2], keys[3]})
	if n := changes() - before; n != 3 {
		t.Fatalf("swap changed %d rows, want two positions and the sync marker", n)
	}
	before = changes()
	named, err := ledger.NameKeys(map[string]string{keys[0].ID: "renamed", keys[1].ID: keys[1].MaskedKey})
	if err != nil || len(named) != 2 || changes()-before != 1 {
		t.Fatalf("expected two acknowledged names and one write: %v, %v", named, err)
	}
}

func TestAllowsModel(t *testing.T) {
	for _, c := range []struct {
		allowed []string
		model   string
		want    bool
	}{
		{nil, "anything", true},
		{[]string{"claude-opus-5"}, "claude-opus-5", true},
		{[]string{"claude-opus-5"}, "claude-opus-5-5", false},
		{[]string{"Claude-Opus-5"}, "devin/claude-opus-5", true},
		{[]string{"devin/claude-opus-5"}, "claude-opus-5", false},
		{[]string{"gemini-*"}, "models/gemini-3-pro", true},
		{[]string{"*-preview", "gpt-5"}, "gemini-3-pro-preview", true},
		{[]string{"claude-*-5"}, "claude-5", false},
		{[]string{"*fable*"}, "claude-fable-5-2", true},
		{[]string{"gpt-5"}, "", false},
	} {
		if got := AllowsModel(c.allowed, c.model); got != c.want {
			t.Errorf("AllowsModel(%q,%q)=%v", c.allowed, c.model, got)
		}
	}
}
