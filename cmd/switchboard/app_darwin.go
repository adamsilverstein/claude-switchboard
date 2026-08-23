//go:build darwin && cgo

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	webview "github.com/webview/webview_go"
)

// runApp opens the picker in its own native window: a WKWebView rendering
// xterm.js, with this same binary's TUI (the no-argument mode) running
// behind it on a pty. The app gets its own Dock icon and cmd-tab entry,
// which is the whole point - switching back to the picker no longer means
// hunting for the right iTerm tab.
func runApp(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("app takes no arguments")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	// Re-exec ourselves with no arguments: the ordinary TUI, unchanged.
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		return fmt.Errorf("app: start tui: %w", err)
	}

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Switchboard")
	w.SetSize(1000, 620, webview.HintNone)

	// Pty output can arrive before the page has built its terminal, so
	// chunks are buffered until the page calls ptyReady.
	var (
		mu      sync.Mutex
		ready   bool
		pending []byte
	)
	emit := func(chunk []byte) {
		w.Eval("__ptyOut(\"" + base64.StdEncoding.EncodeToString(chunk) + "\")")
	}

	// Bound callbacks run on the main (UI) thread, so they may call Eval
	// directly; the pty reader goroutine below must go through Dispatch.
	if err := w.Bind("ptyInput", func(data string) {
		_, _ = ptmx.WriteString(data)
	}); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	if err := w.Bind("ptyResize", func(cols, rows int) {
		if cols > 0 && rows > 0 {
			_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
		}
	}); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	if err := w.Bind("ptyReady", func() {
		mu.Lock()
		ready = true
		buf := pending
		pending = nil
		mu.Unlock()
		if len(buf) > 0 {
			emit(buf)
		}
	}); err != nil {
		return fmt.Errorf("app: %w", err)
	}

	// cmd-q, forwarded by the page: this window has no menu bar, so the
	// shortcut has to come back through the bridge. Terminate is queued
	// rather than called inline so the window tears down after this
	// callback returns, not during it.
	if err := w.Bind("quitApp", func() {
		w.Dispatch(w.Terminate)
	}); err != nil {
		return fmt.Errorf("app: %w", err)
	}

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				mu.Lock()
				if !ready {
					pending = append(pending, chunk...)
					mu.Unlock()
				} else {
					mu.Unlock()
					w.Dispatch(func() { emit(chunk) })
				}
			}
			if readErr != nil {
				// The TUI exited (q inside the picker, or a crash);
				// close the window with it.
				w.Dispatch(w.Terminate)
				return
			}
		}
	}()

	w.SetHtml(appHTML())
	w.Run()

	// The window is gone, via either path: make sure the TUI is too.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = ptmx.Close()
	_ = cmd.Wait()
	return nil
}
