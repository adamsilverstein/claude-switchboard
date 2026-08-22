package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

var base = time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)

func testRows() []Row {
	return []Row{
		{Agent: registry.Agent{PID: 1, Name: "alpha gutenberg fix", Status: "idle", Cwd: "/repo/gutenberg", Live: true}, Name: "alpha gutenberg fix", Summary: "waiting for review", Age: base.Add(-10 * time.Minute)},
		{Agent: registry.Agent{PID: 2, Name: "beta core patch", Status: "busy", Cwd: "/repo/core", Live: true}, Name: "beta core patch", Summary: "running tests", Age: base.Add(-1 * time.Minute)},
		{Agent: registry.Agent{PID: 3, Name: "gamma docs", Status: "idle", Cwd: "/repo/docs", Live: true}, Name: "gamma docs", Summary: "drafting", Age: base.Add(-2 * time.Hour)},
		{Agent: registry.Agent{PID: 4, Name: "delta finished", Status: "idle", Cwd: "/repo/old", Live: false}, Name: "delta finished", Summary: "done", Age: base.Add(-3 * time.Hour)},
	}
}

func loaded(t *testing.T, focuser Focuser) Model {
	t.Helper()
	return loadedWithStopper(t, focuser, nil)
}

func loadedWithStopper(t *testing.T, focuser Focuser, stopper Stopper) Model {
	t.Helper()
	m := New(func() ([]Row, error) { return testRows(), nil }, focuser, stopper)
	next, _ := m.Update(rowsMsg{rows: testRows()})
	return next.(Model)
}

// press sends one key and returns the new model and any command.
func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+x":
		msg = tea.KeyMsg{Type: tea.KeyCtrlX}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// runCmd executes a command and returns every message it produces, flattening
// the tea.Batch that enter now returns (a flash tick alongside the focus).
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, c := range batch {
		msgs = append(msgs, runCmd(c)...)
	}
	return msgs
}

