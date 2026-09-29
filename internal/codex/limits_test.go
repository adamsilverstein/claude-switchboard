package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func limitsLine(ts string, fiveUsed float64, fiveResets int64, weekUsed float64, weekResets int64) string {
	return line(ts, "event_msg", `{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex",`+
		`"primary":{"used_percent":`+ftoa(fiveUsed)+`,"window_minutes":300,"resets_at":`+itoa(fiveResets)+`},`+
		`"secondary":{"used_percent":`+ftoa(weekUsed)+`,"window_minutes":10080,"resets_at":`+itoa(weekResets)+`}}}`)
}

func TestLimitsTakesNewestReadingAcrossRollouts(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	later := now.Add(3 * time.Hour).Unix()
	week := now.Add(5 * 24 * time.Hour).Unix()

	older := filepath.Join(home, "sessions", "2026", "09", "28", "a.jsonl")
	write(t, older, limitsLine("2026-09-29T11:00:00Z", 90, later, 50, week))
	newer := filepath.Join(home, "sessions", "2026", "09", "29", "b.jsonl")
	write(t, newer, limitsLine("2026-09-29T11:30:00Z", 63, later, 37, week)+
		line("2026-09-29T11:31:00Z", "response_item", agentMsg))
	// The newest file of all is a session that has made no request yet.
	fresh := filepath.Join(home, "sessions", "2026", "09", "29", "c.jsonl")
	write(t, fresh, meta("c", "/r", "2026-09-29T11:59:00Z", `"cli"`, "codex-tui"))
	touch(t, older, now.Add(-time.Hour))
	touch(t, newer, now.Add(-30*time.Minute))
	touch(t, fresh, now.Add(-time.Minute))

	l := newScanner(home, nil, nil).Limits(now)
	if l.FiveHour == nil || l.FiveHour.UsedPct != 63 || l.FiveHour.Resets.Unix() != later {
		t.Errorf("FiveHour = %+v", l.FiveHour)
	}
	if l.Weekly == nil || l.Weekly.UsedPct != 37 || l.Weekly.Resets.Unix() != week {
		t.Errorf("Weekly = %+v", l.Weekly)
	}
}

func TestLimitsAfterResetAreUnknown(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	write(t, filepath.Join(home, "sessions", "a.jsonl"),
		limitsLine("2026-09-29T06:00:00Z", 88, now.Add(-time.Minute).Unix(), 40, now.Add(time.Hour).Unix()))
	l := newScanner(home, nil, nil).Limits(now)
	if l.FiveHour != nil {
		t.Errorf("a window past its reset is unknown, not zero; got %+v", l.FiveHour)
	}
	if l.Weekly == nil || l.Weekly.UsedPct != 40 {
		t.Errorf("Weekly = %+v", l.Weekly)
	}
}

func TestLimitsWithoutCodex(t *testing.T) {
	if l := newScanner(t.TempDir(), nil, nil).Limits(time.Now()); l.Any() {
		t.Errorf("got %+v, want nothing", l)
	}
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func itoa(n int64) string   { return strconv.FormatInt(n, 10) }

func TestLimitsIgnoreOtherQuotasAndLooseLengths(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	resets := itoa(now.Add(time.Hour).Unix())
	body := line("2026-09-29T11:00:00Z", "event_msg", `{"type":"token_count","rate_limits":{"limit_id":"codex",`+
		`"primary":{"used_percent":20,"window_minutes":299,"resets_at":`+resets+`},`+
		`"secondary":{"used_percent":30,"window_minutes":10079,"resets_at":`+resets+`}}}`) +
		// A newer, model-specific quota must not replace the account's.
		line("2026-09-29T11:05:00Z", "event_msg", `{"type":"token_count","rate_limits":{"limit_id":"codex_other",`+
			`"primary":{"used_percent":99,"window_minutes":300,"resets_at":`+resets+`}}}`)
	write(t, filepath.Join(home, "sessions", "a.jsonl"), body)
	l := newScanner(home, nil, nil).Limits(now)
	if l.FiveHour == nil || l.FiveHour.UsedPct != 20 {
		t.Errorf("FiveHour = %+v, want 20", l.FiveHour)
	}
	if l.Weekly == nil || l.Weekly.UsedPct != 30 {
		t.Errorf("Weekly = %+v, want 30", l.Weekly)
	}
}

func TestLimitsLookPastManyRolloutsWithoutReadings(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	old := filepath.Join(home, "sessions", "old.jsonl")
	write(t, old, limitsLine("2026-09-29T09:00:00Z", 44, now.Add(time.Hour).Unix(), 10, now.Add(time.Hour).Unix()))
	touch(t, old, now.Add(-3*time.Hour))
	for i := 0; i < 20; i++ {
		p := filepath.Join(home, "sessions", "fresh"+itoa(int64(i))+".jsonl")
		write(t, p, meta("f", "/r", "2026-09-29T11:00:00Z", `"cli"`, "codex-tui"))
		touch(t, p, now.Add(-time.Duration(i)*time.Minute))
	}
	if l := newScanner(home, nil, nil).Limits(now); l.FiveHour == nil || l.FiveHour.UsedPct != 44 {
		t.Errorf("FiveHour = %+v, want the reading behind twenty empty rollouts", l.FiveHour)
	}
}
