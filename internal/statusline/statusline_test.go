package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const fullPayload = `{
  "session_id": "aaaa1111-2222-3333-4444-555566667777",
  "transcript_path": "/Users/x/.claude/projects/-repo/aaaa1111-2222-3333-4444-555566667777.jsonl",
  "cwd": "/Users/x/repositories/gutenberg",
  "model": {"id": "claude-opus-5", "display_name": "Opus 5"},
  "context_window": {
    "context_window_size": 1000000,
    "current_usage": {"input_tokens": 2, "output_tokens": 998,
                      "cache_creation_input_tokens": 3000, "cache_read_input_tokens": 154000},
    "used_percentage": 15.8, "remaining_percentage": 84.2
  },
  "rate_limits": {
    "five_hour": {"used_percentage": 31, "resets_at": 1787740000},
    "seven_day": {"used_percentage": 12.4, "resets_at": 1788109200}
  }
}`

func parse(t *testing.T, raw string) Payload {
	t.Helper()
	var p Payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPayloadFields(t *testing.T) {
	p := parse(t, fullPayload)
	if p.Model.DisplayName != "Opus 5" {
		t.Errorf("display name = %q", p.Model.DisplayName)
	}
	if p.ContextWindow.Size != 1000000 {
		t.Errorf("context window = %d", p.ContextWindow.Size)
	}
	if got, ok := p.Tokens(); !ok || got != 158000 {
		t.Errorf("Tokens() = %d, %v; want 158000, true", got, ok)
	}
	if pct, ok := p.RateLimits.FiveHour.Pct(); !ok || pct != 31 {
		t.Errorf("five hour = %d, %v; want 31, true", pct, ok)
	}
	// 12.4 rounds to 12, not truncates to 12 by accident.
	if pct, ok := p.RateLimits.SevenDay.Pct(); !ok || pct != 12 {
		t.Errorf("seven day = %d, %v; want 12, true", pct, ok)
	}
	if at, ok := p.RateLimits.SevenDay.Resets(); !ok || at.Unix() != 1788109200 {
		t.Errorf("seven day resets = %v, %v", at, ok)
	}
}

// The percentage is the fallback when the token breakdown is absent, since
// older builds send one without the other.
func TestTokensFallsBackToThePercentage(t *testing.T) {
	p := parse(t, `{"context_window":{"context_window_size":200000,"used_percentage":50}}`)
	if got, ok := p.Tokens(); !ok || got != 100000 {
		t.Errorf("Tokens() = %d, %v; want 100000, true", got, ok)
	}
}

func TestTokensReportsUnknown(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"context_window":{"context_window_size":200000}}`,
		`{"context_window":{"used_percentage":50}}`,
		`{"context_window":{"context_window_size":200000,"current_usage":null}}`,
	} {
		if got, ok := parse(t, raw).Tokens(); ok {
			t.Errorf("Tokens() on %s = %d, true; want unknown", raw, got)
		}
	}
}

// A null rate_limits block, or a null window inside it, is a reading that is
// unavailable rather than a reading of zero.
func TestNullWindowsAreUnknownNotZero(t *testing.T) {
	p := parse(t, `{"rate_limits":{"five_hour":null,"seven_day":{"used_percentage":null}}}`)
	if _, ok := p.RateLimits.FiveHour.Pct(); ok {
		t.Error("a null five_hour should read as unknown")
	}
	if _, ok := p.RateLimits.SevenDay.Pct(); ok {
		t.Error("a null used_percentage should read as unknown")
	}
	if _, ok := p.RateLimits.SevenDay.Resets(); ok {
		t.Error("a missing resets_at should read as unknown")
	}
}

// Claude Code may not name the session outright; the transcript filename is
// the same identifier the registry records.
func TestSessionFallsBackToTheTranscriptPath(t *testing.T) {
	p := parse(t, `{"transcript_path":"/Users/x/.claude/projects/-repo/bbbb2222.jsonl"}`)
	if got := p.Session(); got != "bbbb2222" {
		t.Errorf("Session() = %q, want %q", got, "bbbb2222")
	}
	if got := parse(t, `{}`).Session(); got != "" {
		t.Errorf("Session() = %q, want empty", got)
	}
}

func TestStoreAndRead(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, []byte(fullPayload)); err != nil {
		t.Fatal(err)
	}
	p, ok := Read(dir, "aaaa1111-2222-3333-4444-555566667777")
	if !ok {
		t.Fatal("Read found nothing after Store")
	}
	if p.Model.DisplayName != "Opus 5" {
		t.Errorf("round trip lost the display name: %+v", p.Model)
	}
	if _, ok := Read(dir, "no-such-session"); ok {
		t.Error("Read invented a payload for a session with no shim installed")
	}
}

// Store must not let a payload choose where it lands.
func TestStoreRejectsAPathAsASessionID(t *testing.T) {
	dir := t.TempDir()
	for _, raw := range []string{
		`{"session_id":"../../escaped"}`,
		`{"transcript_path":"/x/y.jsonl","session_id":"a/b"}`,
		`{}`,
	} {
		if err := Store(dir, []byte(raw)); err == nil {
			t.Errorf("Store accepted %s", raw)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Store left %d files behind", len(entries))
	}
}

// Store leaves no temporary files behind, and the rename means a reader
// polling once a second never catches a half-written file.
func TestStoreIsAtomic(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := Store(dir, []byte(fullPayload)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("after 5 stores the directory holds %v, want one file", names)
	}
}

func TestAccountTakesTheFreshestFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, raw string, age time.Duration) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("old.json", `{"rate_limits":{"seven_day":{"used_percentage":90}}}`, time.Hour)
	write("new.json", `{"rate_limits":{"seven_day":{"used_percentage":12},"five_hour":{"used_percentage":31}}}`, time.Minute)

	five, seven, ok := Account(dir)
	if !ok {
		t.Fatal("Account found nothing")
	}
	if pct, _ := seven.Pct(); pct != 12 {
		t.Errorf("seven day = %d, want 12 from the freshest file", pct)
	}
	if pct, _ := five.Pct(); pct != 31 {
		t.Errorf("five hour = %d, want 31", pct)
	}
}

func TestAccountPrunesAbandonedFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(stale, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-pruneAfter - time.Hour)
	if err := os.Chtimes(stale, at, at); err != nil {
		t.Fatal(err)
	}
	if err := Store(dir, []byte(fullPayload)); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := Account(dir); !ok {
		t.Fatal("Account found nothing")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a file untouched for over a month should have been pruned")
	}
	if _, ok := Read(dir, "aaaa1111-2222-3333-4444-555566667777"); !ok {
		t.Error("pruning removed a live session's file")
	}
}

// No shim installed anywhere is the normal case for a fresh machine.
func TestAccountWithNoShimInstalled(t *testing.T) {
	if _, _, ok := Account(t.TempDir()); ok {
		t.Error("Account reported limits with no files present")
	}
	if _, _, ok := Account(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("Account reported limits with no directory present")
	}
}
