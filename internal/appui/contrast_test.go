package appui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The design sets its own accessibility bar: no text below 4.5:1 against any
// ground it can sit on, and no non-text graphic below 3:1. An earlier draft
// stated colours that did not meet it, which is exactly the kind of claim
// worth checking mechanically rather than asserting in a comment.
//
// The values are read out of the stylesheet, so editing a token there and
// not here fails the test rather than quietly lowering the bar. Both
// appearances are held to it.

// grounds are every surface text can land on.
var grounds = map[string]string{
	"ground":   "--ground",
	"alt row":  "--panel",
	"band":     "--band",
	"header":   "--thead",
	"selected": "--select",
	"field":    "--field",
	"menu":     "--menu",
}

// textTokens are every token that ever carries text.
var textTokens = []string{"--text", "--secondary", "--muted", "--busy", "--idle", "--alert", "--dead", "--accent-ink"}

// palette is one appearance's colours, read out of its :root block.
type palette struct {
	name   string
	tokens map[string]string
}

// palettes returns the light palette - the first :root block - and the dark
// one, the :root block inside the prefers-color-scheme: dark query. Each is
// checked on its own: a dark token that only passes against a light ground
// is no use to anyone looking at the dark window.
func palettes(t *testing.T) []palette {
	t.Helper()
	css := string(mustRead("assets/console.css"))
	light := rootBlock(t, css, 0)
	q := strings.Index(css, "@media (prefers-color-scheme: dark)")
	if q < 0 {
		t.Fatal("no dark appearance in console.css; the pattern needs updating")
	}
	dark := rootBlock(t, css, q)

	// The dark block must redefine every colour the light one sets, or
	// that token stays light in the dark window.
	for name := range light {
		if _, ok := dark[name]; !ok {
			t.Errorf("%s is set for the light appearance but not the dark one", name)
		}
	}
	return []palette{{"light", light}, {"dark", dark}}
}

func rootBlock(t *testing.T, css string, from int) map[string]string {
	t.Helper()
	start := strings.Index(css[from:], ":root {")
	if start < 0 {
		t.Fatal("no :root block in console.css")
	}
	start += from
	end := strings.Index(css[start:], "}")
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})`).
		FindAllStringSubmatch(css[start:start+end], -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 10 {
		t.Fatalf("only found %d colour tokens; the pattern needs updating", len(out))
	}
	return out
}

func TestTextTokensClearFourAndAHalf(t *testing.T) {
	for _, p := range palettes(t) {
		for _, name := range textTokens {
			for label, groundName := range grounds {
				got := ratio(t, p.tokens[name], p.tokens[groundName])
				if got < 4.5 {
					t.Errorf("%s: %s (%s) on %s (%s) is %.2f:1, under 4.5:1",
						p.name, name, p.tokens[name], label, p.tokens[groundName], got)
				}
			}
		}
	}
}

// The accent splits in two: one value for graphics, an ink for text set in
// the accent and for the primary button's fill.
func TestAccentSplitHoldsBothWays(t *testing.T) {
	for _, p := range palettes(t) {
		tok := p.tokens
		// --accent is graphics only - bars, the focus ring, the edge on
		// a selected row - so 3:1 is its bar, on every ground it is drawn
		// over.
		for label, groundName := range grounds {
			if got := ratio(t, tok["--accent"], tok[groundName]); got < 3 {
				t.Errorf("%s: --accent on %s is %.2f:1, under the 3:1 a graphic needs", p.name, label, got)
			}
		}
		// The primary button's label sits on --accent-ink, and on
		// --accent-strong under the pointer.
		// --on-accent is also the tick knocked out of the green box a
		// passing pull request gets, and of the grey one on an ended row.
		for _, fill := range []string{"--accent-ink", "--accent-strong", "--busy", "--dead"} {
			if got := ratio(t, tok["--on-accent"], tok[fill]); got < 4.5 {
				t.Errorf("%s: --on-accent on %s is %.2f:1, under 4.5:1", p.name, fill, got)
			}
		}
	}
}

// A bar is two blocks side by side; the fill has to be distinguishable from
// the track, which is the 3:1 non-text threshold.
func TestBarFillReadsAgainstItsTrack(t *testing.T) {
	for _, p := range palettes(t) {
		for _, fill := range []string{"--accent", "--alert"} {
			if got := ratio(t, p.tokens[fill], p.tokens["--track"]); got < 3 {
				t.Errorf("%s: a %s bar on --track is %.2f:1, under 3:1", p.name, fill, got)
			}
		}
	}
}

func ratio(t *testing.T, a, b string) float64 {
	t.Helper()
	if a == "" || b == "" {
		t.Fatalf("missing colour in %q / %q", a, b)
	}
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// luminance is the WCAG relative luminance of a #rrggbb colour.
func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		t.Fatalf("bad colour %q: %v", hex, err)
	}
	channel := func(c uint64) float64 {
		s := float64(c) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(v>>16&0xff) + 0.7152*channel(v>>8&0xff) + 0.0722*channel(v&0xff)
}

// Report the whole grid, so a change to the palette shows what it cost.
func TestContrastTable(t *testing.T) {
	order := []string{"ground", "alt row", "band", "header", "selected", "field", "menu"}
	for _, p := range palettes(t) {
		var b strings.Builder
		b.WriteString("\n " + p.name + "\n token        ")
		for _, g := range order {
			b.WriteString(fmt.Sprintf(" %8s", g))
		}
		b.WriteString("\n")
		for _, name := range textTokens {
			b.WriteString(fmt.Sprintf(" %-12s ", strings.TrimPrefix(name, "--")))
			for _, g := range order {
				b.WriteString(fmt.Sprintf(" %8.2f", ratio(t, p.tokens[name], p.tokens[grounds[g]])))
			}
			b.WriteString("\n")
		}
		t.Log(b.String())
	}
}
