# colima-web

`colima` CLI とコンテナランタイム（docker）をラップする、ローカル専用の Web サービス。
ブラウザから **colima インスタンスとコンテナ**をまとめて操作でき、macOS の `launchd` で
ログイン時に `localhost` で自動起動する。

```
ブラウザ ──REST / streaming──> colima-web (127.0.0.1:51900) ──exec──> colima / docker
```

## 特徴

- **単一バイナリ (Go)** — フロントエンド (HTML/CSS/JS) は `go:embed` で同梱。依存ランタイム不要。
- colima / docker は再実装せず、**既存バイナリを `os/exec` で実行**するだけ（バージョン追従が不要）。
- バックエンドは **`127.0.0.1` のみで待ち受け** — 任意コマンド実行の踏み台にしないため外部公開しない。
- 入力は**許可リスト方式**で検証してからコマンド化（コマンドインジェクション対策）。
  - start オプションは既知フィールドのみ、プロファイル名は `[A-Za-z0-9_-]`、コンテナIDは `[A-Za-z0-9_.-]`。
- 時間のかかる操作（start / delete / logs 等）は **stdout/stderr を逐次ストリーム返却**し、UI のログペインに流す。

## 機能

### インスタンス（colima VM）
| 操作 | UI | 対応 CLI |
|------|-----|----------|
| 一覧 / 状態（5秒ごと更新） | テーブル表示 | `colima list --json` |
| プロファイル選択 | **行クリックで選択**（● ハイライト）。選んだ VM のコンテナを下に表示 | — |
| 削除 | 各行右の 🗑（確認ダイアログ付き） | `colima delete -f <profile>` |
| 起動 | Start フォーム | `colima start [profile] --cpu/--memory/--disk/--arch/--runtime/--vm-type/--kubernetes/--mount` |
| 停止 / 再起動 | ボタン | `colima stop` / `colima restart` |

### コンテナ（選択中インスタンスの docker）
| 操作 | UI | 対応 CLI |
|------|-----|----------|
| 一覧（name/state/image/ports、5秒ごと更新） | テーブル表示 | `docker --context colima[-<profile>] ps -a` |
| 起動 / 停止 / 再起動 / 削除 | 各行のボタン | `docker --context … start\|stop\|restart\|rm -f <id>` |
| ログ表示 | Logs ボタン | `docker --context … logs --tail 500 --timestamps <id>` |

### その他
- colima バージョンをヘッダに表示（`colima version`）。

## HTTP API

| メソッド / パス | 内容 |
|----------------|------|
| `GET /` | UI（埋め込み HTML） |
| `GET /api/instances` | `colima list --json` を配列で返す |
| `GET /api/version` | `colima version` |
| `POST /api/action` | `{action: start\|stop\|restart\|delete, profile, config}` を実行しログをストリーム返却 |
| `GET /api/containers?profile=` | 指定プロファイルの docker コンテキストで `ps -a` |
| `POST /api/container` | `{action: start\|stop\|restart\|remove, profile, id}` を実行しストリーム返却 |
| `GET /api/container/logs?profile=&id=` | コンテナログ（末尾500行）をストリーム返却 |

## 使い方

事前に [colima](https://github.com/abiosoft/colima) がインストール済みであること（`colima` が PATH 上にあればOK）。

```sh
git clone git@github.com:KatsuyaAkasaka/colima-web.git
cd colima-web
./install.sh                 # ポート変更は PORT=8080 ./install.sh
```

これだけで完了する。`install.sh` が以下を自動で行う:

1. バイナリをビルドして `~/.local/bin/colima-web` に配置
2. `launchd` に登録 → **ログイン時に自動起動**（落ちても `KeepAlive` で復帰）
3. **デスクトップに 🐳「Colima Web」アイコン**を作成（ダブルクリックで Web UI を開く）

完了後、デスクトップの 🐳 をダブルクリックするか `http://127.0.0.1:51900` を開く。

| 生成物 | 内容 |
|--------|------|
| `~/.local/bin/colima-web` | 実行バイナリ（temp→再署名→mv で配置） |
| `~/Library/LaunchAgents/com.colima-web.plist` | ログイン時自動起動（`RunAtLoad`/`KeepAlive`） |
| `~/Desktop/Colima Web` | 🐳 アイコン付き **Finder エイリアス**（実体は `Colima Web.webloc`） |

- デスクトップは `unix symlink` ではなく **Finder エイリアス**（symlink はカスタムアイコン不可のため）。
- アイコン適用には `swiftc`（Apple 標準）を使用。無い環境では既定アイコンのまま。
- 初回は `osascript` による Finder 操作で「自動化」の許可を求められることがある（許可後に再実行）。

### 解除
```sh
./uninstall.sh               # バイナリ・plist・デスクトップエイリアスを削除
```

### ログ
```
~/Library/Logs/com.colima-web.log
```

## 設定ディレクトリ（XDG_CONFIG_HOME）

colima は設定/インスタンスの保存先を環境で切り替える:

- `~/.colima` が**存在すればそれを優先**（`$XDG_CONFIG_HOME` を無視）。
- 無ければ `$XDG_CONFIG_HOME/colima`（未設定なら `~/.config/colima`）。

`launchd` は対話シェルの環境を引き継がないため、`install.sh` は **インストール時の
`XDG_CONFIG_HOME` を plist に焼き込む**。これがないと colima-web 側の colima が
別の設定ディレクトリ（典型的には空の `~/.colima`）を作成・参照し、CLI と Web で
見えるインスタンスが食い違う。

> ターミナルの `colima list` が "No instance found" になり、かつ空の `~/.colima` が
> ある場合は、それを削除すると `$XDG_CONFIG_HOME/colima` 側の実体が再び見えるようになる。

## バイナリ更新と code signing（macOS）

稼働中のバイナリを `cp` で**上書き**すると inode が再利用され、カーネルがキャッシュした
コード署名と cdhash が食い違って launchd がプロセスを `OS_REASON_CODESIGNING` で
kill し続ける（直接実行は通るのに常駐だけ落ちる、という症状になる）。

`install.sh` はこれを避けるため、**一時ファイルに置いてアドホック再署名し、`mv` で
原子的に差し替える（= 新しい inode）**。手動で差し替えた場合に同症状が出たら:

```sh
codesign --force --sign - ~/.local/bin/colima-web
launchctl kickstart -k gui/$(id -u)/com.colima-web
```

## セキュリティ上の注意

- これは**ローカルでシェル（colima/docker）を実行するサービス**。`127.0.0.1` 固定で待ち受け、
  外部公開はしない。`0.0.0.0` で公開すると任意コンテナ操作の踏み台になり得る。
- 入力は許可リストで検証するが、複数ユーザー環境やリモート利用を想定する場合は
  トークン認証などの追加が必要。

## 今後の拡張余地

- `kubernetes` / `template` / `prune` のUI化
- イメージ一覧、`docker exec`（xterm.js + WebSocket でターミナル）
- トークン認証
- containerd ランタイム（`colima nerdctl`）でのコンテナ一覧対応
