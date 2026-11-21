package billing

import (
	"database/sql"
	"encoding/hex"
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
			k.room = 0
		}
		return nil
	}
	return err
}

// SyncKeys applies a successful complete snapshot without deleting policy/history.
func (l *Ledger) SyncKeys(snapshot []SyncedKey) error {
	present := make(map[string]SyncedKey, len(snapshot))
	for _, k := range snapshot {
		if len(k.ID) != 64 || k.ID != strings.ToLower(k.ID) || k.MaskedKey == "" {
			return errors.New("invalid synchronized key")
		}
		if _, err := hex.DecodeString(k.ID); err != nil {
			return errors.New("invalid synchronized key")
		}
		present[k.ID] = k
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO cpa_keys(id,masked_key,cpa_present) SELECT id,'',0 FROM keys WHERE 1 ON CONFLICT(id) DO UPDATE SET cpa_present=0"); err != nil {
		return err
	}
	for id, k := range present {
		if _, err = tx.Exec("INSERT INTO keys(id,name,enabled,daily,total) VALUES(?,?,1,0,0) ON CONFLICT(id) DO NOTHING", id, k.MaskedKey); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO cpa_keys(id,masked_key,cpa_present) VALUES(?,?,1) ON CONFLICT(id) DO UPDATE SET masked_key=excluded.masked_key,cpa_present=1", id, k.MaskedKey); err != nil {
			return err
		}
	}
	for id, k := range l.keys {
		if _, ok := present[id]; !ok && k.lease != nil && k.lease.free > 0 {
			if _, err = tx.Exec("UPDATE leases SET held=held-? WHERE id=?", k.lease.free, k.lease.id); err != nil {
				return err
			}
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
		if !k.present {
			k.room = 0
			if k.lease != nil {
				k.lease.free = 0
			}
		}
	}
	for id, k := range present {
		if l.keys[id] == nil {
			l.keys[id] = &keyState{Key: Key{ID: id, Name: k.MaskedKey, Enabled: true}, present: true}
		}
	}
	return nil
}
