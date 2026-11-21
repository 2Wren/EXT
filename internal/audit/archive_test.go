package audit

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cpa-ext/internal/store"
)

func TestExactHistoryAndRetention(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := "fixture"
	if _, err = db.Exec("INSERT INTO keys(id,name) VALUES(?,?)", key, key); err != nil {
		t.Fatal(err)
	}
	archive, err := New(db, root, 24*time.Hour, 1<<30, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	first := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	second := "ffffffffffffffffffffffffffffffff"
	turns := []*Turn{
		{ID: first, KeyID: key, Protocol: "chat", Started: time.Now(), Status: 200,
			Request:  []byte(`{"messages":[{"role":"user","content":"hello"}]}`),
			Response: []byte(`{"id":"one","choices":[{"message":{"content":"hi"}}]}`)},
		{ID: second, KeyID: key, Protocol: "chat", Started: time.Now(), Status: 200,
			Request:  []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"again"}]}`),
			Response: []byte(`{"id":"two","choices":[{"message":{"content":"finished"}}]}`)},
	}
	for _, turn := range turns {
		if err = archive.save(turn); err != nil {
			t.Fatal(err)
		}
	}
	var session string
	if err = db.QueryRow("SELECT session FROM turns WHERE id=?", second).Scan(&session); err != nil || session != first {
		t.Fatalf("session=%q err=%v", session, err)
	}
	path := filepath.Join(root, "bodies", first+".json.gz")
	old := time.Now().Add(-7 * 24 * time.Hour)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err = archive.Prune(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = archive.Read(first); !os.IsNotExist(err) {
		t.Fatalf("expired body remains: %v", err)
	}
	var state sql.NullString
	if err = db.QueryRow("SELECT audit_state FROM turns WHERE id=?", first).Scan(&state); err != nil || state.String != "expired" {
		t.Fatalf("state=%q err=%v", state.String, err)
	}
}
