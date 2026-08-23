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
