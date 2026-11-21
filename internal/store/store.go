package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schema string

func Open(dir string) (*sql.DB, error) {
	path := url.URL{Scheme: "file", Path: filepath.Join(dir, "ledger.sqlite")}
	db, err := sql.Open("sqlite3", path.String()+"?_busy_timeout=5000&_foreign_keys=on&_synchronous=FULL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Query(db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func RedactJSON(data []byte) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return "", err
	}
	if object == nil {
		return "", fmt.Errorf("expected usage object")
	}
	for _, name := range []string{"api_key", "access_token_sha256", "auth_id", "source", "base_url", "client_ip", "resolved_client_ip", "x_forwarded_for", "user_agent", "response_headers", "fail"} {
		delete(object, name)
	}
	raw, err := json.Marshal(object)
	return string(raw), err
}
