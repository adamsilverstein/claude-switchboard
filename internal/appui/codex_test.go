package appui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// A Codex session's row is read from its rollout, not from Claude Code's
// transcript directory, and carries the window size the rollout records.
func TestRowsReadCodexRollout(t *testing.T) {
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := `{"timestamp":"2026-09-29T05:26:00Z","type":"turn_context","payload":{"model":"gpt-6-astra"}}
{"timestamp":"2026-09-29T05:26:01Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-09-29T05:26:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"All 649 tests pass."}]}}
{"timestamp":"2026-09-29T05:26:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":29900},"model_context_window":258400}}}
{"timestamp":"2026-09-29T05:26:04Z","type":"event_msg","payload":{"type":"task_complete"}}
`
	if err := os.WriteFile(rollout, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	b := Builder{ProjectsDir: t.TempDir(), StatuslineDir: t.TempDir()}
	a := registry.Agent{
		PID: 7, SessionID: "thread", Cwd: "/r", Live: true, Status: "idle",
		Entrypoint: registry.EntrypointCodex, Transcript: rollout, Name: "Review stacked PRs",
	}
	rows := b.Rows([]registry.Agent{a}, nil, func(a registry.Agent, _ activity.Activity) string { return a.Name })
	tel := rows[0].Telemetry
	if rows[0].Summary != "All 649 tests pass." {
		t.Errorf("Summary = %q", rows[0].Summary)
	}
	if tel.Model != "GPT-6-Astra" || tel.ContextTokens != 29900 || tel.ContextWindow != 258400 {
		t.Errorf("Model = %q, ContextTokens = %d, ContextWindow = %d", tel.Model, tel.ContextTokens, tel.ContextWindow)
	}
	if !tel.Waiting {
		t.Error("an idle Codex session whose last word was its own is waiting on you")
	}
	v := view(time.Now(), rows[0])
	if v.Tool != "codex" || v.ContextWindow != "258k" || v.ContextPct == nil {
		t.Errorf("Tool = %q, ContextWindow = %q, ContextPct = %v", v.Tool, v.ContextWindow, v.ContextPct)
	}
}
