package appui

import (
	"embed"
	"encoding/base64"
	"strings"
	"sync"
)

// The page is one self-contained document with no network access of any
// kind: the stylesheet, the script and the three typefaces are all embedded
// in the binary. Vendoring the fonts costs 144KB and buys a window that
// looks the same on a laptop with no connection as on one with.
//
// Barlow, Barlow Condensed and JetBrains Mono are all SIL Open Font License
// 1.1; see assets/fonts/LICENSE.
//
//go:embed assets/console.html assets/console.css assets/console.js assets/fonts/*.woff2
var assets embed.FS

// faces maps an embedded font file to the family and weight it provides.
var faces = []struct{ file, family, weight string }{
	{"Barlow-400.woff2", "Barlow", "400"},
	{"Barlow-500.woff2", "Barlow", "500"},
	{"Barlow-600.woff2", "Barlow", "600"},
	{"BarlowCondensed-600.woff2", "Barlow Condensed", "600"},
	{"BarlowCondensed-700.woff2", "Barlow Condensed", "700"},
	{"JetBrainsMono-400.woff2", "JetBrains Mono", "400"},
	{"JetBrainsMono-500.woff2", "JetBrains Mono", "500"},
}

var (
	pageOnce sync.Once
	page     string
)

// Page returns the app window's document. It is built once and reused: the
// fonts alone are around 190KB of base64, and rebuilding that per launch
// would be pure waste.
func Page() string {
	pageOnce.Do(func() { page = build() })
	return page
}

func build() string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>Switchboard</title>\n<style>\n")
	b.WriteString(fontFaces())
	b.Write(mustRead("assets/console.css"))
	b.WriteString("\n</style>\n</head>\n<body>\n")
	b.Write(mustRead("assets/console.html"))
	b.WriteString("\n<script>\n")
	b.Write(mustRead("assets/console.js"))
	b.WriteString("\n</script>\n</body>\n</html>\n")
	return b.String()
}

func fontFaces() string {
	var b strings.Builder
	for _, f := range faces {
		raw := mustRead("assets/fonts/" + f.file)
		b.WriteString("@font-face{font-family:'" + f.family + "';font-style:normal;font-weight:" + f.weight)
		b.WriteString(";font-display:block;src:url(data:font/woff2;base64,")
		b.WriteString(base64.StdEncoding.EncodeToString(raw))
		b.WriteString(") format('woff2')}\n")
	}
	return b.String()
}

// mustRead panics on a missing asset. These are compiled into the binary by
// go:embed, so a failure here means the build is broken, not the machine.
func mustRead(name string) []byte {
	raw, err := assets.ReadFile(name)
	if err != nil {
		panic("appui: " + err.Error())
	}
	return raw
}
