package billing

import (
	"errors"
	"strings"
	"testing"
	"time"

	"cpa-ext/internal/store"
)

func TestResetStart(t *testing.T) {
	monday, _ := ParseReset("")
	sundayEvening, _ := ParseReset("0 18:00")
	for _, c := range []struct {
		reset Reset
		now   time.Time
		want  time.Time
	}{
		{monday, time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai), time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai)},
		{monday, time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai), time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai)},
		{monday, time.Date(2026, 11, 22, 23, 59, 59, 0, Shanghai), time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai)},
		{monday, time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC), time.Date(2026, 11, 23, 0, 0, 0, 0, Shanghai)}, // Monday 00:00 Beijing
		{monday, time.Date(2026, 11, 22, 15, 59, 0, 0, time.UTC), time.Date(2026, 11, 16, 0, 0, 0, 0, Shanghai)},
		{sundayEvening, time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai), time.Date(2026, 11, 15, 18, 0, 0, 0, Shanghai)},
		{sundayEvening, time.Date(2026, 11, 22, 17, 59, 0, 0, Shanghai), time.Date(2026, 11, 15, 18, 0, 0, 0, Shanghai)},
		{sundayEvening, time.Date(2026, 11, 22, 18, 0, 0, 0, Shanghai), time.Date(2026, 11, 22, 18, 0, 0, 0, Shanghai)},
	} {
		if got := c.reset.Start(c.now); !got.Equal(c.want) {
			t.Errorf("Start(%s)=%s, want %s", c.now, got, c.want)
		}
	}
}

func TestParseReset(t *testing.T) {
	for _, text := range []string{"", "0 18:00", "6 00:00", "4 23:59"} {
		if _, err := ParseReset(text); err != nil {
			t.Errorf("%q rejected: %v", text, err)
		}
	}
	for _, text := range []string{"7 00:00", "-1 00:00", "10 00:00", "1 24:00", "1 08:60", "1 8:00", "1 08:0", "1", "monday", "1 08:00 extra"} {
		if _, err := ParseReset(text); err == nil {
			t.Errorf("%q accepted", text)
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
	spend := func(event string, at time.Time, amount int64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,cost)
 VALUES(?,?,'','','','p','m','m','',1,1,0,0,2,5,1,0,?,'{}',?)`, event, key, at.UnixMilli(), amount); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO spend(event_id,key_id,day,amount) VALUES(?,?,?,?)", event, key, Day(at), amount); err != nil {
			t.Fatal(err)
		}
	}
	admit := func(want error, why string) {
		t.Helper()
		if _, err := ledger.Admit(key, "chat", "m"); !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", why, err, want)
		}
	}
	limit(0, 0)
	spend("last-week", time.Date(2026, 11, 15, 12, 0, 0, 0, Shanghai), 60)
	spend("monday", time.Date(2026, 11, 16, 12, 0, 0, 0, Shanghai), 99)
	admit(nil, "no limits")
	limit(100, 0)
	admit(nil, "99 of 100 spent this week")
	spend("thursday", time.Date(2026, 11, 19, 12, 0, 0, 0, Shanghai), 1)
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

func TestWeeklyResetSchedule(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ledger, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai) // Thursday
	ledger.now = func() time.Time { return now }
	key := strings.Repeat("c", 64)
	if err = ledger.UpdateKey(Key{ID: key, Weekly: 100, WeekReset: "0 18:00"}); err != nil {
		t.Fatal(err)
	}
	spend := func(event string, at time.Time, amount int64) {
		t.Helper()
		if _, err = db.Exec(`INSERT INTO events(id,key_id,trace,request_id,session,provider,model,billed_model,auth_index,input,output,cached,cache_write,total,latency,ttft,failed,happened,body,cost)
 VALUES(?,?,'','','','p','m','m','',1,1,0,0,2,5,1,0,?,'{}',?)`, event, key, at.UnixMilli(), amount); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO spend(event_id,key_id,day,amount) VALUES(?,?,?,?)", event, key, Day(at), amount); err != nil {
			t.Fatal(err)
		}
	}
	admit := func(want error, why string) {
		t.Helper()
		if _, err = ledger.Admit(key, "chat", "m"); !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", why, err, want)
		}
	}
	// The window started on Sunday the 15th at 18:00 Beijing time.
	spend("before", time.Date(2026, 11, 15, 17, 59, 0, 0, Shanghai), 100)
	spend("within", time.Date(2026, 11, 15, 18, 0, 0, 0, Shanghai), 99)
	admit(nil, "spend before the reset does not count")
	spend("more", time.Date(2026, 11, 16, 8, 0, 0, 0, Shanghai), 1)
	admit(ErrWeekly, "limit used up inside the window")
	// The next reset opens a new window.
	now = time.Date(2026, 11, 22, 18, 0, 0, 0, Shanghai)
	admit(nil, "a new week starts at the configured time")
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, Shanghai) }
	if _, err = restarted.Admit(key, "chat", "m"); !errors.Is(err, ErrWeekly) {
		t.Fatal("reset schedule lost on restart", err)
	}
	if err = ledger.UpdateKey(Key{ID: key, WeekReset: "7 00:00"}); err == nil {
		t.Fatal("invalid reset accepted")
	}
}
