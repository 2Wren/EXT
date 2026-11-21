package store

import (
	"database/sql"
	"path/filepath"
	"strings"
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

func TestLegacyQuotaSchemaIsReduced(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE keys (id TEXT PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL, daily INTEGER NOT NULL, total INTEGER NOT NULL);
CREATE TABLE leases (id INTEGER PRIMARY KEY, key_id TEXT NOT NULL REFERENCES keys(id), day TEXT NOT NULL, held INTEGER NOT NULL);
CREATE INDEX lease_key ON leases(key_id, day);
CREATE TABLE turns (
 id TEXT PRIMARY KEY, key_id TEXT NOT NULL REFERENCES keys(id), day TEXT NOT NULL, started INTEGER NOT NULL,
 protocol TEXT NOT NULL, model TEXT NOT NULL, session TEXT NOT NULL, parent_response TEXT NOT NULL,
 response_id TEXT NOT NULL, trace TEXT NOT NULL, price_id INTEGER NOT NULL, lease_id INTEGER NOT NULL,
 reserved INTEGER NOT NULL, status INTEGER NOT NULL, audit_state TEXT NOT NULL,
 latency INTEGER NOT NULL, ttfb INTEGER NOT NULL, settled INTEGER NOT NULL DEFAULT 0
);
INSERT INTO keys VALUES('k','Mine',0,5,7);
INSERT INTO leases VALUES(1,'k','2026-11-21',3);
INSERT INTO turns VALUES('t','k','2026-11-21',1,'chat','m','s','','','trace',4,1,3,200,'complete',9,2,1)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		if db, err = Open(dir); err != nil {
			t.Fatal(err)
		}
		var name, models, columns string
		var weekly, total, price, status, settled, leases int64
		if err = db.QueryRow("SELECT name,weekly,total,models FROM keys WHERE id='k'").Scan(&name, &weekly, &total, &models); err != nil || name != "Mine" || weekly != 0 || total != 7 || models != "" {
			t.Fatal("key", name, weekly, total, models, err)
		}
		if err = db.QueryRow("SELECT price_id,status,settled FROM turns WHERE id='t'").Scan(&price, &status, &settled); err != nil || price != 4 || status != 200 || settled != 1 {
			t.Fatal("turn", price, status, settled, err)
		}
		if err = db.QueryRow("SELECT group_concat(name) FROM (SELECT name FROM pragma_table_info('keys') UNION ALL SELECT name FROM pragma_table_info('turns'))").Scan(&columns); err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"enabled", "daily", "lease_id", "reserved"} {
			if strings.Contains(","+columns+",", ","+gone+",") {
				t.Fatal("column kept", gone, columns)
			}
		}
		if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('leases','lease_key')").Scan(&leases); err != nil || leases != 0 {
			t.Fatal("leases kept", leases, err)
		}
		if _, err = db.Exec("INSERT INTO keys(id,name,weekly,total) VALUES('new','n',0,0)"); err != nil && !strings.Contains(err.Error(), "UNIQUE") {
			t.Fatal("legacy table rejects a new key", err)
		}
		db.Close()
	}
}
