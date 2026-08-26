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
// not here fails the test rather than quietly lowering the bar.

// grounds are every surface text can land on.
var grounds = map[string]string{
	"ground":  "--ground",
	"alt row": "--panel",
	"band":    "--band",
}

func tokens(t *testing.T) map[string]string {
	t.Helper()
	css := string(mustRead("assets/console.css"))
	// Only the :root block, so a token redefined for a breakpoint does
	// not shadow the value the palette is written in.
	start := strings.Index(css, ":root {")
	end := strings.Index(css[start:], "}")
	if start < 0 || end < 0 {
		t.Fatal("no :root block in console.css")
	}
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
	tok := tokens(t)
	// Every token that ever carries text, and the surface it sits on.
	text := []string{"--text", "--secondary", "--muted", "--busy", "--idle", "--alert", "--dead"}
	for _, name := range text {
		for label, groundName := range grounds {
			got := ratio(t, tok[name], tok[groundName])
			if got < 4.5 {
				t.Errorf("%s (%s) on %s (%s) is %.2f:1, under 4.5:1",
					name, tok[name], label, tok[groundName], got)
			}
		}
	}
}

// The accent splits in two: one value for graphics, a darker one for any
// surface that carries text on it.
func TestAccentSplitHoldsBothWays(t *testing.T) {
	tok := tokens(t)

	// --accent is graphics only, so 3:1 is its bar - but it must be used
	// nowhere that carries text, which is what --accent-ink is for.
	if got := ratio(t, tok["--accent"], tok["--ground"]); got < 3 {
		t.Errorf("--accent on the ground is %.2f:1, under the 3:1 a graphic needs", got)
	}
	// White and the dimmed summary colour both sit on --accent-ink.
	for _, on := range []struct{ name, hex string }{
		{"white", "#ffffff"},
		{"--on-accent-dim", tok["--on-accent-dim"]},
	} {
		if got := ratio(t, on.hex, tok["--accent-ink"]); got < 4.5 {
			t.Errorf("%s on --accent-ink (%s) is %.2f:1, under 4.5:1",
				on.name, tok["--accent-ink"], got)
		}
	}
	// --accent-ink itself is used as ink on the ground, for the chip
	// border text and the branch name.
	if got := ratio(t, tok["--accent-ink"], tok["--ground"]); got < 4.5 {
		t.Errorf("--accent-ink on the ground is %.2f:1, under 4.5:1", got)
	}
}

// A bar is two blocks side by side; the fill has to be distinguishable from
// the track, which is the 3:1 non-text threshold.
func TestBarFillReadsAgainstItsTrack(t *testing.T) {
	tok := tokens(t)
	for _, fill := range []string{"--accent", "--alert"} {
		if got := ratio(t, tok[fill], tok["--track"]); got < 3 {
			t.Errorf("a %s bar on --track is %.2f:1, under 3:1", fill, got)
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
	tok := tokens(t)
	var b strings.Builder
	b.WriteString("\n token          ground  alt row  band\n")
	for _, name := range []string{"--text", "--secondary", "--muted", "--busy", "--idle", "--alert", "--dead", "--accent-ink"} {
		b.WriteString(fmt.Sprintf(" %-14s %5.2f   %5.2f   %5.2f\n", strings.TrimPrefix(name, "--"),
			ratio(t, tok[name], tok["--ground"]),
			ratio(t, tok[name], tok["--panel"]),
			ratio(t, tok[name], tok["--band"])))
	}
	t.Log(b.String())
}
