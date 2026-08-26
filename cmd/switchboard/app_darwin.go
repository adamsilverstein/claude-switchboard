//go:build darwin && cgo

package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
		mu    sync.Mutex
		ready bool
	)
	push := func(s appui.Snapshot) {
		raw, err := json.Marshal(s)
		if err != nil {
			return
		}
		arg, err := json.Marshal(string(raw))
		if err != nil {
			return
		}
		w.Dispatch(func() { w.Eval("window.__snapshot && window.__snapshot(" + string(arg) + ")") })
	}
	notice := func(text string, alert bool) {
		msg, err := json.Marshal(text)
		if err != nil {
			return
		}
		w.Dispatch(func() {
			w.Eval(fmt.Sprintf("window.__notice && window.__notice(%s, %t)", msg, alert))
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
	}
}
