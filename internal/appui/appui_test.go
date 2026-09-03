package appui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/forge"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

var now = time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)

func rows() []ui.Row {
	return []ui.Row{
		{
			Agent: registry.Agent{PID: 1, SessionID: "s1", Status: "idle", Cwd: "/repo/gutenberg", Live: true},
			Name:  "editor intent", Summary: "which placement?", Age: now.Add(-4*time.Hour - 42*time.Minute),
			Telemetry: ui.Telemetry{
				Model: "Opus 5", ContextWindow: 1_000_000, ContextTokens: 158_000,
				Repo: "gutenberg", Branch: "trunk", Dirty: true, Waiting: true,
				PermissionMode: "auto", Elapsed: 4*time.Hour + 42*time.Minute, TTY: "/dev/ttys014",
				Ref: forge.Ref{
					Number: 13, Kind: "pr", State: "open", Title: "Console redesign",
					URL: "https://github.com/adamsilverstein/claude-switchboard/pull/13", Known: true,
				},
			},
		},
		{
			Agent: registry.Agent{PID: 2, SessionID: "s2", Status: "busy", Cwd: "/repo/core", Live: true},
			Name:  "README", Summary: "restructuring", Age: now.Add(-6 * time.Minute),
		},
		{
			Agent: registry.Agent{PID: 3, SessionID: "s3", Status: "idle", Cwd: "/repo/old", Live: false},
			Name:  "finished", Summary: "merged", Age: now.Add(-6 * time.Hour),
		},
	}
}

func snap(t *testing.T, c *Controller) Snapshot {
	t.Helper()
	c.SetRows(rows(), Account{}, nil, now)
	return c.Snapshot(now)
}

// activityWithLastRole builds the one input the waiting derivation reads.
func activityWithLastRole(role string) activity.Activity {
	return activity.Activity{LastRole: role}
}

func TestSnapshotFormatsARow(t *testing.T) {
	s := snap(t, New(filepath.Join(t.TempDir(), "app.json")))
	var v AgentView
	for _, a := range s.Agents {
		if a.PID == 1 {
			v = a
		}
	}
	if v.Status != "waiting" {
		t.Errorf("status = %q, want waiting (derived)", v.Status)
	}
	if v.Age != "4h42m" {
		t.Errorf("age = %q, want 4h42m", v.Age)
	}
	if v.Elapsed != "4h 42m" {
		t.Errorf("elapsed = %q, want %q", v.Elapsed, "4h 42m")
	}
	if v.ContextPct == nil || *v.ContextPct != 15 {
		t.Errorf("contextPct = %v, want 15", v.ContextPct)
	}
	if v.ContextLabel != "158k / 1M" {
		t.Errorf("contextLabel = %q, want %q", v.ContextLabel, "158k / 1M")
	}
	if v.TTY != "ttys014" {
		t.Errorf("tty = %q, want ttys014", v.TTY)
	}
	if !v.Focusable {
		t.Error("a live cli agent with a tty should be focusable")
	}
	if v.Ref != "#13" || v.RefKind != "pr" || v.RefState != "open" {
		t.Errorf("ref = %q %q %q, want #13 pr open", v.Ref, v.RefKind, v.RefState)
	}
	if v.RefURL != "https://github.com/adamsilverstein/claude-switchboard/pull/13" {
		t.Errorf("refUrl = %q", v.RefURL)
	}
}

// The page may only ask for a pull request or issue that gh resolved. It is
// the one command that carries a string straight to a subprocess, so the
// shape it accepts is exactly the shape gh produces and nothing else.
func TestOpenOnlyAcceptsAGitHubURL(t *testing.T) {
	c := loaded(t)
	ok := c.Handle(`{"cmd":"open","url":"https://github.com/a/b/pull/13"}`)
	if ok.Kind != "open" || ok.URL != "https://github.com/a/b/pull/13" {
		t.Errorf("open = %+v, want the URL passed through", ok)
	}
	for _, raw := range []string{
		`{"cmd":"open"}`,
		`{"cmd":"open","url":"http://github.com/a/b/pull/13"}`,
		`{"cmd":"open","url":"https://githubXcom/a/b"}`,
		`{"cmd":"open","url":"file:///etc/passwd"}`,
		`{"cmd":"open","url":"-a/Calculator"}`,
		`{"cmd":"open","url":"https://github.com/a/b pull/13"}`,
	} {
		if act := c.Handle(raw); act.Kind != "" {
			t.Errorf("%s = %+v, want it refused", raw, act)
		}
	}
}

