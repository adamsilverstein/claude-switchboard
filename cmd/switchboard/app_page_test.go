package main

import (
	"strings"
	"testing"
)

func TestAppHTMLIsSelfContained(t *testing.T) {
	html := appHTML()

	// The embedded xterm.js dist must actually be present, not an empty
	// placeholder: its UMD banner defines the Terminal global the page
	// script constructs.
	for _, want := range []string{
		"new Terminal(",
		"FitAddon",
		"__ptyOut",
		"ptyInput",
		"ptyResize",
		"ptyReady",
		"quitApp",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("appHTML missing %q", want)
		}
	}
	if len(html) < 100_000 {
		t.Errorf("appHTML is %d bytes; embedded xterm.js dist looks missing", len(html))
	}

	// Self-contained means no network fetches from the page.
	for _, banned := range []string{"http://", "src=\"https:", "href=\"https:"} {
		if strings.Contains(html, banned) {
			t.Errorf("appHTML contains external reference %q", banned)
		}
	}
}

func TestAppHTMLHandlesCommandQ(t *testing.T) {
	html := appHTML()

	// The app window has no menu bar, so cmd-q only works if the page
	// intercepts it and calls back into Go.
	for _, want := range []string{
		"e.metaKey",
		`"q"`,
		"quitApp();",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("appHTML missing cmd-q handler part %q", want)
		}
	}

	// Capture phase: xterm.js listens on its own textarea, so a bubbling
	// listener would be too late to stop the key reaching the pty.
	if !strings.Contains(html, `window.addEventListener("keydown", (e) => {`) ||
		!strings.Contains(html, "}, true);") {
		t.Error("appHTML cmd-q listener is not registered on window in the capture phase")
	}
}
