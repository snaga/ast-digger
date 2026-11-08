# テクノロジースタック (tech.md)

本ドキュメントは AST-Digger の技術スタックを定義する。

## 🛠️ プログラミング言語

### 1. 実装言語 (Implementation Language)
- **Go (1.22+)**: ツール本体、CLI、および AST 解析エンジンの実装に使用する。

### 2. ターゲット言語 (Target Language - 解析対象)
- **Python / TypeScript / Go / Java / JavaScript / Rust / Markdown / SQL**: 解析（アウトライン抽出、ピンポイント抽出、参照探索、CTE依存解析等）をサポートする主要な対象言語。
  - **SQL**: メイン方言として **Snowflake SQL**（CTE DAG、MERGE INTO、QUALIFY、LATERAL FLATTEN等）および標準SQL構文に対応する。
- **C/C++ / etc.**: 将来的な対応拡張ターゲット（各言語の tree-sitter パーサーを追加可能な設計とする）。

## 🏗️ フレームワーク & ライブラリ

| カテゴリ | ライブラリ候補 | 用途 / 選定理由 |
|:---|:---|:---|
| tree-sitter バインディング | `github.com/tree-sitter/go-tree-sitter` または `github.com/smacker/go-tree-sitter` | tree-sitter コア（C実装）への Go バインディング。**CGO 必須**。 |
| tree-sitter 文法 | `tree-sitter-python`, `tree-sitter-typescript`, `tree-sitter-go`, `tree-sitter-java`, `tree-sitter-javascript`, `tree-sitter-rust`, `tree-sitter-sql` の Go 向け文法パッケージ | 主要言語（コード/ドキュメント/SQL）をターゲットとして解析する。SQLはSnowflake/ANSI構文のCTEやサブクエリをサポートする。 |
| CLI フレームワーク | `github.com/spf13/cobra` | サブコマンド・フラグ定義・ヘルプ生成に対応。`agent-context` のスキーマをコマンド定義から動的導出する設計がcobraのコマンドツリーAPIに依存するため採用。 |
| SQLite ドライバ | `modernc.org/sqlite`（Pure Go, CGO不要） | キャッシュ・インデックス（シンボルデータベース）用。tree-sitter側は既にCGO必須だが、SQLite部分だけでもPure Go実装にしておくことで依存関係とビルドの複雑さを最小限に抑える。 |
| JSON | 標準ライブラリ `encoding/json` | `--json` 出力のシリアライズに使用。追加依存を避ける。 |
| MCP サーバー実装 | `github.com/mark3labs/mcp-go`（Pure Go） | Model Context Protocol (MCP) の stdio トランスポート対応 JSON-RPC サーバー実装。Zero-Dependency で単一バイナリに内包可能であり、型安全なツール定義（JSON Schema）とシリアライズを低オーバーヘッドで処理できるため採用（[ADR-0007](./adr/0007-builtin-mcp-server-and-tool-consolidation.md) 参照）。 |

## 🧪 テストツール
- **標準 `testing` パッケージ**: ユニットテスト・テーブル駆動テストの基盤。
- **`github.com/stretchr/testify`**（`assert` / `require`）: アサーションの可読性向上のため採用。
- **`go test -cover`**: カバレッジ計測。

## 🔧 開発ツール
- **`gofmt` / `goimports`**: 標準フォーマッタ。コミット前に必ず適用する。
- **`go vet`**: 静的解析による基本的な誤り検出。
- **`golangci-lint`**: CI・レビューでの品質ゲートとして使用する。
- **Go Modules (`go.mod` / `go.sum`)**: 依存関係管理。

## 📦 インフラ / プラットフォーム
- **マルチプラットフォーム対応（ネイティブビルド）**: Windows / macOS / Linux 向けのバイナリは、各プラットフォーム上での `go build` によるネイティブビルドで生成する。CGO依存を許容する方針決定に伴い、`GOOS`/`GOARCH` を切り替えたクロスコンパイルはスコープ外とする。
- **配置場所**: Go実装一式はリポジトリルート直下に配置する。

---

## ⚠️ CGO 依存に関する既知の制約

tree-sitter のコア実装は C 言語で書かれており、Go バインディングは **CGO を前提**とする。これは「単一バイナリ配布」「クロスコンパイルの容易さ」に対してトレードオフとなる。

### 論点A: シングルバイナリ自体は作れるか
- ネイティブビルド（ビルドホストと同じOS/アーキテクチャ向け）であれば、CGOを使っても `go build` は引き続き単一の実行ファイルを生成する。
- ただし **Linux(glibc環境) ではデフォルトで動的リンクになりやすい**: 何もフラグを指定せずにビルドすると、生成されたバイナリは実行環境の libc に依存する。真にゼロ依存の単一バイナリにするには `-ldflags '-linkmode external -extldflags "-static"'` の指定や musl libc 環境でのビルドが必要になる。
- **macOS は完全な静的リンクが不可**（Appleの制約でシステムライブラリの静的リンクができない）。

### 論点B: クロスコンパイルが可能か
- CGOを有効にすると、Go単体の魅力であるクロスコンパイル機構が機能しなくなる。CGOはCコードのコンパイルを要するため、ターゲットプラットフォーム向けのCクロスコンパイラが必要になる。
- 対策として `zig cc`（単一のzigバイナリが多数のターゲット向けクロスコンパイラとして機能する）を採用する方法もあるが、ビルドパイプラインの追加設定が必要になる。

### 対応方針（決定済み）

CGO依存を受け入れ、**クロスコンパイルはスコープ外とする**方針を採用する（[ADR-0005](../doc/adr/0005-accept-cgo-and-descope-cross-compilation.md) を正本とする）。

1. 各プラットフォーム（Windows/macOS/Linux）向けのバイナリは、そのプラットフォーム上でのネイティブビルド（`go build`）によって生成する。クロスコンパイラ整備は当面行わない。
2. 静的リンクのビルドフラグは `README.md`・ビルドスクリプトに明記し、配布用バイナリはデフォルトで（プラットフォームの制約が許す範囲で）静的リンクを試みる方針とする。
3. Pure-Go tree-sitter 実装への切り替えや、将来的なクロスコンパイル要件の再検討に備え、tree-sitter バインディングへの依存を `internal/parser` 内に閉じ込め、他レイヤーから直接参照しない設計とする。
