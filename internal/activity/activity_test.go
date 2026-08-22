package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	got := Slug("/Users/example/repositories/my.project")
	want := "-Users-example-repositories-my-project"
	if got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
}

// writeTranscript creates projectsDir/<slug>/<session>.jsonl with the given
// content and returns the projects dir.
func writeTranscript(t *testing.T, cwd, session, content string) string {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, Slug(cwd))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, session+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const cwd = "/Users/example/repo"
const session = "aaaa1111-2222-3333-4444-555566667777"

func TestForFindsLastAssistantText(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"older message"}]}}`,
		`{"type":"user","message":{"role":"user","content":"do the thing"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"wp-env is up on 8901.\nLet me run the new e2e test."}]}}`,
		`{"type":"system","content":"noise"}`,
	}, "\n") + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	want := "wp-env is up on 8901. Let me run the new e2e test."
	if a.Summary != want {
		t.Errorf("Summary = %q, want %q", a.Summary, want)
	}
	if a.Modified.IsZero() {
		t.Error("Modified should be set from the file mtime")
	}
}

func TestForSkipsAssistantEntriesWithoutText(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the real summary"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read"}]}}`,
	}, "\n") + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	if a.Summary != "the real summary" {
		t.Errorf("Summary = %q", a.Summary)
	}
}

func TestForDropsPartialLineAtChunkBoundary(t *testing.T) {
	// Build a transcript larger than tailBytes whose read window starts
	// mid-line. The partial first line must be discarded, not half-parsed.
	long := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", 2*tailBytes) + `"}]}}`
	content := long + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"after the boundary"}]}}` + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	if a.Summary != "after the boundary" {
		t.Errorf("Summary = %q, want %q", a.Summary, "after the boundary")
	}
}

func TestForToleratesTrailingPartialWrite(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done deploying"}]}}` + "\n" +
		`{"type":"assistant","message":{"role":"assist` // entry being written right now
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	if a.Summary != "done deploying" {
		t.Errorf("Summary = %q, want %q", a.Summary, "done deploying")
	}
}

func TestForStringContent(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":"plain string content"}}` + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	if a.Summary != "plain string content" {
		t.Errorf("Summary = %q", a.Summary)
	}
}

func TestForMissingTranscript(t *testing.T) {
	a := For(t.TempDir(), cwd, session)
	if a.Summary != "" || !a.Modified.IsZero() {
		t.Errorf("missing transcript should yield zero Activity, got %+v", a)
	}
}

func TestOneLineCapsLength(t *testing.T) {
	got := oneLine(strings.Repeat("a ", 1000))
	if len(got) > maxSummary+1 {
		t.Errorf("oneLine returned %d bytes", len(got))
	}
}

func TestForReadsAITitle(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"ai-title","aiTitle":"Stale earlier title","sessionId":"x"}`,
		`{"type":"user","message":{"role":"user","content":"carry on"}}`,
		`{"type":"ai-title","aiTitle":"Clean up worktrees","sessionId":"x"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"on it"}]}}`,
	}, "\n") + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	// The newest title wins: Claude Code rewrites it as the session moves on.
	if a.Title != "Clean up worktrees" {
		t.Errorf("Title = %q, want %q", a.Title, "Clean up worktrees")
	}
	if a.Summary != "on it" {
		t.Errorf("Summary = %q, want %q", a.Summary, "on it")
	}
}

func TestForWithoutAITitle(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}` + "\n"
	if a := For(writeTranscript(t, cwd, session, content), cwd, session); a.Title != "" {
		t.Errorf("Title = %q, want empty", a.Title)
	}
}

func TestFirstPromptSkipsScaffolding(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"<local-command-caveat>ignore me</local-command-caveat>"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"The output above is already visible to the user."}}`,
		`{"type":"user","message":{"role":"user","content":"review PR 81885 and fix the findings"}}`,
		`{"type":"user","message":{"role":"user","content":"a later prompt"}}`,
	}, "\n") + "\n"
	got := FirstPrompt(writeTranscript(t, cwd, session, content), cwd, session)
	if got != "review PR 81885 and fix the findings" {
		t.Errorf("FirstPrompt = %q", got)
	}
}

func TestFirstPromptDropsTrailingPartialLine(t *testing.T) {
	// A bounded read almost always cuts the last line in half, so the last
	// line is never trusted. Here the only user entry is that partial one.
	content := `{"type":"system","content":"boot"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"cut in ha`
	if got := FirstPrompt(writeTranscript(t, cwd, session, content), cwd, session); got != "" {
		t.Errorf("FirstPrompt = %q, want empty", got)
	}
}

func TestFirstPromptMissingTranscript(t *testing.T) {
	if got := FirstPrompt(t.TempDir(), cwd, session); got != "" {
		t.Errorf("FirstPrompt = %q, want empty", got)
	}
}
