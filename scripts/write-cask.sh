#!/bin/sh
# Print the Homebrew cask for a released Switchboard.app zip.
#
# Usage: scripts/write-cask.sh VERSION ZIP [notarized]
#
# The release workflow writes the output to Casks/switchboard.rb in the
# homebrew-tap repository. Pass "notarized" once the bundle is signed with a
# Developer ID and notarized; without it the cask tells people how to get
# past Gatekeeper on first launch.
set -eu

if [ $# -lt 2 ]; then
	echo "usage: $0 VERSION ZIP [notarized]" >&2
	exit 2
fi

VERSION="$1"
SHA256=$(shasum -a 256 "$2" | cut -d' ' -f1)
NOTARIZED="${3:-}"

cat <<EOF
cask "switchboard" do
  version "$VERSION"
  sha256 "$SHA256"

  url "https://github.com/adamsilverstein/claude-switchboard/releases/download/v#{version}/Switchboard-#{version}.zip"
  name "Switchboard"
  desc "Keyboard-driven picker for live Claude Code agents"
  homepage "https://github.com/adamsilverstein/claude-switchboard"

  depends_on macos: :big_sur

  app "Switchboard.app"
EOF

if [ "$NOTARIZED" != "notarized" ]; then
	cat <<'EOF'

  caveats <<~CAVEATS
    Switchboard.app is ad-hoc signed, not notarized, so macOS blocks the
    first launch. Allow it once with:

      xattr -dr com.apple.quarantine "#{appdir}/Switchboard.app"

    or open it, then choose Open Anyway in System Settings > Privacy & Security.
  CAVEATS
EOF
fi

echo "end"
