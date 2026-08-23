#!/bin/sh
# Regenerate assets/AppIcon.icns from assets/icon.svg.
#
# Usage: scripts/make-icon.sh
#
# The .icns is committed, so this only needs to run when the artwork changes.
# Rendering needs either rsvg-convert (brew install librsvg) or a
# Chromium-based browser; iconutil and sips ship with macOS.
set -eu

cd "$(dirname "$0")/.."

SRC="assets/icon.svg"
OUT="assets/AppIcon.icns"
SET="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$SET"
MASTER="$SET/../master.png"

render_1024() {
	if command -v rsvg-convert >/dev/null 2>&1; then
		rsvg-convert -w 1024 -h 1024 -o "$MASTER" "$SRC"
		return
	fi
	for browser in \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
		"/Applications/Chromium.app/Contents/MacOS/Chromium" \
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" \
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"; do
		if [ -x "$browser" ]; then
			"$browser" --headless --disable-gpu --hide-scrollbars \
				--default-background-color=00000000 \
				--force-device-scale-factor=1 \
				--window-size=1024,1024 \
				--screenshot="$MASTER" "file://$PWD/$SRC" >/dev/null 2>&1
			[ -f "$MASTER" ] && return
		fi
	done
	echo "no SVG renderer found: install librsvg (brew install librsvg) or Google Chrome" >&2
	exit 1
}

render_1024

# The iconset names macOS expects; each size is downsampled from the 1024 master.
for spec in 16:16x16 32:16x16@2x 32:32x32 64:32x32@2x 128:128x128 256:128x128@2x \
	256:256x256 512:256x256@2x 512:512x512 1024:512x512@2x; do
	px="${spec%%:*}"
	name="${spec#*:}"
	sips -s format png -z "$px" "$px" "$MASTER" --out "$SET/icon_$name.png" >/dev/null
done

iconutil -c icns "$SET" -o "$OUT"
rm -rf "$(dirname "$SET")"
echo "built $OUT"
