package statusline

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func costPayload(session string, usd float64, api int64, model, repo string) []byte {
	return []byte(fmt.Sprintf(`{
	  "session_id": %q,
	  "cwd": "/Users/x/repositories/worktrees/some-worktree",
	  "model": {"display_name": %q},
	  "workspace": {"project_dir": "/Users/x/repositories/worktrees/some-worktree", "repo": {"name": %q}},
	  "cost": {"total_cost_usd": %v, "total_api_duration_ms": %d, "total_lines_added": 10, "total_lines_removed": 2}
	}`, session, model, repo, usd, api))
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Every render that moves the totals adds one line; a render that does not
// adds nothing.
func TestStoreAppendsToTheLedgerOnlyWhenTotalsMove(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 9, 30, 0, 0, time.Local)
	for _, usd := range []float64{0.5, 0.5, 1.25, 1.25, 2} {
		if err := store(dir, costPayload("s1", usd, int64(1000*usd), "Opus 5.5", "gutenberg"), at); err != nil {
			t.Fatal(err)
		}
	}
	entries := NewLedger(dir).Day(at)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	first := entries[0]
	if first.From.USD != 0 || first.To.USD != 0.5 {
		t.Errorf("first entry %+v; want 0 -> 0.5", first)
	}
	if first.Model != "Opus 5.5" || first.Repo != "gutenberg" {
		t.Errorf("first entry model %q repo %q", first.Model, first.Repo)
	}
	if entries[2].From.USD != 1.25 || entries[2].To.USD != 2 {
		t.Errorf("last entry %+v; want 1.25 -> 2", entries[2])
	}
}

