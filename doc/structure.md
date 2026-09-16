# プロジェクト構造 (structure.md)

## 📁 フォルダ構成（リポジトリ全体・抜粋）

```text
ast-digger/                    # リポジトリルート
├── doc/                        # 公開仕様書・ドキュメント
│   ├── product.md
│   ├── tech.md
│   ├── structure.md
│   ├── requirements.md
│   ├── design.md
│   └── adr/                    # アーキテクチャ決定レコード
├── cmd/
│   └── ast-digger/
│       ├── main.go            # CLI エントリーポイント（cobra ルートコマンド登録）
│       ├── agentcontext.go    # agent-context: スキーマ動的導出
│       ├── output.go          # text/JSON出力共通ヘルパー
│       ├── exitcode.go        # 終了コード判定
│       └── truncate.go        # 出力切り詰め処理
├── internal/
│   ├── parser/                # 各言語用 AST 解析パーサー
│   │   ├── base.go            # Parser インターフェース、SymbolData/ReferenceData 型定義
│   │   ├── python.go          # Python 解析用パーサー
│   │   ├── typescript.go      # TypeScript 解析用パーサー
│   │   ├── golang.go          # Go 解析用パーサー（予約語との混同を避けるため golang.go と命名）
│   │   ├── java.go            # Java 解析用パーサー
│   │   ├── javascript.go      # JavaScript 解析用パーサー
│   │   ├── rust.go            # Rust 解析用パーサー
│   │   ├── markdown.go        # Markdown ドキュメント解析用パーサー
│   │   ├── sql.go             # SQL (Snowflake Dialect) 解析用パーサー
│   │   └── fuzzy.go           # シンボル候補提示ロジック
│   ├── db/
│   │   └── db.go              # SQLite キャッシュデータベース操作
│   ├── outline/
│   │   └── outline.go         # スケルトンコード抽出・ディレクトリツリー合成
│   └── apperr/
│       └── apperr.go          # カスタムエラー型・終了コードタクソノミー
├── testdata/                   # ユニットテスト用のサンプルコード（Python/TS/Go/Java/JS/Rust/MD/SQL）
├── README.md                  # ツール利用方法
├── Makefile                   # ビルドスクリプト
└── .gitignore                 # バージョン管理除外設定
```

## 🏷️ 命名規則
- **パッケージ名**: 全て小文字・単数形（`parser`, `db`, `outline`, `apperr`）。アンダースコアやキャメルケースは使用しない（Go標準規約）。
- **ファイル名**: `snake_case` は使用せず、Go標準の小文字連結（例: `python.go`, `typescript.go`, `golang.go`）とする。
- **exported識別子**（パッケージ外に公開する型・関数・メソッド）: `PascalCase`（例: `Parser`, `SymbolData`, `ParseFile`）。
- **unexported識別子**（パッケージ内部限定）: `camelCase`（例: `parseImports`, `symbolCache`）。
- **エラー変数/型**: `Err` 接頭辞の sentinel error（例: `ErrSymbolNotFound`）、またはカスタムエラー型は `XxxError` の形式（例: `FileAccessError`）とする。
- **インターフェース名**: 単一メソッドの場合は `-er` 接尾辞（Go標準の慣習、例: `Parser` は複数メソッドを持つため例外的に名詞形を許容）。
- **テストファイル**: `xxx_test.go`。テーブル駆動テストを基本形式とする。

## 🏗️ アーキテクチャの方針
- **モジュール分割と関心の分離**:
  - `internal/parser/`: tree-sitter の AST 解析に特化し、共通の `Parser` インターフェースを介して、各言語の構文木から統一されたシンボル構造を抽出する。
  - `internal/db/`: SQLite のテーブル操作および `mtime`/ハッシュによるキャッシュ差分スキャン処理を担当する。
  - `internal/outline/`: フォルダ構成やシンボル一覧のツリー表現、スケルトンコード生成を担当する。
  - `cmd/ast-digger/main.go`: CLIの入力パラメータのパース（cobra）、および各パッケージのオーケストレーション。
  - `internal/apperr/`: エラー型と終了コードのマッピングを一元管理する。
- **`internal/` パッケージの使用**: Go言語の `internal` ディレクトリ機構を活用し、外部から実装詳細への直接依存を防ぐ。外部公開が必要なAPIが生じた場合のみ `pkg/` の新設を検討する（現時点では不要）。

## 🛠️ インポートパターン
- モジュールルート（`go.mod` の `module` 宣言 `github.com/snaga/ast-digger`）を起点とした絶対インポートパスを使用する（例: `import "github.com/snaga/ast-digger/internal/parser"`）。

## 🔗 その他設計の決定事項
- **キャッシュDBファイルの扱い**: 本ツールはプロジェクトルートの `.workspace_cache/symbols.db` にキャッシュデータベースを作成する。接続の都度 `PRAGMA foreign_keys = ON;` を発行し、`database/sql.DB` コネクションプールでの物理接続を1本（`db.SetMaxOpenConns(1)`）に固定することで、CASCADE DELETE などの整合性を保証する。
- **feedback.jsonl の扱い**: エージェント動作時の問題点（フリクション）は `.workspace_cache/feedback.jsonl` にタイムスタンプと共に JSONL 形式で追記保存する。
- **`.gitignore`**: `ast-digger` などのビルド成果物や `.workspace_cache/` ディレクトリは除外対象とする。
