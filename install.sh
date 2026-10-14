#!/bin/sh
# Build MacPanel and install it as a launchd agent for the current user.
# The agent auto-starts at login (RunAtLoad) and is kept alive (KeepAlive).
set -e

DIR=$(cd "$(dirname "$0")" && pwd)
cd "$DIR"

./build.sh

PLIST_DST="$HOME/Library/LaunchAgents/com.macpanel.plist"
sed -e "s|__REPO_DIR__|$DIR|g" com.macpanel.plist > "$PLIST_DST"

# Reload if already loaded; bootstrap otherwise
launchctl bootout gui/$(id -u)/com.macpanel 2>/dev/null || true
launchctl bootstrap gui/$(id -u) "$PLIST_DST"

echo "Installed: $PLIST_DST"
echo "Panel:     http://127.0.0.1:9000/"
echo "Data dir:  $DIR/data"