// The contract the whole design rests on: a field with no source is absent,
// never a zero value dressed up as a reading.
func TestMissingTelemetryIsNullNotZero(t *testing.T) {
	s := snap(t, New(filepath.Join(t.TempDir(), "app.json")))
	raw, err := json.Marshal(s.Agents[len(s.Agents)-1])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["contextPct"]; !ok || v != nil {
		t.Errorf("contextPct = %v, want an explicit null", v)
	}
	for _, key := range []string{
		"model", "repo", "branch", "elapsed", "contextLabel", "permissionMode",
		"ref", "refKind", "refState", "refTitle", "refUrl",
	} {
		if v, ok := got[key]; ok {
			t.Errorf("%s = %v, want the key omitted entirely", key, v)
		}
	}
}

func TestDeadAgentsReadAsDeadAndAreNotFocusable(t *testing.T) {
	s := snap(t, New(filepath.Join(t.TempDir(), "app.json")))
	last := s.Agents[len(s.Agents)-1]
	if last.Status != "dead" {
		t.Errorf("status = %q, want dead", last.Status)
	}
	if last.Focusable {
		t.Error("a dead agent must not be focusable")
	}
	if last.Live {
		t.Error("Live should be false")
	}
}

// Waiting must only be derived for live idle agents; a dead agent that
// happened to stop talking is not waiting on anyone.
func TestWaitingIsNotDerivedForDeadAgents(t *testing.T) {
	b := Builder{}
	tel := b.telemetry(
		registry.Agent{Status: "idle", Live: false},
		activityWithLastRole("assistant"), "")
	if tel.Waiting {
		t.Error("a dead agent should never read as waiting")
	}
	tel = b.telemetry(
		registry.Agent{Status: "busy", Live: true},
		activityWithLastRole("assistant"), "")
	if tel.Waiting {
		t.Error("a busy agent should never read as waiting")
	}
	tel = b.telemetry(
		registry.Agent{Status: "idle", Live: true},
		activityWithLastRole("user"), "")
	if tel.Waiting {
		t.Error("an agent mid-turn should not read as waiting")
	}
	tel = b.telemetry(
		registry.Agent{Status: "idle", Live: true},
		activityWithLastRole("assistant"), "")
	if !tel.Waiting {
		t.Error("an idle agent that spoke last is waiting on you")
	}
}

func TestFilterAndSortComeFromTheSharedFunctions(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)

	c.SetSort("context")
	s := c.Snapshot(now)
	if s.Sort != "context" || s.Agents[0].PID != 1 {
		t.Errorf("sort = %q, first pid = %d; want context, 1", s.Sort, s.Agents[0].PID)
	}

	c.SetFilter("gutenberg")
	s = c.Snapshot(now)
	if len(s.Agents) != 1 || s.Agents[0].PID != 1 {
		t.Errorf("filtered to %d agents, want just pid 1", len(s.Agents))
	}
	// The count of everything must survive the filter, so the page can
	// say "1 of 3" rather than implying two agents stopped existing.
	if s.Total != 3 {
		t.Errorf("Total = %d, want 3", s.Total)
	}
}

func TestGenerationIncrementsPerFrame(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	if a, b := c.Snapshot(now).Generation, c.Snapshot(now).Generation; b != a+1 {
		t.Errorf("generation went %d -> %d, want consecutive", a, b)
	}
}

// A transient scan failure should report itself without blanking the list.
func TestScanErrorKeepsTheLastGoodRows(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	c.SetRows(nil, Account{}, os.ErrPermission, now)

	s := c.Snapshot(now)
	if s.Error == "" {
		t.Error("want the error reported")
	}
	if len(s.Agents) != 3 {
		t.Errorf("got %d agents, want the last good list of 3", len(s.Agents))
	}
}

func TestDensityDefaultsToTheRowCountAndYieldsToAChoice(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	if c.Snapshot(now).Compact {
		t.Error("three rows should not be compact")
	}

	many := make([]ui.Row, 0, 12)
	for i := 0; i < 12; i++ {
		many = append(many, ui.Row{Agent: registry.Agent{PID: i + 10, Live: true}, Name: "a"})
	}
	c.SetRows(many, Account{}, nil, now)
	if !c.Snapshot(now).Compact {
		t.Error("twelve rows should be compact")
	}

	c.SetDensity("comfy")
	if c.Snapshot(now).Compact {
		t.Error("an explicit comfy choice must beat the row count")
	}
	c.SetDensity("")
	if !c.Snapshot(now).Compact {
		t.Error("clearing the choice should hand density back to the row count")
	}
}