// A payload without a cost block is an older Claude Code, and has nothing to
// record.
func TestStoreWithoutCostWritesNoLedger(t *testing.T) {
	dir := t.TempDir()
	if err := store(dir, []byte(`{"session_id":"s1"}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LedgerDir(dir)); !os.IsNotExist(err) {
		t.Errorf("ledger directory exists: %v", err)
	}
}

// The ledger directory sits inside the statusline directory, and must not
// trip up the scan for the freshest session file.
func TestAccountIgnoresTheLedgerDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := store(dir, costPayload("s1", 1, 1, "Opus 5.5", "r"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := Account(dir); !ok {
		t.Error("Account() did not find the session file")
	}
}

func TestRepoPrefersClaudeCodesAnswer(t *testing.T) {
	for raw, want := range map[string]string{
		`{"workspace":{"project_dir":"/r/wt","repo":{"name":"gutenberg"}}}`: "gutenberg",
		`{"workspace":{"project_dir":"/r/wordpress-develop","repo":null}}`:  "wordpress-develop",
		`{"cwd":"/r/scratch"}`: "scratch",
		`{}`:                   "",
	} {
		if got := repoOf(parse(t, raw)); got != want {
			t.Errorf("repoOf(%s) = %q, want %q", raw, got, want)
		}
	}
}

func entry(at int64, session string, from, to float64) LedgerEntry {
	return LedgerEntry{At: at, Session: session, From: Totals{USD: from}, To: Totals{USD: to}}
}

func sum(spends []Spend) float64 {
	var s float64
	for _, x := range spends {
		s += x.Delta.USD
	}
	return s
}

func TestSpendsAddsEachIncrease(t *testing.T) {
	got := Spends([]LedgerEntry{
		entry(1, "a", 0, 1),
		entry(2, "b", 0, 3),
		entry(3, "a", 1, 2.5),
	})
	if len(got) != 3 || !near(sum(got), 5.5) {
		t.Errorf("Spends = %+v; want three spends totalling 5.5", got)
	}
}

// Two renders racing each read the same previous total; the second line
// must only count what it adds on top of the first.
func TestSpendsCountsARaceOnce(t *testing.T) {
	got := Spends([]LedgerEntry{
		entry(1, "a", 1, 2),
		entry(1, "a", 1, 2),
		entry(2, "a", 1, 2.5),
	})
	if !near(sum(got), 1.5) {
		t.Errorf("sum = %v; want 1.5", sum(got))
	}
}

// A session that started before midnight opens today's file part way through
// its total; only what it spent today counts.
func TestSpendsStartsFromTheFirstLinesStartingPoint(t *testing.T) {
	got := Spends([]LedgerEntry{entry(1, "a", 40, 41)})
	if !near(sum(got), 1) {
		t.Errorf("sum = %v; want 1", sum(got))
	}
}

// A resumed session starts its counters over.
func TestSpendsTreatsADropAsAReset(t *testing.T) {
	got := Spends([]LedgerEntry{
		entry(1, "a", 0, 5),
		entry(2, "a", 5, 0.25),
		entry(3, "a", 0.25, 1),
	})
	if !near(sum(got), 6) {
		t.Errorf("sum = %v; want 6", sum(got))
	}
}

// The reader picks up appended lines without rereading the file, and skips
// a line still being written.
func TestLedgerReadsIncrementally(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	l := NewLedger(dir)
	if got := l.Day(at); len(got) != 0 {
		t.Fatalf("empty ledger returned %d entries", len(got))
	}
	_ = store(dir, costPayload("s1", 1, 1, "m", "r"), at)
	if got := l.Day(at); len(got) != 1 {
		t.Fatalf("after one write: %d entries", len(got))
	}
	f, _ := os.OpenFile(ledgerFile(dir, at), os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.WriteString(`{"t":1,"s":"s2","from":{"usd":0},"to":{"usd":`)
	f.Close()
	if got := l.Day(at); len(got) != 1 {
		t.Fatalf("a partial line was read: %d entries", len(got))
	}
	f, _ = os.OpenFile(ledgerFile(dir, at), os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.WriteString("3}}\n")
	f.Close()
	if got := l.Day(at); len(got) != 2 || got[1].To.USD != 3 {
		t.Fatalf("after completing the line: %+v", got)
	}
	// A new day is a new file.
	if got := l.Day(at.Add(24 * time.Hour)); len(got) != 0 {
		t.Errorf("tomorrow returned %d entries", len(got))
	}
}

func TestLedgerPrunesOldDays(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	if err := os.MkdirAll(LedgerDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	old := ledgerFile(dir, now.AddDate(0, 0, -45))
	recent := ledgerFile(dir, now.AddDate(0, 0, -3))
	for _, p := range []string{old, recent} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	NewLedger(dir).Day(now)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("%s survived", filepath.Base(old))
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("%s was removed", filepath.Base(recent))
	}
}

// Two renders racing across a reset both record the same drop; the second
// must not count the new counters again, and later spend still counts.
func TestSpendsCountsARacedResetOnce(t *testing.T) {
	got := Spends([]LedgerEntry{
		entry(1, "a", 0, 5),
		entry(2, "a", 5, 0.25),
		entry(2, "a", 5, 0.25),
		entry(3, "a", 0.25, 1),
		entry(4, "a", 1, 0.5),
	})
	if !near(sum(got), 6.5) {
		t.Errorf("sum = %v; want 6.5", sum(got))
	}
}

// A session first seen today that has been running since before midnight
// has an unknown share of its total in today; only what follows counts.
func TestFirstSightingOfAnOldSessionCountsNothing(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	old := fmt.Sprintf(`{"session_id":"s1","cost":{"total_cost_usd":%v,"total_duration_ms":%d}}`, 40.0, (12 * time.Hour).Milliseconds())
	if err := store(dir, []byte(old), at); err != nil {
		t.Fatal(err)
	}
	later := fmt.Sprintf(`{"session_id":"s1","cost":{"total_cost_usd":%v,"total_duration_ms":%d}}`, 41.5, (13 * time.Hour).Milliseconds())
	if err := store(dir, []byte(later), at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := sum(Spends(NewLedger(dir).Day(at))); !near(got, 1.5) {
		t.Errorf("today = %v; want 1.5", got)
	}
}

// A session that started today counts from zero on its first render.
func TestFirstSightingOfANewSessionCountsEverything(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	raw := `{"session_id":"s1","cost":{"total_cost_usd":0.75,"total_duration_ms":60000}}`
	if err := store(dir, []byte(raw), at); err != nil {
		t.Fatal(err)
	}
	if got := sum(Spends(NewLedger(dir).Day(at))); !near(got, 0.75) {
		t.Errorf("today = %v; want 0.75", got)
	}
}

func TestLedgerFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	at := time.Now()
	if err := store(dir, costPayload("s1", 1, 1, "m", "r"), at); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ledgerFile(dir, at))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("ledger mode %o; want 600", perm)
	}
}
