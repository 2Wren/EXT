package billing

import (
	"strings"
	"testing"
	"time"

	"cpa-ext/internal/store"
)

func TestWeekStartsMondayBeijingTime(t *testing.T) {
	for _, c := range []struct {
		now  time.Time
		week string
	}{
		{time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai), "2026-11-16"},    // Monday midnight
		{time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai), "2026-11-16"},   // Thursday
		{time.Date(2026, 11, 22, 23, 59, 59, 0, Shanghai), "2026-11-16"}, // Sunday night
		{time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC), "2026-11-23"},   // Monday 00:00 in Beijing
		{time.Date(2026, 11, 22, 15, 59, 0, 0, time.UTC), "2026-11-16"},
	} {
		if got := Week(c.now); got != c.week {
			t.Errorf("Week(%s)=%s, want %s", c.now, got, c.week)
		}
	}
}

func TestWeeklyAndTotalLimits(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai) // Thursday; the week began on the 16th
	ledger.now = func() time.Time { return now }
	key := strings.Repeat("a", 64)
	limit := func(weekly, total int64) {
		t.Helper()
		if err := ledger.UpdateKey(Key{ID: key, Name: "test", Weekly: weekly, Total: total}); err != nil {
			t.Fatal(err)
		}
	}
	spend := func(event, day string, amount int64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,cost)
 VALUES(?,?,'','','','p','m','m','',1,1,0,0,2,5,1,0,1000,'{}',?)`, event, key, amount); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO spend(event_id,key_id,day,amount) VALUES(?,?,?,?)", event, key, day, amount); err != nil {
			t.Fatal(err)
		}
	}
	admit := func(want error, why string) {
		t.Helper()
		if _, err := ledger.Admit(key, "chat", "m"); err != want {
			t.Fatalf("%s: got %v, want %v", why, err, want)
		}
	}
	limit(0, 0)
	spend("last-week", "2026-11-15", 60)
	spend("monday", "2026-11-16", 99)
	admit(nil, "no limits")
	limit(100, 0)
	admit(nil, "99 of 100 spent this week")
	spend("thursday", "2026-11-19", 1)
	admit(ErrWeekly, "weekly limit used up")
	limit(0, 160)
	admit(ErrTotal, "total limit used up")
	limit(0, 161)
	admit(nil, "total limit not reached")
	limit(100, 1000)
	admit(ErrWeekly, "the limit that runs out first applies")
	limit(1000, 160)
	admit(ErrTotal, "the limit that runs out first applies")
	now = time.Date(2026, 11, 23, 0, 0, 0, 0, Shanghai)
	limit(100, 0)
	admit(nil, "a new week starts on Monday")
	limit(100, 160)
	admit(ErrTotal, "the total limit does not reset")
	if _, err := ledger.Admit(strings.Repeat("b", 64), "chat", "m"); err != ErrDisabled {
		t.Fatal("unknown key admitted", err)
	}
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Admit(key, "chat", "m"); err != ErrTotal {
		t.Fatal("limits lost on restart", err)
	}
	if err = ledger.UpdateKey(Key{ID: key, Weekly: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}
