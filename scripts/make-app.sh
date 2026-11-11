#!/bin/sh
# Build Switchboard.app, a standalone macOS bundle around `switchboard app`.
#
# Usage: scripts/make-app.sh [destination-dir]
#
# The destination defaults to ~/Applications. Three environment variables
# serve the release workflow and default to a local, native build:
#
#   VERSION        CFBundleShortVersionString (default: 0.0.0-dev)
#   ARCHS          space-separated GOARCH list; more than one is merged into
#                  a universal binary with lipo (default: the host's arch)
#   SIGN_IDENTITY  codesign identity (default: "-", an ad-hoc signature)
# The bundle gives the picker
# its own Dock icon, cmd-tab entry, and Spotlight/Launchpad presence, and
# lets macOS attribute the Automation (Apple Events) permission prompt to
# "Switchboard" instead of to whatever terminal launched it.
set -eu

cd "$(dirname "$0")/.."

DEST="${1:-$HOME/Applications}"
APP="$DEST/Switchboard.app"
VERSION="${VERSION:-0.0.0-dev}"
ARCHS="${ARCHS:-$(go env GOARCH)}"
SIGN_IDENTITY="${SIGN_IDENTITY:--}"

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# The binary cannot be named "switchboard" here: the default APFS volume is
# case-insensitive, so it would collide with the "Switchboard" launcher.
BIN="$APP/Contents/MacOS/switchboard-bin"

# The window needs cgo; Go passes the matching -arch to clang, so the other
# architecture cross-builds on the same Mac.
BUILD=$(mktemp -d)
trap 'rm -rf "$BUILD"' EXIT
for arch in $ARCHS; do
	CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" go build -trimpath \
		-ldflags "-s -w" \
		-o "$BUILD/switchboard-$arch" ./cmd/switchboard
done
# shellcheck disable=SC2086 # ARCHS is a word list by design.
set -- $ARCHS
if [ $# -gt 1 ]; then
	lipo -create -output "$BIN" "$BUILD"/switchboard-*
else
	cp "$BUILD/switchboard-$1" "$BIN"
fi

# The icon gives the bundle a Dock, cmd-tab, and Finder identity of its own.
# Regenerate it from assets/icon.svg with scripts/make-icon.sh.
cp assets/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"

# Info.plist cannot pass arguments to the executable, so the bundle entry
# point is a two-line launcher that runs the binary in app mode.
cat > "$APP/Contents/MacOS/Switchboard" <<'EOF'
#!/bin/sh
exec "$(dirname "$0")/switchboard-bin" app
EOF
chmod +x "$APP/Contents/MacOS/Switchboard"

cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>Switchboard</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleIdentifier</key>
	<string>com.adamsilverstein.switchboard</string>
	<key>CFBundleName</key>
	<string>Switchboard</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$VERSION</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSAppleEventsUsageDescription</key>
	<string>Switchboard focuses the terminal window running the agent you pick.</string>
</dict>
</plist>
EOF

# Ad-hoc signing is enough for a bundle to launch on Apple Silicon. A
# Developer ID signature also needs the hardened runtime and a secure
# timestamp before Apple will notarize it.
if [ "$SIGN_IDENTITY" = "-" ]; then
	codesign --force --deep --sign - "$APP" 2>/dev/null || true
else
	codesign --force --options runtime --timestamp \
		--sign "$SIGN_IDENTITY" "$BIN"
	codesign --force --options runtime --timestamp \
		--sign "$SIGN_IDENTITY" "$APP"
fi

echo "built $APP"
echo "launch it with: open '$APP'"
