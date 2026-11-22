package billing

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SyncedKey struct{ ID, MaskedKey string }

// InitializeKeySync gates unsynchronized legacy policies on first startup.
func (l *Ledger) InitializeKeySync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var last string
	err := l.db.QueryRow("SELECT last_success FROM cpa_keys_sync WHERE id=1").Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		for _, k := range l.keys {
			k.present = false
		}
		return nil
	}
	return err
}

// NameKeys renames synchronized keys without touching their policy and returns
// the names that were applied.
func (l *Ledger) NameKeys(names map[string]string) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	applied := map[string]string{}
	tx, err := l.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for id, name := range names {
		key := l.keys[id]
		if key == nil {
			continue
		}
		if key.Name != name {
			if _, err = tx.Exec("UPDATE keys SET name=? WHERE id=?", name, id); err != nil {
				return nil, err
			}
		}
		applied[id] = name
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for id, name := range applied {
		l.keys[id].Name = name
	}
	return applied, nil
}

// SyncKeys applies a successful complete snapshot without deleting policy/history.
func (l *Ledger) SyncKeys(snapshot []SyncedKey) error {
	type listedKey struct {
		SyncedKey
		position int
	}
	present := make(map[string]listedKey, len(snapshot))
	ids := make([]string, 0, len(snapshot))
	for position, k := range snapshot {
		if len(k.ID) != 64 || k.ID != strings.ToLower(k.ID) || k.MaskedKey == "" {
			return errors.New("invalid synchronized key")
		}
		if _, err := hex.DecodeString(k.ID); err != nil {
			return errors.New("invalid synchronized key")
		}
		if _, exists := present[k.ID]; !exists {
			present[k.ID] = listedKey{SyncedKey: k, position: position}
			ids = append(ids, k.ID)
		}
	}
	activeIDs, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO cpa_keys(id,masked_key,cpa_present)
 SELECT id,'',0 FROM keys WHERE id NOT IN (SELECT value FROM json_each(?))
 ON CONFLICT(id) DO UPDATE SET cpa_present=0,position=NULL
 WHERE cpa_keys.cpa_present<>0 OR cpa_keys.position IS NOT NULL`, string(activeIDs)); err != nil {
		return err
	}
	for id, k := range present {
		if _, err = tx.Exec("INSERT INTO keys(id,name,weekly,total) VALUES(?,?,0,0) ON CONFLICT(id) DO NOTHING", id, k.MaskedKey); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO cpa_keys(id,masked_key,cpa_present,position) VALUES(?,?,1,?)
 ON CONFLICT(id) DO UPDATE SET masked_key=excluded.masked_key,cpa_present=1,position=excluded.position
 WHERE cpa_keys.cpa_present<>1 OR cpa_keys.position IS NOT excluded.position
    OR cpa_keys.masked_key<>excluded.masked_key`, id, k.MaskedKey, k.position); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO cpa_keys_sync(id,last_success) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET last_success=excluded.last_success", l.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for id, k := range l.keys {
		_, k.present = present[id]
	}
	for id, k := range present {
		if l.keys[id] == nil {
			l.keys[id] = &keyState{Key: Key{ID: id, Name: k.MaskedKey}, present: true}
		}
	}
	return nil
}