// applyAll feeds every message a command produced back into the model.
func applyAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range runCmd(cmd) {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func visiblePIDs(m Model) []int {
	rows := m.visible()
	pids := make([]int, len(rows))
	for i, r := range rows {
		pids[i] = r.Agent.PID
	}
	return pids
}

func pidsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDefaultSortPutsAttentionFirstDeadLast(t *testing.T) {
	m := loaded(t, nil)
	// Status sort: idle before busy, dead always last; ties break newest
	// first, so the fresher idle agent leads.
	if got := visiblePIDs(m); !pidsEqual(got, []int{1, 3, 2, 4}) {
		t.Errorf("order = %v", got)
	}
}

func TestSortKeys(t *testing.T) {
	m := loaded(t, nil)
	m, _ = press(t, m, "a") // age: newest first, dead still last
	if got := visiblePIDs(m); !pidsEqual(got, []int{2, 1, 3, 4}) {
		t.Errorf("age order = %v", got)
	}
	m, _ = press(t, m, "n")
	if got := visiblePIDs(m); !pidsEqual(got, []int{1, 2, 3, 4}) {
		t.Errorf("name order = %v", got)
	}
	m, _ = press(t, m, "d")
	if got := visiblePIDs(m); !pidsEqual(got, []int{2, 3, 1, 4}) {
		t.Errorf("dir order = %v", got)
	}
}

func TestIncrementalFilter(t *testing.T) {
	m := loaded(t, nil)
	m, _ = press(t, m, "/")
	for _, r := range "core" {
		m, _ = press(t, m, string(r))
	}
	if got := visiblePIDs(m); !pidsEqual(got, []int{2}) {
		t.Errorf("filtered = %v", got)
	}
	// Backspace widens the filter again.
	m, _ = press(t, m, "backspace")
	if m.filter != "cor" {
		t.Errorf("filter = %q", m.filter)
	}
	// Esc clears it entirely.
	m, _ = press(t, m, "esc")
	if m.filter != "" || len(visiblePIDs(m)) != 4 {
		t.Errorf("after esc: filter=%q rows=%v", m.filter, visiblePIDs(m))
	}
}

func TestFilterMatchesSummaryAndDir(t *testing.T) {
	m := loaded(t, nil)
	m, _ = press(t, m, "/")
	for _, r := range "running tests" {
		m, _ = press(t, m, string(r))
	}
	if got := visiblePIDs(m); !pidsEqual(got, []int{2}) {
		t.Errorf("summary filter = %v", got)
	}
}

func TestEnterFocusesSelection(t *testing.T) {
	var focused int
	m := loaded(t, func(a registry.Agent) (string, error) {
		focused = a.PID
		return "iTerm window 3", nil
	})
	m, _ = press(t, m, "down")
	m, cmd := press(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter should produce a focus command")
	}
	// The keypress is acknowledged before the focus finishes, so the row
	// flashes and the footer says what is happening straight away.
	if m.flash == 0 {
		t.Error("enter should start the flash on the picked row")
	}
	if !strings.Contains(m.View(), "focusing gamma docs") {
		t.Error("view should say which agent is being focused, before it lands")
	}
	m = applyAll(t, m, cmd)
	if focused != 3 {
		t.Errorf("focused PID = %d, want 3 (second row in status order)", focused)
	}
	if !strings.Contains(m.View(), "focused iTerm window 3") {
		t.Error("view should show the focus notice")
	}
	if m.flash != 0 {
		t.Error("flash should stop once the focus lands")
	}
}

func TestFlashDecaysToZero(t *testing.T) {
	m := loaded(t, func(a registry.Agent) (string, error) { return "somewhere", nil })
	m, _ = press(t, m, "enter")
	start := m.flash
	if start == 0 {
		t.Fatal("enter should start the flash")
	}
	// Each flash frame schedules the next until it runs out, so the row
	// cannot blink forever if the focus never reports back.
	for i := 0; i < start+2; i++ {
		next, _ := m.Update(flashMsg{})
		m = next.(Model)
	}
	if m.flash != 0 {
		t.Errorf("flash = %d after %d frames, want 0", m.flash, start+2)
	}
}

func TestEnterOnDeadAgentDoesNotFocus(t *testing.T) {
	m := loaded(t, func(a registry.Agent) (string, error) {
		t.Fatal("focuser must not be called for a dead agent")
		return "", nil
	})
	m, _ = press(t, m, "G") // dead agent sorts last
	m, cmd := press(t, m, "enter")
	if cmd != nil {
		t.Fatal("no command expected for a dead agent")
	}
	if !strings.Contains(m.View(), "nothing to focus") {
		t.Error("view should explain there is nothing to focus")
	}
}

func TestFocusErrorShownInFooter(t *testing.T) {
	m := loaded(t, func(a registry.Agent) (string, error) {
		return "", fmt.Errorf("no iTerm window found")
	})
	m, cmd := press(t, m, "enter")
	m = applyAll(t, m, cmd)
	if !strings.Contains(m.View(), "focus failed: no iTerm window found") {
		t.Error("view should surface the focus error")
	}
}

func TestRefreshKeepsCursorOnSameAgent(t *testing.T) {
	m := loaded(t, nil)
	m, _ = press(t, m, "down") // cursor on PID 3 in status order
	// A refresh arrives where the busy agent went idle, reshuffling order.
	rows := testRows()
	rows[1].Agent.Status = "idle"
	rows[1].Age = base
	next, _ := m.Update(rowsMsg{rows: rows})
	m = next.(Model)
	if got := m.visible()[m.cursor].Agent.PID; got != 3 {
		t.Errorf("cursor followed PID %d, want 3", got)
	}
}

func TestViewRendersColumns(t *testing.T) {
	m := loaded(t, nil)
	m.width, m.height = 120, 30
	view := m.View()
	for _, want := range []string{"alpha gutenberg fix", "running tests", "sort: status", "enter focus"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	// The working directory is not a column any more; the room goes to the
	// summary instead.
	for _, gone := range []string{"DIR", "/repo/gutenberg"} {
		if strings.Contains(view, gone) {
			t.Errorf("view should no longer show %q", gone)
		}
	}
}

func TestFilterStillMatchesDirectory(t *testing.T) {
	// The column is gone but the directory is still worth filtering on.
	m := loaded(t, nil)
	m, _ = press(t, m, "/")
	for _, r := range "gutenberg" {
		m, _ = press(t, m, string(r))
	}
	if got := visiblePIDs(m); !pidsEqual(got, []int{1}) {
		t.Errorf("directory filter = %v, want [1]", got)
	}
}

func TestQuit(t *testing.T) {
	m := loaded(t, nil)
	m, cmd := press(t, m, "q")
	if cmd == nil {
		t.Fatal("q should quit")
	}
	if m.View() != "" {
		t.Error("quitting view should be empty")
	}
}

func TestStopRequiresConfirmation(t *testing.T) {
	var stopped int
	m := loadedWithStopper(t, nil, func(a registry.Agent) error {
		stopped = a.PID
		return nil
	})
	m, cmd := press(t, m, "ctrl+x")
	if cmd != nil {
		t.Fatal("ctrl+x alone must not stop anything")
	}
	if !strings.Contains(m.View(), "y/N") {
		t.Error("view should ask for confirmation")
	}
	m, cmd = press(t, m, "y")
	if cmd == nil {
		t.Fatal("y should confirm the stop")
	}
	msg := cmd()
	if stopped != 1 {
		t.Errorf("stopped PID = %d, want 1 (first row in status order)", stopped)
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if !strings.Contains(m.View(), "sent SIGTERM") {
		t.Error("view should report the stop")
	}
}

func TestStopCancelledByAnyOtherKey(t *testing.T) {
	m := loadedWithStopper(t, nil, func(a registry.Agent) error {
		t.Fatal("stopper must not be called after cancel")
		return nil
	})
	m, _ = press(t, m, "ctrl+x")
	m, cmd := press(t, m, "n")
	if cmd != nil {
		t.Fatal("cancel must not produce a command")
	}
	if !strings.Contains(m.View(), "stop cancelled") {
		t.Error("view should show the cancellation")
	}
}

func TestStopDeadAgentRefused(t *testing.T) {
	m := loadedWithStopper(t, nil, func(a registry.Agent) error {
		t.Fatal("stopper must not be called for a dead agent")
		return nil
	})
	m, _ = press(t, m, "G")
	m, _ = press(t, m, "ctrl+x")
	if !strings.Contains(m.View(), "already gone") {
		t.Error("view should say the agent is already gone")
	}
}

func TestBackspaceRemovesWholeRune(t *testing.T) {
	m := loaded(t, nil)
	m, _ = press(t, m, "/")
	for _, r := range "café" {
		m, _ = press(t, m, string(r))
	}
	m, _ = press(t, m, "backspace")
	if m.filter != "caf" {
		t.Errorf("filter = %q, want %q (backspace must trim the whole multi-byte rune)", m.filter, "caf")
	}
}
