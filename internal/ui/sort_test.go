package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// names joins the row names so an ordering assertion reads as one string.
func names(rows []Row) string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return strings.Join(out, ",")
}

func telemetryRows() []Row {
	return []Row{
		{
			Agent: registry.Agent{PID: 1, Status: "idle", Cwd: "/repo/gutenberg", Live: true},
			Name:  "alpha", Summary: "drafting", Age: base.Add(-time.Hour),
			Telemetry: Telemetry{Model: "Opus 5", Repo: "gutenberg", ContextWindow: 1000000, ContextTokens: 620000},
		},
		{
			Agent: registry.Agent{PID: 2, Status: "busy", Cwd: "/repo/core", Live: true},
			Name:  "beta", Summary: "running tests", Age: base.Add(-time.Minute),
			Telemetry: Telemetry{Model: "Sonnet 4.6", Repo: "core", ContextWindow: 200000, ContextTokens: 176000},
		},
		{
			Agent: registry.Agent{PID: 3, Status: "idle", Cwd: "/repo/docs", Live: true},
			Name:  "gamma", Summary: "waiting on you", Age: base.Add(-2 * time.Minute),
			Telemetry: Telemetry{Model: "Opus 5", Repo: "docs", Waiting: true},
		},
	}
}

func TestFilterMatchesRepoAndModel(t *testing.T) {
	rows := telemetryRows()
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"gutenberg", "alpha"},      // repo, and also the cwd
		{"sonnet", "beta"},          // model, which no other field carries
		{"opus", "alpha,gamma"},     // model across two rows
		{"waiting on you", "gamma"}, // summary, as before
		{"", "alpha,beta,gamma"},
		{"nothing", ""},
	} {
		if got := names(Filter(rows, tc.query)); got != tc.want {
			t.Errorf("Filter(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

// Filter must not disturb the caller's slice: the app window keeps one
// unfiltered snapshot and filters it repeatedly as you type.
func TestFilterLeavesInputAlone(t *testing.T) {
	rows := telemetryRows()
	got := Filter(rows, "beta")
	got[0].Name = "mutated"
	if rows[1].Name != "beta" {
		t.Fatalf("Filter aliased its input: row is now %q", rows[1].Name)
	}
	if len(rows) != 3 {
		t.Fatalf("Filter changed the input length to %d", len(rows))
	}
}

func TestSortByContextFullestFirst(t *testing.T) {
	rows := telemetryRows()
	SortRows(rows, SortContext)
	// beta is at 88%, alpha at 62%, and gamma has no reading at all so it
	// sorts last rather than sorting as 0%.
	if got := names(rows); got != "beta,alpha,gamma" {
		t.Errorf("context order = %q, want %q", got, "beta,alpha,gamma")
	}
}

func TestSortByRepoPutsUnknownLast(t *testing.T) {
	rows := append(telemetryRows(), Row{
		Agent: registry.Agent{PID: 4, Status: "idle", Live: true},
		Name:  "delta", Age: base,
	})
	SortRows(rows, SortRepo)
	// core, docs, gutenberg alphabetically; delta has no repo at all.
	if got := names(rows); got != "beta,gamma,alpha,delta" {
		t.Errorf("repo order = %q, want %q", got, "beta,gamma,alpha,delta")
	}
}

// Derived waiting has to reach the sort, because the whole point of it is to
// put the agent that stopped and handed the turn back at the top.
func TestSortByStatusHonoursDerivedWaiting(t *testing.T) {
	rows := telemetryRows()
	SortRows(rows, SortStatus)
	if got := names(rows); got != "gamma,alpha,beta" {
		t.Errorf("status order = %q, want %q", got, "gamma,alpha,beta")
	}
}

// The terminal picker builds rows with no telemetry at all. Its ordering
// must be exactly what it was before telemetry existed.
func TestSortByStatusUnchangedWithoutTelemetry(t *testing.T) {
	rows := testRows()
	SortRows(rows, SortStatus)
	if got := names(rows); got != "alpha gutenberg fix,gamma docs,beta core patch,delta finished" {
		t.Errorf("status order = %q", got)
	}
}

func TestContextPctReportsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tel     Telemetry
		wantPct int
		wantOK  bool
	}{
		{"both known", Telemetry{ContextWindow: 1000000, ContextTokens: 158000}, 15, true},
		{"no window", Telemetry{ContextTokens: 158000}, 0, false},
		{"no tokens", Telemetry{ContextWindow: 1000000}, 0, false},
		{"zero value", Telemetry{}, 0, false},
		{"over full", Telemetry{ContextWindow: 100, ContextTokens: 250}, 100, true},
	} {
		pct, ok := tc.tel.ContextPct()
		if pct != tc.wantPct || ok != tc.wantOK {
			t.Errorf("%s: ContextPct() = %d, %v; want %d, %v", tc.name, pct, ok, tc.wantPct, tc.wantOK)
		}
	}
}

func TestParseSortKeyFallsBackToStatus(t *testing.T) {
	for name, want := range map[string]SortKey{
		"status":  SortStatus,
		"age":     SortAge,
		"name":    SortName,
		"dir":     SortDir,
		"context": SortContext,
		"repo":    SortRepo,
		"":        SortStatus,
		"tokens":  SortStatus,
	} {
		if got := ParseSortKey(name); got != want {
			t.Errorf("ParseSortKey(%q) = %v, want %v", name, got, want)
		}
	}
}
