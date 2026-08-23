package main

import (
	_ "embed"
	"strings"
)

// The standalone app window is a WKWebView running xterm.js, with the
// ordinary switchboard TUI attached behind it on a pty. Vendoring the
// xterm.js dist files (MIT, see appassets/LICENSE.xterm) keeps the page
// fully self-contained: no network access, no node toolchain.
var (
	//go:embed appassets/xterm.js
	xtermJS string
	//go:embed appassets/xterm-addon-fit.js
	xtermFitJS string
	//go:embed appassets/xterm.css
	xtermCSS string
)

// appHTML assembles the single self-contained page shown in the app window.
//
// The page talks to Go through two bound functions (ptyInput, ptyResize)
// plus ptyReady, which signals that the terminal exists and buffered pty
// output can be flushed. Go pushes output by evaluating __ptyOut with a
// base64 chunk; bytes cross the bridge undecoded so multi-byte sequences
// split across chunks reassemble inside xterm.js.
func appHTML() string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
`)
	b.WriteString(xtermCSS)
	b.WriteString(`
html, body { margin: 0; padding: 0; height: 100%; background: #1d1f21; }
#term { position: absolute; inset: 0; padding: 6px; }
</style>
</head>
<body>
<div id="term"></div>
<script>
`)
	b.WriteString(xtermJS)
	b.WriteString("\n")
	b.WriteString(xtermFitJS)
	b.WriteString(`
const term = new Terminal({
	fontFamily: '"SF Mono", Menlo, Monaco, monospace',
	fontSize: 13,
	theme: { background: "#1d1f21" },
	macOptionIsMeta: true,
});
const fit = new FitAddon.FitAddon();
term.loadAddon(fit);
term.open(document.getElementById("term"));

const b64ToBytes = (b64) => Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
window.__ptyOut = (b64) => term.write(b64ToBytes(b64));

term.onData((d) => ptyInput(d));

const doFit = () => {
	fit.fit();
	ptyResize(term.cols, term.rows);
};
window.addEventListener("resize", doFit);
doFit();
ptyReady();
term.focus();
</script>
</body>
</html>
`)
	return b.String()
}
