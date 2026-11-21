package billing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpa-ext/internal/store"
)

func TestConcurrentRequestsAndCrashHold(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ledger.SetHealthy(true)
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err = ledger.UpdateKey(Key{ID: key, Name: "test", Enabled: true, Daily: 300_000_000, Total: 300_000_000}); err != nil {
		t.Fatal(err)
	}
	if err = ledger.AddPrice(Price{Protocol: "chat", Model: "test", BilledModel: "test", MaxInput: 1, MaxOutput: 1, Output: 200_000_000, Verified: true}); err != nil {
		t.Fatal(err)
	}
	if err = ledger.Refresh(); err != nil {
		t.Fatal(err)
	}
	day := Day(time.Now())
	var success, refused atomic.Int64
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := ledger.Admit(key, "chat", "test", 1, true, day)
			switch err {
			case nil:
				success.Add(1)
			case ErrExceeded:
				refused.Add(1)
			default:
				t.Errorf("unexpected admission result: %v", err)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 || refused.Load() != 1 {
		t.Fatalf("admitted=%d refused=%d", success.Load(), refused.Load())
	}
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetHealthy(true)
	if err = restarted.Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Admit(key, "chat", "test", 1, true, day); err != ErrExceeded {
		t.Fatalf("crash released reserved amount: %v", err)
	}
}
