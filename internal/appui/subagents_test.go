package appui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func subagentRow(subs ...ui.Subagent) ui.Row {
	return ui.Row{
		Agent:     registry.Agent{PID: 7, SessionID: "s7", Status: "idle", Live: true},
		Name:      "fan-out",
		Telemetry: ui.Telemetry{Subagents: subs},
	}
}

func TestSubagentsListWorkingFirstThenMostRecentlyFinished(t *testing.T) {
	v := view(now, subagentRow(
		ui.Subagent{Description: "old done", Modified: now.Add(-time.Hour)},
		ui.Subagent{Description: "late start", Working: true, Started: now.Add(-2 * time.Minute)},
		ui.Subagent{Description: "recent done", Modified: now.Add(-5 * time.Minute)},
		ui.Subagent{Description: "early start", Working: true, Started: now.Add(-9 * time.Minute)},
	))
	var got []string
	for _, s := range v.Subagents {
		got = append(got, s.Description+" "+s.Age)
	}
	want := "early start 9m|late start 2m|recent done 5m|old done 1h 00m"
	if strings.Join(got, "|") != want {
		t.Errorf("order = %q, want %q", strings.Join(got, "|"), want)
	}
	if v.SubagentsWorking != 2 || v.SubagentsTotal != 4 {
		t.Errorf("counts = %d working / %d total, want 2 / 4", v.SubagentsWorking, v.SubagentsTotal)
	}
}

func TestSubagentsAreCappedButCountedInFull(t *testing.T) {
	var subs []ui.Subagent
	for i := 0; i < maxSubagents+10; i++ {
		subs = append(subs, ui.Subagent{Description: fmt.Sprint(i), Working: i == maxSubagents+5})
	}
	v := view(now, subagentRow(subs...))
	if len(v.Subagents) != maxSubagents {
		t.Errorf("sent %d, want the cap of %d", len(v.Subagents), maxSubagents)
	}
	if !v.Subagents[0].Working {
		t.Error("a working subagent past the cap must still be sent, first")
	}
	if v.SubagentsTotal != maxSubagents+10 || v.SubagentsWorking != 1 {
		t.Errorf("counts = %d / %d", v.SubagentsWorking, v.SubagentsTotal)
	}
}

func TestSubagentWithoutADescriptionShowsItsType(t *testing.T) {
	v := view(now, subagentRow(ui.Subagent{Type: "Explore"}))
	if v.Subagents[0].Description != "Explore" {
		t.Errorf("description = %q", v.Subagents[0].Description)
	}
}

func TestNoSubagentsIsOmittedFromTheJSON(t *testing.T) {
	raw, _ := json.Marshal(view(now, subagentRow()))
	if strings.Contains(string(raw), `"subagents":`) {
		t.Errorf("subagents should be omitted: %s", raw)
	}
}
