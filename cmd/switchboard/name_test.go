package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

func writeTranscript(t *testing.T, cwd, session, content string) string {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, activity.Slug(cwd))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, session+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const testCwd = "/Users/example/repo"
const testSession = "aaaa1111-2222-3333-4444-555566667777"

func TestDisplayNameKeepsRealName(t *testing.T) {
	a := registry.Agent{Name: "Media: keep indexed PNG sub-sizes #81884"}
	act := activity.Activity{Title: "Something else entirely"}
	if got := displayName(t.TempDir(), a, act); got != a.Name {
		t.Errorf("displayName = %q, want the registered name", got)
	}
}

func TestDisplayNamePrefersAITitle(t *testing.T) {
	a := registry.Agent{Name: "gutenberg-42", NameSource: "derived"}
	act := activity.Activity{Title: "Review fixes 2 and 3"}
	if got := displayName(t.TempDir(), a, act); got != "Review fixes 2 and 3" {
		t.Errorf("displayName = %q", got)
	}
}

func TestDisplayNameFallsBackToFirstPrompt(t *testing.T) {
	content := `{"type":"user","message":{"role":"user","content":"/code-review medium https://github.com/WordPress/gutenberg/pull/81665"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n"
	dir := writeTranscript(t, testCwd, testSession, content)
	a := registry.Agent{Name: "gutenberg-42", NameSource: "derived", Cwd: testCwd, SessionID: testSession}
	got := displayName(dir, a, activity.Activity{})
	if got != "/code-review medium PR #81665" {
		t.Errorf("displayName = %q", got)
	}
}

func TestDisplayNameKeepsDerivedNameWhenNothingBetterExists(t *testing.T) {
	a := registry.Agent{Name: "gutenberg-42", NameSource: "derived", Cwd: testCwd, SessionID: testSession}
	if got := displayName(t.TempDir(), a, activity.Activity{}); got != "gutenberg-42" {
		t.Errorf("displayName = %q, want the derived name as a last resort", got)
	}
}

func TestDisplayNameTruncatesLongPrompts(t *testing.T) {
	long := strings.Repeat("fix the thing ", 20)
	content := `{"type":"user","message":{"role":"user","content":"` + long + `"}}` + "\n"
	dir := writeTranscript(t, testCwd, testSession, content)
	a := registry.Agent{Name: "repo-71", NameSource: "derived", Cwd: testCwd, SessionID: testSession}
	got := displayName(dir, a, activity.Activity{})
	if len([]rune(got)) > maxDerivedName {
		t.Errorf("displayName is %d runes, want at most %d", len([]rune(got)), maxDerivedName)
	}
}

func TestShortenLinks(t *testing.T) {
	cases := map[string]string{
		"work on https://github.com/WordPress/gutenberg/pull/81665":        "work on PR #81665",
		"see https://github.com/WordPress/gutenberg/issues/81846 for why":  "see issue #81846 for why",
		"https://github.com/WordPress/gutenberg/pull/81665#issuecomment-1": "PR #81665",
		"nothing to shorten here": "nothing to shorten here",
		"https://example.com/WordPress/gutenberg/pull/81665 stays as it is": "https://example.com/WordPress/gutenberg/pull/81665 stays as it is",
	}
	for in, want := range cases {
		if got := shortenLinks(in); got != want {
			t.Errorf("shortenLinks(%q) = %q, want %q", in, got, want)
		}
	}
}
