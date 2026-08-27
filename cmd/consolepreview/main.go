// Command consolepreview serves the app window's page in an ordinary browser,
// backed by fabricated agents and the real appui.Controller.
//
// It exists because the app window needs a cgo build and a Mac to open, and
// because checking that the layout survives eighteen agents at 980x700 should
// not require eighteen agents. Everything below the page - filtering,
// sorting, density, the snapshot format - is the code that ships; only the
// agents and the transport are pretend.
//
//	go run ./cmd/consolepreview -agents 18
//	open http://localhost:8765
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/forge"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func main() {
	addr := flag.String("addr", "localhost:8765", "address to serve on")
	n := flag.Int("agents", 5, "how many agents to fabricate")
	bare := flag.Bool("bare", false, "omit the statusline telemetry, as on a machine with no shim")
	flag.Parse()

	c := appui.New("")
	c.SetRows(fixture(*n, *bare), account(*bare), nil, time.Now())

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, strings.Replace(appui.Page(), "</body>", bridge+"</body>", 1))
	})
	http.HandleFunc("/cmd", func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			Cmd    string        `json:"cmd"`
			Key    string        `json:"key"`
			Q      string        `json:"q"`
			On     bool          `json:"on"`
			Value  string        `json:"value"`
			Rows   int           `json:"rows"`
			Widths appui.Columns `json:"widths"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		switch m.Cmd {
		case "sort":
			c.SetSort(m.Key)
		case "filter":
			c.SetFilter(m.Q)
		case "group":
			c.SetGrouped(m.On)
		case "density":
			c.SetDensity(m.Value)
		case "capacity":
			c.SetCapacity(m.Rows)
		case "columns":
			c.SetColumns(m.Widths)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(c.Snapshot(time.Now()))
	})

	log.Printf("console preview on http://%s (%d agents)", *addr, *n)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// bridge stands in for the WKWebView bindings: cmd() posts and applies
// whatever snapshot comes back, which is exactly what the real window does.
//
// It deliberately does not kick off the first frame. The page announces
// itself, and letting the preview do it instead would hide a page that had
// stopped announcing - which is exactly what it did hide once.
const bridge = `<script>
async function cmd(json) {
  const res = await fetch("/cmd", { method: "POST", body: json });
  window.__snapshot(JSON.stringify(await res.json()));
}
</script>
`

// The fixture is deliberately awkward. A tidy one hid a real bug once: every
// fabricated name happened to fit its column, so a name that did not clip
// looked identical to one that did, and the overflow only showed up against
// live agents. At least one of everything here is too long for its space.
var (
	names = []string{
		`Redesign the app window: "Console" layout with live session telemetry #11`,
		"Notes: Open a full searchable emoji picker from the add-reaction button (stacked on #76767) #78176",
		"Suggest mode 1/9: editor intent #80427", "Client Side Media iteration for WP 7.2 #80159",
		"README improvements", "Command-q quit command", "Image block hides Resolution #81902",
		"Console redesign spec", "Focus multi-monitor fix", "Interactivity API audit",
		"Block bindings docs", "Performance sweep", "REST API schema", "Theme.json migration",
		"E2E flake hunt", "Playground blueprint", "PHPStan baseline", "Trac triage",
		"Icon artwork", "Release notes",
	}
	summaries = []string{
		"Two placements for the badge — which do you want?",
		"Pulling the issue and its linked PRs.",
		"Restructuring the README — top section first.",
		"Green. Smoke test confirms cmd-q closes the window.",
		"Done — PR #81938 merged, issue closed.",
		"Everything is now verified locally. Final state on [#76767](https://github.com/WordPress/gutenberg/pull/76767) — 8 commits, `f1f9816` → `d8018c7`: | # | Finding | Fix | Proven red first | |---|---|---|---| | 1 | Reactions mutable via inherited update route | `update_item_permissions_check()` rejects reactions (403) | 3/4 tests fail without it |",
	}
	statuses = []string{"idle", "busy", "busy", "idle", "idle", "busy", "shell", "idle"}
	repos    = []string{"gutenberg", "add-notes-emoji-reactions-picker", "switchboard", "media-experiments"}
	branches = []string{"trunk", "add-notes-emoji-reactions-try-additional-comment-type", "trunk", "feature/x"}
	models   = []string{"Opus 5", "Opus 5", "Sonnet 4.6", "Sonnet 4.6", "Opus 5"}
	windows  = []int{1_000_000, 1_000_000, 200_000, 200_000, 1_000_000}
	used     = []int{158_000, 620_000, 176_000, 62_000, 310_000}
	modes    = []string{"auto", "plan", "default"}
	// One of each state, plus a branch with no pull request at all: the
	// dash is as much a case worth looking at as the number is.
	refs = []forge.Ref{
		{Number: 13, Kind: "pr", State: "open", Title: "Replace the app window's terminal with a Console", URL: "https://github.com/adamsilverstein/claude-switchboard/pull/13", Known: true},
		{Number: 76767, Kind: "pr", State: "draft", Title: "Add emoji reactions to notes", URL: "https://github.com/WordPress/gutenberg/pull/76767", Known: true},
		{Known: true},
		{Number: 11, Kind: "issue", State: "open", Title: "Console redesign", URL: "https://github.com/adamsilverstein/claude-switchboard/issues/11", Known: true},
		{Number: 10, Kind: "pr", State: "merged", Title: "Fix multi-monitor focus", URL: "https://github.com/adamsilverstein/claude-switchboard/pull/10", Known: true},
		{Number: 81938, Kind: "pr", State: "closed", Title: "Speculative loading defaults", URL: "https://github.com/WordPress/wordpress-develop/pull/81938", Known: true},
	}
)

func fixture(n int, bare bool) []ui.Row {
	now := time.Now()
	rows := make([]ui.Row, 0, n)
	for i := 0; i < n; i++ {
		repo := repos[i%len(repos)]
		t := ui.Telemetry{
			Repo:    repo,
			Branch:  branches[i%len(branches)],
			Dirty:   i%3 == 0,
			Elapsed: time.Duration(i+1) * 47 * time.Minute,
			TTY:     fmt.Sprintf("/dev/ttys0%02d", 10+i),
			// The first agent, and one further down, have handed the
			// turn back - the case the whole triage design is for.
			Waiting: i == 0 || i == 5,
		}
		if !bare {
			t.Ref = refs[i%len(refs)]
			t.Model = models[i%len(models)]
			t.ContextWindow = windows[i%len(windows)]
			t.ContextTokens = used[i%len(used)]
			t.PermissionMode = modes[i%len(modes)]
		}
		status := statuses[i%len(statuses)]
		if t.Waiting {
			status = "idle"
		}
		rows = append(rows, ui.Row{
			Agent: registry.Agent{
				PID: 48200 + i, SessionID: fmt.Sprintf("s%d", i), Status: status,
				Cwd: "/Users/x/repositories/" + repo, Live: i != 4, Entrypoint: "cli",
			},
			Name:      names[i%len(names)],
			Summary:   summaries[i%len(summaries)],
			Age:       now.Add(-time.Duration(i*17+1) * time.Minute),
			Telemetry: t,
		})
	}
	return rows
}

// account is what the statusline shim would have recorded, or nothing at all
// on a machine where it is not installed.
func account(bare bool) appui.Account {
	if bare {
		return appui.Account{}
	}
	five, seven := 31, 12
	return appui.Account{
		Usage5hPct: &five, Usage5hResetsIn: "3h 12m",
		Usage7dPct: &seven, Usage7dResetsIn: "4d 14h",
	}
}
