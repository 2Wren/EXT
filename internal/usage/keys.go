package usage

import (
	"context"
	"cpa-ext/internal/billing"
	"cpa-ext/internal/cpa"
	"log"
	"time"
)

type KeySyncStatus struct {
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	Error       string    `json:"error"`
	Count       int       `json:"count"`
}

func (c *Collector) KeySyncStatus() KeySyncStatus {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	status := c.keysStatus
	if status.LastSuccess.IsZero() {
		var text string
		if c.db.QueryRow("SELECT last_success FROM cpa_keys_sync WHERE id=1").Scan(&text) == nil {
			status.LastSuccess, _ = time.Parse(time.RFC3339Nano, text)
		}
	}
	_ = c.db.QueryRow("SELECT count(*) FROM cpa_keys WHERE cpa_present=1").Scan(&status.Count)
	return status
}

// Serialize manual and periodic snapshots so an older response cannot win.
func (c *Collector) SyncKeys(ctx context.Context) error {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	c.keysStatus.LastAttempt = time.Now().UTC()
	poll, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	keys, err := c.cpa.Keys(poll)
	if err == nil {
		snapshot := make([]billing.SyncedKey, 0, len(keys))
		for _, key := range keys {
			snapshot = append(snapshot, billing.SyncedKey{ID: cpa.KeyID(key), MaskedKey: cpa.MaskKey(key)})
		}
		err = c.ledger.SyncKeys(snapshot)
	}
	if err != nil {
		c.keysStatus.Error = err.Error()
		return err
	}
	c.keysStatus.Error = ""
	c.keysStatus.LastSuccess = time.Now().UTC()
	c.keysStatus.Count = len(keys)
	return nil
}
func (c *Collector) runKeys(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := c.SyncKeys(ctx); err != nil && ctx.Err() == nil {
			log.Printf("CPA key synchronization: %v", err)
		}
	}
}
