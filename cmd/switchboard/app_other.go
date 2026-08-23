//go:build !darwin || !cgo

package main

import "errors"

// The standalone window needs WKWebView, which needs cgo on macOS.
// Cross-compiled and CGO_ENABLED=0 builds (goreleaser) get this stub so
// the rest of the tool still builds everywhere.
func runApp(args []string) error {
	return errors.New("app window requires a cgo build on macOS; run: go build ./cmd/switchboard")
}
