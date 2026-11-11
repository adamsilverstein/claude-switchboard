//go:build darwin && cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	webview "github.com/webview/webview_go"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
)

// runApp opens the Console in its own native window: a WKWebView rendering
// the page from internal/appui, fed a JSON snapshot once a second. The app
// gets its own Dock icon and cmd-tab entry, which is the whole point -
// switching back to the picker no longer means hunting for the right iTerm
// tab.
//
// The terminal picker is untouched by any of this. `switchboard` with no
// arguments is the same Bubble Tea program it always was; the two front ends
// meet only in internal/ui, whose filter and sort both of them call.
func runApp(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("app takes no arguments")
	}

	src, err := newAppSource()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	prefs, err := appui.DefaultPrefsPath()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	control := appui.New(prefs)

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Switchboard")
	w.SetSize(1240, 820, webview.HintNone)
	w.SetSize(980, 640, webview.HintMin)

	// The page is only ready to be pushed to once its script has run, so
	// frames before that are dropped rather than queued: a snapshot is a
	// whole picture of now, and a stale one is worth nothing.
	var (
		mu     sync.Mutex
		ready  bool
		closed bool
	)

	// dispatch is the only way anything off the UI thread reaches the
	// window, and it holds the lock across the call. Quitting closes the
	// webview and frees it, and the poll goroutine can easily be a scan
	// deep in the middle of an iteration when that happens: dispatching
	// to a destroyed webview is a use-after-free, not a no-op. Taking the
	// lock to mark it closed cannot overtake a dispatch already under way.
	dispatch := func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		w.Dispatch(f)
	}
	push := func(s appui.Snapshot) {
		raw, err := json.Marshal(s)
		if err != nil {
			return
		}
		arg, err := json.Marshal(string(raw))
		if err != nil {
			return
		}
		dispatch(func() { w.Eval("window.__snapshot && window.__snapshot(" + string(arg) + ")") })
	}
	notice := func(text string, alert bool) {
		msg, err := json.Marshal(text)
		if err != nil {
			return
		}
		dispatch(func() {
			w.Eval(fmt.Sprintf("window.__notice && window.__notice(%s, %t)", msg, alert))
		})
	}

	showPeek := func(v appui.PeekView) {
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		arg, err := json.Marshal(string(raw))
		if err != nil {
			return
		}
		dispatch(func() { w.Eval("window.__peek && window.__peek(" + string(arg) + ")") })
	}

	// replied tells the page whether its reply went, so it can clear the
	// input on success and keep the text to retry on failure.
	replied := func(act appui.Action, ok bool) {
		dispatch(func() {
			w.Eval(fmt.Sprintf("window.__replied && window.__replied(%d, %t)", act.ID, ok))
		})
	}

	if err := w.Bind("cmd", func(raw string) {
		switch act := control.Handle(raw); act.Kind {
		case "":
			// Any command at all proves the page has run its
			// script and can be pushed to.
			mu.Lock()
			ready = true
			mu.Unlock()
			if act.Repaint {
				push(control.Snapshot(time.Now()))
			}
		case "quit":
			w.Dispatch(w.Terminate)
		case "reply":
			// Typing runs AppleScript, with a pause before the
			// return; off the UI thread like focus.
			go replyApp(control, act, notice, replied)
		case "peek":
			// A transcript read of up to 256KB; off the UI thread.
			go peekApp(control, src.projectsDir, act, showPeek)
		case "open":
			// `open` hands the URL to the default browser and
			// returns immediately, but it is still a subprocess.
			go openURL(act.URL, notice)
		default:
			// Focus takes AppleScript and stop takes a fresh
			// liveness check, so both run off the UI thread: the
			// window must not sit frozen while iTerm is raised.
			go perform(control, act, notice)
		}
	}); err != nil {
		return fmt.Errorf("app: %w", err)
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			now := time.Now()
			rows, err := src.rows()
			control.SetRows(rows, src.account(now), err, now)
			mu.Lock()
			up := ready
			mu.Unlock()
			if up {
				push(control.Snapshot(now))
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()

	w.SetHtml(appui.Page())
	w.Run()

	// Run has returned, so the window has gone and the deferred Destroy
	// is next. Shut the door before it does, then stop the poll.
	mu.Lock()
	closed = true
	mu.Unlock()
	close(done)
	return nil
}

// pollInterval matches the terminal picker's. No AppleScript runs on this
// path: window resolution stays lazy, on focus.
const pollInterval = time.Second

// perform carries out a focus or a stop and reports back through the notice
// line.
//
// The session id travels with the PID for a reason. registry.SameProcess's
// own doc comment warns that a caller about to focus or signal must re-check
// against a fresh snapshot, because the PID may have been reused since the
// scan. Matching on both means a click on a row whose agent has since exited
// finds nothing rather than finding whatever took its number.
// openURL shows a pull request or issue in the browser. The URL has already
// been checked by the controller; nothing here builds one from page input.
func openURL(url string, notice func(string, bool)) {
	if err := exec.Command("open", url).Run(); err != nil {
		notice("could not open "+url, true)
		return
	}
	notice("opened "+url, false)
}

// peekApp reads the last exchange of the agent the page asked about and
// sends it back. The page drops an answer for an agent it has stopped
// peeking at, so a slow read cannot paint over a newer one.
func peekApp(control *appui.Controller, projectsDir string, act appui.Action, show func(appui.PeekView)) {
	v := appui.PeekView{PID: act.PID, SessionID: act.SessionID}
	agent, ok := control.Find(act.PID, act.SessionID)
	if !ok {
		v.Error = "agent is gone"
		show(v)
		return
	}
	ex, err := peekAgent(projectsDir, agent)
	if err != nil {
		v.Error = err.Error()
	}
	v.Asked, v.Reply, v.Truncated = ex.Asked, ex.Reply, ex.Truncated
	show(v)
}

// replyApp types a reply from the peek panel into the agent's terminal.
func replyApp(control *appui.Controller, act appui.Action, notice func(string, bool), replied func(appui.Action, bool)) {
	agent, ok := control.Find(act.PID, act.SessionID)
	if !ok {
		notice("agent is gone; not sending", true)
		replied(act, false)
		return
	}
	if err := replyAgent(agent, act.Text); err != nil {
		notice("reply failed: "+err.Error(), true)
		replied(act, false)
		return
	}
	notice("sent to "+agent.Name, false)
	replied(act, true)
}

func perform(control *appui.Controller, act appui.Action, notice func(string, bool)) {
	agent, ok := control.Find(act.PID, act.SessionID)
	if !ok {
		notice("agent is gone", true)
		return
	}
	switch act.Kind {
	case "focus":
		desc, err := focusAgent(agent)
		if err != nil {
			notice("focus failed: "+err.Error(), true)
			return
		}
		notice("focused "+desc, false)
	case "stop":
		if err := stopAgent(agent); err != nil {
			notice("stop failed: "+err.Error(), true)
			return
		}
		notice(fmt.Sprintf("sent SIGTERM to %s", agent.Name), false)
	case "copy":
		if err := copyText(agent.Cwd); err != nil {
			notice("could not copy: "+err.Error(), true)
			return
		}
		notice("copied "+agent.Cwd, false)
	}
}

// copyText puts text on the clipboard. The page cannot: it is loaded with
// no origin, and WebKit withholds navigator.clipboard from such a page.
//
// pbcopy reads its input in the locale's encoding, and an app launched from
// the Dock or Finder inherits no LANG at all, so a directory name with an
// accent in it would be mangled while pbcopy reported success. The locale is
// pinned to UTF-8 for this one subprocess.
func copyText(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
