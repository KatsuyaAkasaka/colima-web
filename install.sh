#!/usr/bin/env bash
# colima-web を ~/.local/bin に置き、ログイン時に自動起動する LaunchAgent を登録する。
set -euo pipefail

PORT="${PORT:-51900}"
LABEL="com.colima-web"
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$HOME/.local/bin"
BIN_PATH="$BIN_DIR/colima-web"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_DIR="$HOME/Library/Logs"

# colima の場所と、起動に必要なツール群を含む PATH を解決して plist に焼き込む。
COLIMA_BIN="$(command -v colima || true)"
if [ -z "$COLIMA_BIN" ]; then
  echo "error: colima が PATH 上に見つかりません。" >&2
  exit 1
fi

# colima は XDG_CONFIG_HOME によって設定/インスタンスの保存先を変える。
# launchd は対話シェルの環境を引き継がないため、設定時の値を plist に焼き込んで
# CLI と colima-web が同じインスタンスを見るようにする（未設定なら何も足さない）。
XDG_ENTRY=""
if [ -n "${XDG_CONFIG_HOME:-}" ]; then
  XDG_ENTRY="    <key>XDG_CONFIG_HOME</key><string>$XDG_CONFIG_HOME</string>"
fi

echo "==> ビルド"
( cd "$SRC_DIR" && go build -o "$SRC_DIR/colima-web" . )

echo "==> インストール: $BIN_PATH"
mkdir -p "$BIN_DIR"
# 稼働中バイナリを cp で上書きすると inode が再利用され、カーネルがキャッシュした
# コード署名と cdhash が食い違って launchd が OS_REASON_CODESIGNING で kill する。
# temp に置いてアドホック再署名し、mv で原子的に差し替える（=新 inode）ことで回避。
TMP_BIN="$BIN_DIR/.colima-web.new"
cp "$SRC_DIR/colima-web" "$TMP_BIN"
codesign --force --sign - "$TMP_BIN" 2>/dev/null || true
mv -f "$TMP_BIN" "$BIN_PATH"

echo "==> LaunchAgent を作成: $PLIST"
mkdir -p "$(dirname "$PLIST")" "$LOG_DIR"
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_PATH</string>
    <string>-addr</string>
    <string>127.0.0.1:$PORT</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>$PATH</string>
    <key>COLIMA_BIN</key><string>$COLIMA_BIN</string>
$XDG_ENTRY  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$LOG_DIR/$LABEL.log</string>
  <key>StandardErrorPath</key><string>$LOG_DIR/$LABEL.log</string>
</dict>
</plist>
EOF

echo "==> LaunchAgent を登録 (reload)"
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
launchctl enable "gui/$(id -u)/$LABEL"

# --- デスクトップランチャー（クジラアイコン付き Finder エイリアス）---
echo "==> デスクトップにランチャーを作成"
WEBLOC="$SRC_DIR/Colima Web.webloc"
ALIAS="$HOME/Desktop/Colima Web"

# .webloc 実体（URL は PORT に追従）を生成/更新
cat > "$WEBLOC" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>URL</key>
	<string>http://127.0.0.1:$PORT</string>
</dict>
</plist>
EOF

# 既存を消して Finder エイリアスを作成（unix symlink はカスタムアイコン不可のため alias を使う）
/bin/rm -f "$ALIAS"
osascript >/dev/null 2>&1 <<EOF || echo "  ! エイリアス作成に失敗（Finder自動化の許可が必要な場合あり）"
tell application "Finder"
  set a to make alias file to (POSIX file "$WEBLOC") at (POSIX file "$HOME/Desktop")
  set name of a to "Colima Web"
end tell
EOF

# クジラアイコンを設定（swiftc があれば seticon をビルドして適用。無ければ既定アイコンのまま）
if command -v swiftc >/dev/null 2>&1; then
  ( cd "$SRC_DIR" && swiftc seticon.swift -o seticon 2>/dev/null ) || true
  if [ -x "$SRC_DIR/seticon" ] && [ -e "$ALIAS" ]; then
    "$SRC_DIR/seticon" "🐳" "$ALIAS" >/dev/null 2>&1 || true
  fi
fi
killall Finder 2>/dev/null || true

echo
echo "完了: http://127.0.0.1:$PORT を開いてください。"
echo "デスクトップ: 「Colima Web」(🐳) をダブルクリックで起動"
echo "ログ: $LOG_DIR/$LABEL.log"
echo "停止/削除: ./uninstall.sh"
