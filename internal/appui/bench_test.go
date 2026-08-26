package appui

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func benchRows(n int) []ui.Row {
	rows := make([]ui.Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, ui.Row{
			Agent: registry.Agent{
				PID: 40000 + i, SessionID: fmt.Sprintf("session-%d", i),
				Status: []string{"idle", "busy", "shell"}[i%3],
				Cwd:    fmt.Sprintf("/Users/x/repositories/repo-%d", i%4),
				Live:   i%7 != 0, Entrypoint: "cli",
			},
			Name:    fmt.Sprintf("agent %d doing something with a fairly long title #%d", i, 80000+i),
			Summary: "A summary of roughly the length these actually run to in practice.",
			Age:     now.Add(-time.Duration(i) * time.Minute),
			Telemetry: ui.Telemetry{
				Model: "Opus 5", ContextWindow: 1_000_000, ContextTokens: 158_000 * (i%6 + 1),
				Repo: fmt.Sprintf("repo-%d", i%4), Branch: "trunk", Dirty: i%2 == 0,
				PermissionMode: "auto", Elapsed: time.Duration(i) * time.Minute,
				TTY: fmt.Sprintf("/dev/ttys%03d", i), Waiting: i%5 == 0,
			},
		})
	}
	return rows
}

// The budget is a full snapshot for 20 agents in under 50ms of Go work, at a
// 1s poll. This measures the filter, the sort and the formatting - the part
// that runs on every frame, including the ones a keystroke asks for.
//
// Measured on an M5 with the real thing behind it, a whole poll of 20 agents
// takes about 39ms: 29ms of that is the single `ps` call, which the terminal
// picker has always paid, 9ms is reading twenty transcript tails, and the
// rest - what is measured here - is under 100µs.
func TestSnapshotIsWellInsideTheBudget(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(benchRows(20), Account{}, nil, now)

	start := time.Now()
	const frames = 100
	for i := 0; i < frames; i++ {
		c.Snapshot(now)
	}
	per := time.Since(start) / frames
	if per > 50*time.Millisecond {
		t.Errorf("a 20-agent snapshot takes %v, over the 50ms budget", per)
	}
	t.Logf("20 agents: %v per snapshot", per)
}

func BenchmarkSnapshot20(b *testing.B) {
	c := New("")
	c.SetRows(benchRows(20), Account{}, nil, now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Snapshot(now)
	}
}

func BenchmarkSnapshot100(b *testing.B) {
	c := New("")
	c.SetRows(benchRows(100), Account{}, nil, now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Snapshot(now)
	}
}
