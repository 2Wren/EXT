package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestLegacyPricesMigrateWithoutRewritingIDs(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE prices(id INTEGER PRIMARY KEY,protocol TEXT NOT NULL,model TEXT NOT NULL,billed_model TEXT NOT NULL,input INTEGER NOT NULL,output INTEGER NOT NULL,cached INTEGER NOT NULL,cache_write INTEGER NOT NULL,max_input INTEGER NOT NULL,max_output INTEGER NOT NULL,verified INTEGER NOT NULL);INSERT INTO prices VALUES(17,'chat','custom','custom',1,2,0,0,1000,20,1)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	var input int64
	if err = db.QueryRow("SELECT source,input FROM prices WHERE id=17").Scan(&source, &input); err != nil || source != "manual" || input != 1 {
		t.Fatalf("migration: %q %d %v", source, input, err)
	}
	db.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
}
