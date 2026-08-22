package main

import (
	"strings"
	"testing"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

func findFixtures() []registry.Agent {
	return []registry.Agent{
		{PID: 100, SessionID: "aaaa-1111", Name: "alpha fix", Live: true},
		{PID: 200, SessionID: "bbbb-2222", Name: "beta patch", Live: true},
		{PID: 300, SessionID: "cccc-3333", Name: "gone agent", Live: false},
	}
}

func TestFindAgentByPIDAndSubstring(t *testing.T) {
	a, err := findAgent(findFixtures(), "200")
	if err != nil || a.PID != 200 {
		t.Fatalf("by PID: %v, %v", a.PID, err)
	}
	a, err = findAgent(findFixtures(), "alpha")
	if err != nil || a.PID != 100 {
		t.Fatalf("by substring: %v, %v", a.PID, err)
	}
	a, err = findAgent(findFixtures(), "bbbb")
	if err != nil || a.PID != 200 {
		t.Fatalf("by session prefix: %v, %v", a.PID, err)
	}
}

// A dead agent's PID may already belong to an unrelated process, so exact
// matches on dead entries must error rather than resolve to a window.
func TestFindAgentRefusesDeadMatches(t *testing.T) {
	for _, query := range []string{"300", "gone agent", "cccc-3333", "gone"} {
		_, err := findAgent(findFixtures(), query)
		if err == nil {
			t.Errorf("query %q resolved a dead agent", query)
			continue
		}
		if query == "gone" {
			// Fuzzy queries skip dead agents entirely.
			if !strings.Contains(err.Error(), "no agent matches") {
				t.Errorf("query %q: err = %v", query, err)
			}
		} else if !strings.Contains(err.Error(), "no longer running") {
			t.Errorf("query %q: err = %v, want a no-longer-running error", query, err)
		}
	}
}

func TestFindAgentAmbiguous(t *testing.T) {
	_, err := findAgent(findFixtures(), "a")
	if err == nil || !strings.Contains(err.Error(), "matches 2 agents") {
		t.Fatalf("err = %v", err)
	}
}
