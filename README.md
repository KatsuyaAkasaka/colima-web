# colima-web

`colima` CLI とコンテナランタイム（docker）をラップする、ローカル専用の Web サービス。
ブラウザから **colima インスタンスとコンテナ**をまとめて操作でき、macOS の `launchd` で
ログイン時に `localhost` で自動起動する。

```
ブラウザ ──REST / streaming──> colima-web (127.0.0.1:51900) ──exec──> colima / docker
```

## できること

| 対象 | 操作 |
|------|------|
| インスタンス（colima VM） | 一覧・状態表示 / 起動・停止・再起動 / 削除 / オプション指定で新規起動（CPU・メモリ・ディスク・arch・runtime・vm-type・kubernetes・mount） |
| コンテナ（docker） | 一覧表示 / 起動・停止・再起動・削除 / ログ表示 |
| イメージ（docker） | 一覧表示 / pull / 削除 |
| クリーンアップ | `docker system prune` / `colima prune` |

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

### 解除

`uninstall.sh` がインストール時に作成したものをすべて削除する:

- 自動起動の解除（`launchd` から登録解除）
- バイナリ（`~/.local/bin/colima-web`）の削除
- LaunchAgent 設定（`~/Library/LaunchAgents/com.colima-web.plist`）の削除
- デスクトップの 🐳「Colima Web」アイコンの削除

```sh
./uninstall.sh
```
