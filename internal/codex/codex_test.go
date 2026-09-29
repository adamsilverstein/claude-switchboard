package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Captured from `ps -axo pid=,tty=,lstart=,args=` with one Codex session
// open in iTerm, trimmed to the rows that matter.
const psOutput = `  501 ??       Thu Sep 24 22:00:24 2026     /Users/u/.codex/packages/app-server-daemon/releases/0.158.0/bin/codex app-server daemon pid-update-loop
24464 ??       Fri Sep 25 22:14:12 2026     /Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex -c features.code_mode_host=true app-server
19966 ttys001  Mon Sep 28 14:41:26 2026     node /Users/u/.nvm/versions/node/v20.20.2/bin/codex
19980 ttys001  Mon Sep 28 14:41:26 2026     /Users/u/.nvm/versions/node/v20.20.2/lib/node_modules/@openai/codex/vendor/aarch64-apple-darwin/bin/codex
20001 ttys004  Mon Sep 28 15:02:09 2026     /opt/homebrew/bin/codex resume --last
20002 ttys005  Mon Sep 28 15:03:00 2026     codex app-server
20003 ttys006  Mon Sep 28 15:03:00 2026     /usr/bin/vim codex
`

func TestParseProcsKeepsOnlyInteractiveCodex(t *testing.T) {
	procs := parseProcs(psOutput)
	var pids []int
	for _, p := range procs {
		pids = append(pids, p.PID)
	}
	if len(procs) != 2 || procs[0].PID != 19980 || procs[1].PID != 20001 {
		t.Fatalf("pids = %v, want [19980 20001]", pids)
	}
	if procs[0].TTY != "/dev/ttys001" {
		t.Errorf("TTY = %q", procs[0].TTY)
	}
	want := time.Date(2026, time.September, 28, 14, 41, 26, 0, time.Local)
	if !procs[0].Start.Equal(want) {
		t.Errorf("Start = %v, want %v", procs[0].Start, want)
	}
}

func TestParseCwds(t *testing.T) {
	got := parseCwds("p19980\nfcwd\nn/Users/u/repositories/gutenberg\np20001\nfcwd\nn/tmp/x y\n")
	if got[19980] != "/Users/u/repositories/gutenberg" || got[20001] != "/tmp/x y" {
		t.Errorf("parseCwds = %v", got)
	}
}

