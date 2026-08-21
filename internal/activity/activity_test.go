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
