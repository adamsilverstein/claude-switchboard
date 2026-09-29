package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSubagent creates one subagent's transcript and meta file under the
// session's subagents directory, the layout Claude Code writes.
func writeSubagent(t *testing.T, projectsDir, id, meta, transcript string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, Slug(cwd), session, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "agent-"+id+".jsonl")
	if err := os.WriteFile(path, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const subagentStop = `{"type":"attachment","isSidechain":true,"attachment":{"type":"hook_success","hookEvent":"SubagentStop"}}`

func assistantSaid(text string) string {
	return `{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestSubagentsReadsMetaAndStatus(t *testing.T) {
	dir := t.TempDir()
	writeSubagent(t, dir, "a1",
		`{"agentType":"general-purpose","description":"Fix CI #1240"}`,
		strings.Join([]string{
			`{"type":"user","isSidechain":true,"message":{"role":"user","content":"go"}}`,
			assistantSaid("Pushing the Prettier fixes."),
			`{"type":"attachment","attachment":{"type":"hook_success","hookEvent":"PostToolUse"}}`,
		}, "\n")+"\n")
	writeSubagent(t, dir, "b2",
		`{"agentType":"Explore","description":"Find the focus code"}`,
		assistantSaid("Found it in iterm.go.")+"\n"+subagentStop+"\n")

	got := NewSubagents().For(dir, cwd, session, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d subagents, want 2: %+v", len(got), got)
	}
	byID := map[string]Subagent{}
	for _, s := range got {
		byID[s.ID] = s
	}
	a, b := byID["a1"], byID["b2"]
	if a.Type != "general-purpose" || a.Description != "Fix CI #1240" {
		t.Errorf("a1 meta = %q / %q", a.Type, a.Description)
	}
	if !a.Working {
		t.Error("a1 has not stopped, so it should be working")
	}
	if a.Summary != "Pushing the Prettier fixes." {
		t.Errorf("a1 summary = %q", a.Summary)
	}
	if b.Working {
		t.Error("b2 ended with SubagentStop, so it should not be working")
	}
	if b.Summary != "Found it in iterm.go." {
		t.Errorf("b2 summary = %q", b.Summary)
	}
}

// A subagent woken again after stopping - a SendMessage, a monitor event -
// writes a new user entry after its SubagentStop, and is working again.
func TestSubagentResumedAfterStopIsWorking(t *testing.T) {
	dir := t.TempDir()
	writeSubagent(t, dir, "a1", `{"agentType":"general-purpose","description":"x"}`,
		assistantSaid("done")+"\n"+subagentStop+"\n"+
			`{"type":"user","isSidechain":true,"message":{"role":"user","content":"CI finished"}}`+"\n")
	got := NewSubagents().For(dir, cwd, session, time.Now())
	if len(got) != 1 || !got[0].Working {
		t.Fatalf("resumed subagent should be working: %+v", got)
	}
}

// A subagent that never recorded a stop and has not written anything in a
// long time was cut off - its session was killed, or resumed without it -
// and must not claim to be working forever.
func TestLongSilentSubagentIsNotWorking(t *testing.T) {
	dir := t.TempDir()
	path := writeSubagent(t, dir, "a1", `{"agentType":"general-purpose","description":"x"}`,
		assistantSaid("still going")+"\n")
	old := time.Now().Add(-2 * StaleAfter)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	got := NewSubagents().For(dir, cwd, session, time.Now())
	if len(got) != 1 || got[0].Working {
		t.Fatalf("a silent subagent should not be working: %+v", got)
	}
}

func TestSubagentsWithoutMetaFallBackToTheID(t *testing.T) {
	dir := t.TempDir()
	writeSubagent(t, dir, "a1", "", assistantSaid("hi")+"\n")
	got := NewSubagents().For(dir, cwd, session, time.Now())
	if len(got) != 1 || got[0].Description != "" || got[0].ID != "a1" {
		t.Fatalf("got %+v", got)
	}
}

func TestNoSubagentsDirectoryIsEmpty(t *testing.T) {
	if got := NewSubagents().For(t.TempDir(), cwd, session, time.Now()); len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

// The cache must notice a transcript that changed since the last poll.
func TestSubagentsCacheRereadsAChangedTranscript(t *testing.T) {
	dir := t.TempDir()
	path := writeSubagent(t, dir, "a1", `{"agentType":"general-purpose","description":"x"}`,
		assistantSaid("working")+"\n")
	c := NewSubagents()
	if got := c.For(dir, cwd, session, time.Now()); !got[0].Working {
		t.Fatal("should start working")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(subagentStop + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := c.For(dir, cwd, session, time.Now()); got[0].Working {
		t.Fatal("should have noticed the stop")
	}
}

// Entries for transcripts that were deleted, and for sessions nobody has
// asked about in a while, must leave the cache: the app window runs for
// days, and every session that ever spawned a subagent would otherwise stay.
func TestSubagentsCacheForgetsWhatIsGone(t *testing.T) {
	dir := t.TempDir()
	path := writeSubagent(t, dir, "a1", `{"agentType":"general-purpose","description":"x"}`,
		assistantSaid("hi")+"\n")
	c := NewSubagents()
	start := time.Now()
	c.For(dir, cwd, session, start)
	if len(c.cache) != 1 {
		t.Fatalf("cache has %d entries, want 1", len(c.cache))
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	c.For(dir, cwd, session, start)
	if len(c.cache) != 0 {
		t.Fatalf("a deleted transcript should leave the cache, %d left", len(c.cache))
	}

	writeSubagent(t, dir, "b2", `{"agentType":"general-purpose","description":"y"}`,
		assistantSaid("hi")+"\n")
	c.For(dir, cwd, session, start)
	// Another session's poll, well past the idle window, sweeps this one.
	c.For(dir, "/Users/example/other", session, start.Add(2*forgetAfter))
	if len(c.cache) != 0 {
		t.Fatalf("an unasked-for session should leave the cache, %d left", len(c.cache))
	}
}