func TestPrefsPersistAcrossLaunchesExceptTheFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	c := New(path)
	c.SetSort("context")
	c.SetGrouped(false)
	c.SetDensity("compact")
	c.SetFilter("gutenberg")

	next := New(path)
	next.SetRows(rows(), Account{}, nil, now)
	s := next.Snapshot(now)
	if s.Sort != "context" {
		t.Errorf("sort = %q, want context", s.Sort)
	}
	if s.Grouped {
		t.Error("grouped should have persisted as false")
	}
	if !s.Compact {
		t.Error("compact should have persisted")
	}
	if s.Filter != "" {
		t.Errorf("filter = %q; reopening to a hidden list is a bad surprise", s.Filter)
	}
}

func TestNewWithAnUnreadablePrefsFileUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(path)
	c.SetRows(rows(), Account{}, nil, now)
	if s := c.Snapshot(now); s.Sort != "status" || !s.Grouped {
		t.Errorf("sort = %q, grouped = %v; want the defaults", s.Sort, s.Grouped)
	}
}

// Find is the guard against acting on a reused PID: the same number with a
// different session is a different process.
func TestFindRequiresTheSessionToMatchThePID(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	if _, ok := c.Find(1, "s1"); !ok {
		t.Error("want the agent found")
	}
	if a, ok := c.Find(1, "some-other-session"); ok {
		t.Errorf("Find matched pid 1 against the wrong session: %+v", a)
	}
	if _, ok := c.Find(999, "s1"); ok {
		t.Error("Find matched a pid that is not listed")
	}
}

func TestFormatTokens(t *testing.T) {
	for n, want := range map[int]string{
		0: "—", -1: "—", 999: "999", 1000: "1k", 158_000: "158k",
		200_000: "200k", 1_000_000: "1M", 1_250_000: "1.2M",
	} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:              "30s",
		18 * time.Minute:              "18m",
		4*time.Hour + 42*time.Minute:  "4h 42m",
		4*time.Hour + 2*time.Minute:   "4h 02m",
		4*24*time.Hour + 14*time.Hour: "4d 14h",
		-time.Hour:                    "0s",
	} {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// The page is a static bundle; it must not reach for anything off-machine.
func TestPageIsSelfContained(t *testing.T) {
	page := Page()
	for _, forbidden := range []string{"http://", "https://", "//fonts.", "cdn."} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page references %q; it must be fully self-contained", forbidden)
		}
	}
	if !strings.Contains(page, "<!doctype html>") {
		t.Error("the page should be a complete document")
	}
}

// Density is a question about height, not about a number of agents: eighteen
// rows fit at 1440 wide and do not fit on any laptop.
func TestCapacityDrivesDensity(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	many := make([]ui.Row, 0, 12)
	for i := 0; i < 12; i++ {
		many = append(many, ui.Row{Agent: registry.Agent{PID: i + 10, Live: true}, Name: "a"})
	}
	c.SetRows(many, Account{}, nil, now)
	if !c.Snapshot(now).Compact {
		t.Error("twelve rows with no measurement yet should fall back to the count")
	}

	c.SetCapacity(20)
	if c.Snapshot(now).Compact {
		t.Error("twelve rows in a window that fits twenty should stay comfortable")
	}
	c.SetCapacity(6)
	if !c.Snapshot(now).Compact {
		t.Error("twelve rows in a window that fits six should go compact")
	}

	// An explicit choice still wins over any measurement.
	c.SetDensity("comfy")
	if c.Snapshot(now).Compact {
		t.Error("an explicit comfy choice must beat the measurement")
	}
}

func TestSetCapacityIgnoresNoiseAndNonsense(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	if !c.SetCapacity(10) {
		t.Error("the first measurement is a change")
	}
	if c.SetCapacity(10) {
		t.Error("the same measurement again should not force a repaint")
	}
	if c.SetCapacity(-1) {
		t.Error("a negative capacity should be ignored")
	}
}

// Grouping is only coherent under the status sort. Bands of statuses over a
// list ordered by context would hide the ordering, and would leave the
// keyboard walking rows in an order the screen does not show.
func TestGroupingOnlyAppliesToTheStatusSort(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	if !c.Snapshot(now).Grouped {
		t.Error("grouped is the default under the status sort")
	}
	c.SetSort("context")
	if c.Snapshot(now).Grouped {
		t.Error("a context sort must not be rendered as status bands")
	}
	// The preference itself survives, so going back to status regroups.
	c.SetSort("status")
	if !c.Snapshot(now).Grouped {
		t.Error("returning to the status sort should restore grouping")
	}
	c.SetGrouped(false)
	if c.Snapshot(now).Grouped {
		t.Error("an explicit flat choice must be honoured")
	}
}

