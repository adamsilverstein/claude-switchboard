#!/bin/sh
# Build Switchboard.app, a standalone macOS bundle around `switchboard app`.
#
# Usage: scripts/make-app.sh [destination-dir]
#
# The destination defaults to ~/Applications. The bundle gives the picker
# its own Dock icon, cmd-tab entry, and Spotlight/Launchpad presence, and
# lets macOS attribute the Automation (Apple Events) permission prompt to
# "Switchboard" instead of to whatever terminal launched it.
set -eu

cd "$(dirname "$0")/.."

DEST="${1:-$HOME/Applications}"
APP="$DEST/Switchboard.app"

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# The binary cannot be named "switchboard" here: the default APFS volume is
# case-insensitive, so it would collide with the "Switchboard" launcher.
go build -o "$APP/Contents/MacOS/switchboard-bin" ./cmd/switchboard

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

cat > "$APP/Contents/Info.plist" <<'EOF'
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
	<string>0.1.0</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSAppleEventsUsageDescription</key>
	<string>Switchboard focuses the terminal window running the agent you pick.</string>
</dict>
</plist>
EOF

# Ad-hoc sign so a locally built bundle launches cleanly on Apple Silicon.
codesign --force --deep --sign - "$APP" 2>/dev/null || true

echo "built $APP"
echo "launch it with: open '$APP'"
