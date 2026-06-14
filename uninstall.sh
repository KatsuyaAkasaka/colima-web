#!/usr/bin/env bash
# LaunchAgent を解除し、バイナリと plist を削除する。
set -euo pipefail
LABEL="com.colima-web"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
/bin/rm -f "$PLIST"
/bin/rm -f "$HOME/.local/bin/colima-web"
/bin/rm -f "$HOME/Desktop/Colima Web"
echo "アンインストール完了。"
