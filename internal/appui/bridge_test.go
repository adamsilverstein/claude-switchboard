package appui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func loaded(t *testing.T) *Controller {
	t.Helper()
	c := New(filepath.Join(t.TempDir(), "app.json"))
	c.SetRows(rows(), Account{}, nil, now)
	return c
}

// These are the exact messages assets/console.js sends. Nothing else checks
// that the two halves of the bridge agree on field names, so if a rename on
// one side is not mirrored on the other, it fails here rather than silently
// in a window nobody can unit-test.
func TestHandleAppliesEveryCommandThePageSends(t *testing.T) {
	c := loaded(t)

	if act := c.Handle(`{"cmd":"hello"}`); !act.Repaint || act.Kind != "" {
		t.Errorf("hello = %+v; want a repaint and nothing else", act)
	}
	if act := c.Handle(`{"cmd":"sort","key":"context"}`); !act.Repaint {
		t.Error("sort should repaint")
	}
	if got := c.Snapshot(now).Sort; got != "context" {
		t.Errorf("sort = %q, want context", got)
	}
	if act := c.Handle(`{"cmd":"filter","q":"gutenberg"}`); !act.Repaint {
		t.Error("filter should repaint")
	}
	if got := c.Snapshot(now).Filter; got != "gutenberg" {
		t.Errorf("filter = %q, want gutenberg", got)
	}
	c.Handle(`{"cmd":"filter","q":""}`)

	if act := c.Handle(`{"cmd":"group","on":false}`); !act.Repaint {
		t.Error("group should repaint")
	}
	c.Handle(`{"cmd":"sort","key":"status"}`)
	if c.Snapshot(now).Grouped {
		t.Error("group off did not take")
	}
	if act := c.Handle(`{"cmd":"density","value":"compact"}`); !act.Repaint {
		t.Error("density should repaint")
	}
	if !c.Snapshot(now).Compact {
		t.Error("density compact did not take")
	}
	if act := c.Handle(`{"cmd":"capacity","rows":14}`); !act.Repaint {
		t.Error("the first capacity report should repaint")
	}

	act := c.Handle(`{"cmd":"focus","pid":1,"sessionId":"s1"}`)
	if act.Kind != "focus" || act.PID != 1 || act.SessionID != "s1" {
		t.Errorf("focus = %+v, want {focus 1 s1}", act)
	}
	act = c.Handle(`{"cmd":"stop","pid":2,"sessionId":"s2"}`)
	if act.Kind != "stop" || act.PID != 2 || act.SessionID != "s2" {
		t.Errorf("stop = %+v, want {stop 2 s2}", act)
	}
	if act := c.Handle(`{"cmd":"quit"}`); act.Kind != "quit" {
		t.Errorf("quit = %+v", act)
	}
}

// A malformed message must not take the window down, and must not be
// mistaken for a focus or a stop.
func TestHandleIgnoresNonsense(t *testing.T) {
	c := loaded(t)
	for _, raw := range []string{
		``, `not json`, `{}`, `[]`, `null`,
		`{"cmd":"nosuchcommand"}`,
		`{"cmd":"sort"}`,  // no key: falls back to status
		`{"cmd":"focus"}`, // no agent named
		`{"cmd":42}`,      // wrong type
	} {
		act := c.Handle(raw)
		if act.Kind == "quit" {
			t.Errorf("%q was treated as a quit", raw)
		}
		if act.Kind == "stop" && act.PID == 0 {
			// A stop with no agent must find nothing, never signal.
			if _, ok := c.Find(act.PID, act.SessionID); ok {
				t.Errorf("%q resolved to an agent", raw)
			}
		}
	}
	// A focus naming no agent resolves to nothing rather than to pid 0.
	act := c.Handle(`{"cmd":"focus"}`)
	if _, ok := c.Find(act.PID, act.SessionID); ok {
		t.Error("an unaddressed focus resolved to an agent")
	}
}

// The two halves of the bridge must name the same commands. Nothing else
// checks that: a rename on one side and not the other would fail silently in
// a window that has no unit tests of its own. Both sets are read from source.
func TestBridgeCommandsMatchThePage(t *testing.T) {
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`cmd:\s*"(\w+)"`).FindAllStringSubmatch(
		string(mustRead("assets/console.js")), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("found no commands in console.js; the pattern needs updating")
	}
	// hello is not excused here. The page has to send it: nothing else
	// tells the window a frame would land anywhere, and a version of this
	// test that assumed the bootstrap sent it let a page ship that never
	// announced itself and so never showed a single agent.
	if !sent["hello"] {
		t.Error("console.js never sends hello; the window will hold every frame back")
	}

	// One case can label several commands ("focus", "stop"), so take
	// every quoted word on a case line rather than just the first.
	handled := map[string]bool{}
	for _, line := range strings.Split(controllerSource(t), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "case ") {
			continue
		}
		for _, m := range regexp.MustCompile(`"(\w+)"`).FindAllStringSubmatch(line, -1) {
			handled[m[1]] = true
		}
	}

	for name := range sent {
		if !handled[name] {
			t.Errorf("console.js sends %q but Handle has no case for it", name)
		}
	}
	for name := range handled {
		if !sent[name] {
			t.Errorf("Handle accepts %q but console.js never sends it", name)
		}
	}
}

func controllerSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("controller.go")
	if err != nil {
		t.Fatal(err)
	}
	// Only the Handle switch: the density values ("compact", "comfy")
	// are case labels elsewhere in the file and are not commands.
	i := strings.Index(string(raw), "func (c *Controller) Handle(")
	if i < 0 {
		t.Fatal("Handle not found in controller.go")
	}
	return string(raw)[i:]
}

// The list is drawn as a band per status, but the cursor walks the rows in
// the order Go sorted them. If the two orders disagree, arrowing down out of
// the top band lands somewhere further down the screen, skipping a whole band
// and then jumping back up - so the band order is read out of console.js and
// checked against the order SortRows actually produces.
func TestBandOrderMatchesTheSortOrder(t *testing.T) {
	m := regexp.MustCompile(`const STATUS_ORDER = \[([^\]]*)\]`).
		FindStringSubmatch(string(mustRead("assets/console.js")))
	if m == nil {
		t.Fatal("no STATUS_ORDER in console.js; the pattern needs updating")
	}
	var bands []string
	for _, q := range regexp.MustCompile(`"(\w*)"`).FindAllStringSubmatch(m[1], -1) {
		bands = append(bands, q[1])
	}
	if len(bands) == 0 {
		t.Fatal("STATUS_ORDER parsed as empty; the pattern needs updating")
	}
	// The two bands console.js appends after the named ones: a status it
	// does not know, and the ended rows.
	bands = append(bands, "", "dead")

	// One row per status the page can be handed, deliberately out of
	// order, and each with the shape statusWord derives that status from.
	rows := []ui.Row{
		{Agent: registry.Agent{PID: 1, Status: "busy", Live: true}},
		{Agent: registry.Agent{PID: 2, Status: "idle"}}, // not live: dead
		{Agent: registry.Agent{PID: 3, Status: "mystery", Live: true}},
		{Agent: registry.Agent{PID: 4, Status: "shell", Live: true}},
		{Agent: registry.Agent{PID: 5, Status: "idle", Live: true}},
		{Agent: registry.Agent{PID: 6, Status: "idle", Live: true}, Telemetry: ui.Telemetry{Waiting: true}},
	}
	ui.SortRows(rows, ui.SortStatus)

	named := map[string]bool{}
	for _, b := range bands {
		named[b] = true
	}
	var sorted []string
	for _, r := range rows {
		// A status console.js does not name lands in the band it
		// labels "Unknown", whose key is the empty string.
		w := statusWord(r)
		if !named[w] {
			w = ""
		}
		sorted = append(sorted, w)
	}
	// Every status is represented once, so the sorted sequence is the
	// band order or the two disagree.
	if strings.Join(sorted, ",") != strings.Join(bands, ",") {
		t.Errorf("console.js bands\n\t%v\nbut SortRows orders them\n\t%v", bands, sorted)
	}
}

// scrollCursorIntoView compares a row's offsetTop with the list's scrollTop,
// which are the same coordinate only while #list is a positioned box.
// Without the position the offset is measured from the body, so it carries
// the toolbar's height with it and the keyboard scrolls the selected row off
// the top of the window instead of to its edge. It is one declaration in the
// stylesheet, and nothing about the rule says the script depends on it.
func TestTheListIsTheOffsetParentTheScrollingAssumes(t *testing.T) {
	m := regexp.MustCompile(`#list\s*\{([^}]*)\}`).FindStringSubmatch(string(mustRead("assets/console.css")))
	if m == nil {
		t.Fatal("no #list rule in console.css; the pattern needs updating")
	}
	if !regexp.MustCompile(`position:\s*(relative|absolute|sticky|fixed)`).MatchString(m[1]) {
		t.Errorf("#list is not positioned, so offsetTop is measured from the body: {%s}", strings.TrimSpace(m[1]))
	}
	if !strings.Contains(string(mustRead("assets/console.js")), "row.offsetTop") {
		t.Error("scrollCursorIntoView no longer reads row.offsetTop; this test and the rule above it are stale")
	}
}
