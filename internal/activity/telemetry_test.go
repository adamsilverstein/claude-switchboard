package activity

import (
	"strings"
	"testing"
)

// A tail that carries every telemetry field should surface the most recent
// value of each, not the first one it happens to see.
func TestForReadsTelemetryFromTheTail(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"permission-mode","permissionMode":"plan"}`,
		`{"type":"assistant","gitBranch":"old-branch","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"first"}],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"permission-mode","permissionMode":"auto"}`,
		`{"type":"user","gitBranch":"trunk","message":{"role":"user","content":"go on"}}`,
		`{"type":"assistant","gitBranch":"trunk","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":3000,"cache_read_input_tokens":150000,"output_tokens":998}}}`,
	}, "\n") + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)

	if a.Model != "claude-opus-5" {
		t.Errorf("Model = %q, want %q", a.Model, "claude-opus-5")
	}
	if a.ContextTokens != 154000 {
		t.Errorf("ContextTokens = %d, want 154000", a.ContextTokens)
	}
	if a.PermissionMode != "auto" {
		t.Errorf("PermissionMode = %q, want %q", a.PermissionMode, "auto")
	}
	if a.GitBranch != "trunk" {
		t.Errorf("GitBranch = %q, want %q", a.GitBranch, "trunk")
	}
	if a.LastRole != "assistant" {
		t.Errorf("LastRole = %q, want %q", a.LastRole, "assistant")
	}
}

// LastRole is what the app window derives "waiting on you" from, so it has to
// ignore the bookkeeping entries Claude Code writes after a turn ends.
func TestLastRoleIgnoresBookkeepingEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name: "assistant finished and the harness wrote its own entries after",
			lines: []string{
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"all set"}]}}`,
				`{"type":"ai-title","aiTitle":"Fix the thing"}`,
				`{"type":"mode","mode":"normal"}`,
			},
			want: "assistant",
		},
		{
			name: "a tool result came back, so the agent is mid-turn",
			lines: []string{
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`,
				`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
			},
			want: "user",
		},
		{
			name: "injected instructions are not the user speaking",
			lines: []string{
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"all set"}]}}`,
				`{"type":"user","isMeta":true,"message":{"role":"user","content":"<system-reminder>"}}`,
			},
			want: "assistant",
		},
		{
			name:  "nothing has been said yet",
			lines: []string{`{"type":"mode","mode":"normal"}`},
			want:  "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Join(tc.lines, "\n") + "\n"
			a := For(writeTranscript(t, cwd, session, content), cwd, session)
			if a.LastRole != tc.want {
				t.Errorf("LastRole = %q, want %q", a.LastRole, tc.want)
			}
		})
	}
}

// A transcript with none of the telemetry must still produce a summary. The
// whole contract is that a sparser file degrades to a sparser listing.
func TestTelemetryIsOptional(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}` + "\n"
	a := For(writeTranscript(t, cwd, session, content), cwd, session)
	if a.Summary != "hello" {
		t.Errorf("Summary = %q, want %q", a.Summary, "hello")
	}
	if a.Model != "" || a.ContextTokens != 0 || a.PermissionMode != "" || a.GitBranch != "" {
		t.Errorf("expected zero telemetry, got %+v", a)
	}
}

func TestModelDisplayName(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5":              "Opus 5",
		"claude-opus-4-7":            "Opus 4.7",
		"claude-sonnet-4-6":          "Sonnet 4.6",
		"claude-fable-5":             "Fable 5",
		"claude-haiku-4-5-20251001":  "Haiku 4.5",
		"claude-3-5-sonnet-20241022": "claude-3-5-sonnet-20241022", // version before family
		"some-other-model":           "some-other-model",
		"":                           "",
	} {
		if got := ModelDisplayName(id); got != want {
			t.Errorf("ModelDisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}