func TestModelDisplayName(t *testing.T) {
	for id, want := range map[string]string{
		"gpt-6-astra":       "GPT-6-Astra",
		"gpt-5.6-sol":       "GPT-5.6-Sol",
		"codex-auto-review": "Codex-Auto-Review",
		"":                  "",
	} {
		if got := ModelDisplayName(id); got != want {
			t.Errorf("ModelDisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}

// line builds one rollout entry.
func line(ts, typ, payload string) string {
	return `{"timestamp":"` + ts + `","type":"` + typ + `","payload":` + payload + "}\n"
}

func meta(id, cwd, ts, source, originator string) string {
	return line(ts, "session_meta", `{"id":"`+id+`","cwd":"`+cwd+`","timestamp":"`+ts+
		`","originator":"`+originator+`","source":`+source+`,"base_instructions":{"text":"`+strings.Repeat("x", 70000)+`"}}`)
}

const (
	started   = `{"type":"task_started","turn_id":"t1"}`
	completed = `{"type":"task_complete","turn_id":"t1"}`
	aborted   = `{"type":"turn_aborted","turn_id":"t1"}`
	context   = `{"model":"gpt-6-astra","cwd":"/r"}`
	tokens    = `{"type":"token_count","info":{"last_token_usage":{"total_tokens":29900},"model_context_window":258400}}`
	userMsg   = `{"type":"message","role":"user","content":[{"type":"input_text","text":"continue the reviews"}]}`
	agentMsg  = `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The structural failures\n can change what gets published."}]}`
)

func TestScanTailBusyMidTurn(t *testing.T) {
	buf := line("2026-09-29T05:26:00Z", "turn_context", context) +
		line("2026-09-29T05:26:01Z", "response_item", userMsg) +
		line("2026-09-29T05:26:02Z", "event_msg", started) +
		line("2026-09-29T05:26:03Z", "response_item", agentMsg) +
		line("2026-09-29T05:26:04Z", "event_msg", tokens)
	tail := scanTail([]byte(buf), false)
	if !tail.Busy {
		t.Error("an open turn should be busy")
	}
	if tail.Summary != "The structural failures can change what gets published." {
		t.Errorf("Summary = %q", tail.Summary)
	}
	if tail.Model != "gpt-6-astra" || tail.ContextTokens != 29900 || tail.ContextWindow != 258400 {
		t.Errorf("Model = %q, ContextTokens = %d, ContextWindow = %d", tail.Model, tail.ContextTokens, tail.ContextWindow)
	}
	if tail.LastRole != "assistant" {
		t.Errorf("LastRole = %q", tail.LastRole)
	}
	if want := time.Date(2026, time.September, 29, 5, 26, 4, 0, time.UTC); !tail.Updated.Equal(want) {
		t.Errorf("Updated = %v", tail.Updated)
	}
}

func TestScanTailIdleAfterCompleteOrAbort(t *testing.T) {
	for _, end := range []string{completed, aborted} {
		buf := line("2026-09-29T05:26:02Z", "event_msg", started) +
			line("2026-09-29T05:26:03Z", "response_item", agentMsg) +
			line("2026-09-29T05:26:04Z", "event_msg", end)
		if scanTail([]byte(buf), false).Busy {
			t.Errorf("a turn ended by %s should be idle", end)
		}
	}
	// A truncated head and a half-written final line are both skipped.
	buf := `ial":1}` + "\n" + line("2026-09-29T05:26:02Z", "event_msg", started) + `{"timestamp":"2026-09-29T05:2`
	if !scanTail([]byte(buf), true).Busy {
		t.Error("partial lines should not hide the open turn")
	}
}

func TestReadMetaSkipsSubagentsAndDesktop(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, source, originator string
		interactive              bool
	}{
		{"cli", `"cli"`, "codex-tui", true},
		{"tui-vscode-source", `"vscode"`, "codex-tui", true},
		{"guardian", `{"subagent":{"other":"guardian"}}`, "codex-tui", false},
		{"desktop", `"vscode"`, "Codex Desktop", false},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".jsonl")
		write(t, path, meta("id-"+c.name, "/r", "2026-09-28T21:41:31.163Z", c.source, c.originator))
		r, ok := readMeta(path)
		if !ok {
			t.Fatalf("%s: readMeta failed", c.name)
		}
		if r.Interactive != c.interactive || r.Cwd != "/r" || r.ID != "id-"+c.name {
			t.Errorf("%s: got %+v", c.name, r)
		}
	}
	write(t, filepath.Join(dir, "junk.jsonl"), "not json\n")
	if _, ok := readMeta(filepath.Join(dir, "junk.jsonl")); ok {
		t.Error("junk should not parse")
	}
}

func TestMatch(t *testing.T) {
	base := time.Date(2026, time.September, 28, 14, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	ro := func(path, cwd string, created, modified int) Rollout {
		return Rollout{Path: path, Cwd: cwd, Created: at(created), Modified: at(modified), Interactive: true}
	}

	t.Run("two processes in one directory each keep their own", func(t *testing.T) {
		procs := []Proc{{PID: 1, Cwd: "/g", Start: at(0)}, {PID: 2, Cwd: "/g", Start: at(10)}}
		// The older process's rollout was written most recently.
		got := match(procs, []Rollout{ro("a", "/g", 1, 30), ro("b", "/g", 11, 20)})
		if got[1].Path != "a" || got[2].Path != "b" {
			t.Errorf("got 1=%q 2=%q, want a, b", got[1].Path, got[2].Path)
		}
	})

	t.Run("after /new the latest session wins", func(t *testing.T) {
		procs := []Proc{{PID: 1, Cwd: "/g", Start: at(0)}}
		got := match(procs, []Rollout{ro("first", "/g", 1, 5), ro("second", "/g", 6, 9)})
		if got[1].Path != "second" {
			t.Errorf("got %q, want second", got[1].Path)
		}
	})

	t.Run("a resumed session falls back to the older rollout", func(t *testing.T) {
		procs := []Proc{{PID: 1, Cwd: "/g", Start: at(60)}}
		got := match(procs, []Rollout{ro("old", "/g", 0, 70), ro("stale", "/g", 0, 30)})
		if got[1].Path != "old" {
			t.Errorf("got %q, want old", got[1].Path)
		}
	})

	t.Run("other directories, subagents and fresh processes stay unmatched", func(t *testing.T) {
		sub := ro("sub", "/g", 1, 2)
		sub.Interactive = false
		procs := []Proc{{PID: 1, Cwd: "/g", Start: at(0)}, {PID: 2, Cwd: "/h", Start: at(0)}}
		got := match(procs, []Rollout{sub, ro("elsewhere", "/x", 1, 2)})
		if len(got) != 0 {
			t.Errorf("got %v, want no matches", got)
		}
	})
}

func TestScan(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "09", "28")
	start := time.Date(2026, time.September, 28, 14, 41, 26, 0, time.UTC)

	path := filepath.Join(day, "rollout-a.jsonl")
	write(t, path, meta("thread-a", "/r/gutenberg", "2026-09-28T14:41:31Z", `"cli"`, "codex-tui")+
		line("2026-09-28T14:43:26Z", "event_msg", started)+
		line("2026-09-28T14:43:27Z", "turn_context", context)+
		line("2026-09-28T14:43:28Z", "response_item", agentMsg))
	write(t, filepath.Join(home, "session_index.jsonl"),
		`{"id":"thread-a","thread_name":"Old name"}`+"\n"+
			`{"id":"thread-a","thread_name":"Review stacked PRs"}`+"\n")
	old := filepath.Join(day, "rollout-old.jsonl")
	write(t, old, meta("thread-old", "/r/gutenberg", "2026-09-27T10:00:00Z", `"cli"`, "codex-tui"))
	if err := os.Chtimes(old, start.Add(-time.Hour), start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	lsofCalls := 0
	s := newScanner(home,
		func() []Proc {
			return []Proc{
				{PID: 19980, TTY: "/dev/ttys001", Start: start},
				{PID: 30000, TTY: "/dev/ttys009", Start: start},
			}
		},
		func(pids []int) map[int]string {
			lsofCalls++
			return map[int]string{19980: "/r/gutenberg", 30000: "/r/fresh"}
		})

	agents, ttys := s.Scan()
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(agents))
	}
	a := agents[0]
	if a.PID != 19980 || a.SessionID != "thread-a" || a.Transcript != path {
		t.Errorf("matched agent = %+v", a)
	}
	if a.Name != "Review stacked PRs" || a.NameIsDerived() {
		t.Errorf("Name = %q (derived %v)", a.Name, a.NameIsDerived())
	}
	if a.Status != "busy" || !a.Live || !a.Codex() || !a.Focusable() {
		t.Errorf("Status = %q, Live = %v, Codex = %v", a.Status, a.Live, a.Codex())
	}
	if ttys[19980] != "/dev/ttys001" {
		t.Errorf("tty = %q", ttys[19980])
	}

	fresh := agents[1]
	if fresh.SessionID != "" || fresh.Name != "fresh" || !fresh.NameIsDerived() || fresh.Status != "idle" {
		t.Errorf("unmatched agent = %+v", fresh)
	}

	s.Scan()
	if lsofCalls != 1 {
		t.Errorf("lsof ran %d times; cached cwds should make it once", lsofCalls)
	}
}

func TestScanWithoutCodexIsEmpty(t *testing.T) {
	s := newScanner(t.TempDir(), func() []Proc { return nil }, func([]int) map[int]string {
		t.Fatal("lsof should not run when no codex is running")
		return nil
	})
	if agents, _ := s.Scan(); len(agents) != 0 {
		t.Errorf("got %d agents", len(agents))
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