// The page drops the context and reference columns when nothing on the
// machine can fill them. That has to be counted over every agent: deciding
// it from the rows the filter left would collapse a column the moment a
// query happened to match only agents without one, and reflow the whole
// table on the next keystroke.
func TestColumnsAreDecidedBeforeTheFilter(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)

	if s := c.Snapshot(now); !s.AnyContext || !s.AnyRef {
		t.Fatalf("unfiltered: anyContext = %t, anyRef = %t, want both true", s.AnyContext, s.AnyRef)
	}
	// "README" is the one row with neither a context reading nor a
	// reference, so the filtered list cannot answer either question.
	c.SetFilter("README")
	s := c.Snapshot(now)
	if len(s.Agents) != 1 {
		t.Fatalf("filter matched %d agents, want 1", len(s.Agents))
	}
	if s.Agents[0].ContextPct != nil || s.Agents[0].Ref != "" {
		t.Fatalf("the fixture changed: %+v still fills a column", s.Agents[0])
	}
	if !s.AnyContext || !s.AnyRef {
		t.Errorf("filtered: anyContext = %t, anyRef = %t; the columns went away with the filter",
			s.AnyContext, s.AnyRef)
	}
}

// Without the shim and without gh there is nothing to show, and the columns
// should go rather than stand empty.
func TestColumnsGoWhenNothingCanFillThem(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows([]ui.Row{{
		Agent: registry.Agent{PID: 9, SessionID: "s9", Status: "busy", Live: true},
		Name:  "bare", Age: now,
	}}, Account{}, nil, now)
	if s := c.Snapshot(now); s.AnyContext || s.AnyRef {
		t.Errorf("anyContext = %t, anyRef = %t, want both false", s.AnyContext, s.AnyRef)
	}
}

// Grouping defaults to on, and a file that never mentions it has not turned
// it off. Reading the key unconditionally made every prefs file written by
// another build open the window ungrouped, with nothing to say it was not
// the choice you made.
func TestAPrefsFileMayLeaveKeysOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"density":"compact"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := snap(t, New(path))
	if !s.Grouped {
		t.Error("grouped = false; a file that omits the key should keep the default")
	}
	if s.Sort != "status" {
		t.Errorf("sort = %q, want status", s.Sort)
	}
	if !s.Compact {
		t.Error("compact = false; the one key the file did set was ignored")
	}
}

// app.json is an ordinary file: it can be edited by hand, or written by a
// build that sized its columns differently. A width narrower than a drag can
// produce is read as "never dragged" rather than applied, because a header
// that narrow shows nothing and has no divider left to grab.
//
// A wide one is passed through. There is no maximum on this side: how wide a
// column may be is a question about the window, and the page is the half
// that can see it.
func TestAColumnTooNarrowToHaveBeenDraggedIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	const raw = `{"sort":"age","columns":{"status":900,"age":10,"name":-5,"repo":180}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got := New(path).Snapshot(now).Columns
	if want := (Columns{Status: 900, Repo: 180}); got != want {
		t.Errorf("columns = %+v, want %+v", got, want)
	}
}

// A width the page sends goes through the same check, and survives a
// relaunch - the whole point of writing it down.
func TestDraggedColumnsSurviveARelaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	c := New(path)
	c.Handle(`{"cmd":"columns","widths":{"name":260,"ref":12}}`)

	got := New(path).Snapshot(now).Columns
	if want := (Columns{Name: 260}); got != want {
		t.Errorf("reopened with %+v, want %+v", got, want)
	}
}

// Nothing in the language ties the page's floor to Go's, and a drag that
// stopped at one number while the other threw it away would look like the
// window forgetting a width you had just set.
func TestThePageAndGoAgreeOnTheNarrowestColumn(t *testing.T) {
	m := regexp.MustCompile(`const MIN_COL = (\d+)`).
		FindStringSubmatch(string(mustRead("assets/console.js")))
	if m == nil {
		t.Fatal("no MIN_COL in console.js; the pattern needs updating")
	}
	if m[1] != strconv.Itoa(minColumnPx) {
		t.Errorf("console.js clamps a drag at %spx but minColumnPx is %d", m[1], minColumnPx)
	}
}
