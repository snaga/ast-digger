# 🔍 AST-Digger

> **AI エージェントのためのピンポイント・コード解析 CLI**

AST-Digger は、AIエージェントが大規模なコードベースを効率的に探索するための構文解析・コード抽出ツールです。本ツールはリポジトリルート直下（`cmd/`, `internal/`等）に実装されています。詳細な設計判断の経緯は [`SPECS/design.md`](SPECS/design.md)・[`SPECS/tech.md`](SPECS/tech.md)・[`doc/adr/`](doc/adr/) 配下のADRを参照してください。

> 📎 全9サブコマンド（`outline` / `symbol` / `locate` / `references` / `resolve-import` / `directory` / `feedback` / `agent-context` / `change-impact`）が実装済みで、CLIとして一通り機能します。

---

## 📚 目次

- [概要](#概要)
- [対応言語](#対応言語)
- [インストール / ビルド](#インストール--ビルド)
- [クイックスタート](#クイックスタート)
- [コマンドリファレンス](#コマンドリファレンス)
- [AIエージェントの推奨ワークフロー](#aiエージェントの推奨ワークフロー)
- [キャッシュと差分スキャン](#キャッシュと差分スキャン)
- [ライセンス](#ライセンス)

---

## 概要

大規模コードベースをAIエージェントが探索する際、ファイル全体を読み込むとコンテキストウィンドウが無駄に消費され、推論の精度も低下します。AST-Digger は [tree-sitter](https://tree-sitter.github.io/) を使って AST（抽象構文木）を高速にパースし、エージェントが必要な情報だけをピンポイントかつ構造的に取り出せる CLI ツールです。シングルバイナリとして動作し、ピンポイント抽出・差分キャッシュ・上限制御付き出力を提供します。

## 対応言語

| 言語 | 拡張子 |
| :--- | :--- |
| Python | `.py` |
| TypeScript | `.ts`, `.tsx` |
| Go | `.go` |
| Java | `.java` |
| JavaScript | `.js`, `.jsx` |
| Rust | `.rs` |
| SQL | `.sql` |
| Markdown | `.md`, `.markdown` |

---



## インストール / ビルド

### 必要環境

- **Go 1.22 以上**
- **Cコンパイラ（gcc/clang 等）が必須です**。tree-sitter バインディング（`github.com/tree-sitter/go-tree-sitter` および各言語文法パッケージ）が CGO に依存しているため、`CGO_ENABLED=1` でのビルドには C コンパイラが必要です。CGO依存に関する詳細は [`SPECS/tech.md`](SPECS/tech.md) および [ADR-0005](doc/adr/0005-accept-cgo-and-descope-cross-compilation.md) を参照してください。

### ビルド手順

リポジトリルートを作業ディレクトリとして、以下を実行します。共通のビルドコマンドは全プラットフォームで同一です。

```sh
go build ./cmd/ast-digger
```

カレントディレクトリに `ast-digger`（Windowsでは `ast-digger.exe`）という実行ファイルが生成されます。ただし、この`go build`が成功するには、各OSで以下のようにCコンパイラを用意しておく必要があります。

#### Windows

MinGW-w64 系のGCCツールチェインが必要です。以下のいずれかで導入してください。

- [MSYS2](https://www.msys2.org/) を導入し、`pacman -S mingw-w64-ucrt-x86_64-gcc` 等で `gcc` をインストールする（MSYS2のUCRT64/MINGW64シェルの`bin`ディレクトリをPATHに追加）。
- [WinLibs](https://winlibs.com/) が配布するビルド済みMinGW-w64一式を展開し、`bin`ディレクトリをPATHに追加する。
- Chocolatey等のパッケージマネージャ経由で `mingw` パッケージを導入する（`choco install mingw`）。

`gcc --version` がPowerShell/コマンドプロンプトから実行できることを確認してからビルドしてください。

#### macOS

Xcode Command Line Tools（`clang`）を導入します。

```sh
xcode-select --install
```

#### Linux

ディストリビューション標準のCコンパイラパッケージを導入します（Debian/Ubuntu系の例）。

```sh
sudo apt-get install build-essential
```

RHEL/Fedora系では `sudo dnf groupinstall "Development Tools"` 等、ディストリビューションの慣習に従ってください。

### クロスコンパイルはスコープ外です

`GOOS`/`GOARCH` を切り替えたクロスコンパイル（例: Windows上で `GOOS=linux go build` を実行してLinux向けバイナリを生成する）は、本プロジェクトの方針として**行いません**。

CGOを有効にすると、ターゲットプラットフォーム向けのCクロスコンパイラの整備（`zig cc` 等）が別途必要になります。本プロジェクトはtree-sitterバインディングの利用（CGO依存）を優先し、クロスコンパイル対応の複雑化を避けるという方針決定を行いました。詳細は [`SPECS/tech.md`](SPECS/tech.md) および [ADR-0005](doc/adr/0005-accept-cgo-and-descope-cross-compilation.md) を参照してください。

各プラットフォーム向けのバイナリは、**そのプラットフォーム上でのネイティブビルド**によって個別に生成する運用とします。

### 静的リンクについて

要件I-1（[`SPECS/requirements.md`](SPECS/requirements.md)）により、Windows/Linuxでは生成バイナリが追加の共有ライブラリ配置なしに単独で動作することを必須とします。macOSはAppleのOS制約により完全な静的リンクができないため、システムライブラリへの動的リンクを許容し、可能な範囲での静的リンク（ベストエフォート）を目標とします。

静的リンクを促すビルドフラグの例（Windows）:

```sh
CGO_ENABLED=1 go build -ldflags '-linkmode external -extldflags "-static"' -o ast-digger.exe ./cmd/ast-digger
```

> ⚠️ Windows環境では出力先ファイルに `.\ast-digger.exe` のように明示的に指定して実行することを推奨します。

### バイナリサイズの削減（シンボル・デバッグ情報の削除）

配布用バイナリのファイルサイズを軽量化したい場合や、セキュリティソフトによる誤検知（False Positive）を回避したい場合は、`-ldflags "-s -w"` フラグを付与してビルドすることを推奨します。

```sh
go build -ldflags "-s -w" -o ast-digger.exe ./cmd/ast-digger
```

- `-s`: 記号表（シンボルテーブル）を削除
- `-w`: DWARF デバッグ情報を削除

> 💡 **削減効果の実測値**: 通常ビルド（約 22.8 MB）に対し、本フラグを付与することで **約 12.3 MB（約 46% 削減）** までバイナリサイズを大幅に軽量化できます。


### 実行

ビルドせずに直接実行する場合:

```sh
go run ./cmd/ast-digger --version
```

ビルド済みバイナリを実行する場合:

```sh
./ast-digger --version
```

---

## クイックスタート

以下は、実際にビルドしたバイナリを実行して得られた出力例です（対象は `testdata/python/` 配下のテスト用フィクスチャ）。

### ディレクトリ構造をシンボル付きで確認する

```sh
./ast-digger directory testdata/python/importproj
```

```
importproj/
  bad_caller.py
  main.py
  mathutils.py
    def add
    def subtract
    def multiply
    def divide
  __init__.py
    def init_func
  caller.py
  utils.py
    def util_func
```

### ファイルのスケルトン（骨格コード）を取得する

```sh
./ast-digger outline testdata/python/importproj/mathutils.py
```

```python
def add(a, b):
    """Add two numbers."""
    ...


def subtract(a, b):
    """Subtract b from a."""
    ...


def multiply(a, b):
    """Multiply two numbers."""
    ...


def divide(a, b):
    """Divide a by b."""
    ...
```

### 特定のシンボルを抽出する

```sh
./ast-digger symbol testdata/python/sample.py Greeter.shout
```

```python
@staticmethod
    def shout(message: str) -> str:
        """Shout a message."""
        return message.upper()
```

---

## コマンドリファレンス

共通フラグ `--json` / `-j` はすべてのコマンドに付与されており、指定するとJSON出力に切り替わります（`agent-context`は常にJSON出力）。

### `directory` — ディレクトリツリー + シンボル一覧

```sh
ast-digger directory [dir_path] [--recursive] [--no-recursive] [--limit N] [--json]
```

| オプション | デフォルト | 説明 |
| :--- | :--- | :--- |
| `dir_path` | `.` | スキャン対象ディレクトリ |
| `--recursive` | `true`（bool flag） | サブディレクトリを再帰的にスキャンする |
| `--no-recursive` | `false`（bool flag） | 指定すると`--recursive`の値に関わらずトップレベルのみをスキャンする |
| `--limit N` | `50` | 表示するファイル数の上限 |

`.workspace_cache/symbols.db`（SQLite）を活用し、変更されたファイルのみを差分スキャンするため**高速**です。

多言語混在プロジェクトの例（`testdata/mixed/references/`、Python/TypeScript/Go/Java/JavaScript混在）:

```sh
./ast-digger directory testdata/mixed/references
```

```
references/
  caller.go
    def useHelper
  caller.java
    class Caller
      def callSite
  caller.js
  caller.py
  caller.ts
  target.go
    def helperFn
  target.java
    class Target
      def helperFn
  target.js
    def helperFn
  target.py
    def helperFn
  target.ts
    def helperFn
```

JSON出力時は、テキストツリー全体が `{"result": "..."}` の1文字列としてラップされます:

```sh
./ast-digger directory testdata/python/importproj --json
```

```json
{
  "result": "importproj/\n  bad_caller.py\n  main.py\n  mathutils.py\n    def add\n    def subtract\n    def multiply\n    def divide\n  __init__.py\n    def init_func\n  caller.py\n  utils.py\n    def util_func"
}
```

---

### `outline` — スケルトンコード生成

```sh
ast-digger outline <file_path> [--json] [--no-truncate]
```

関数やメソッドの**実装ボディを省略**し、シグネチャ・docstring/doc comment・型情報だけを残したアウトラインコードを生成します。テキスト出力は300行を超えると自動的に切り詰められます（`--no-truncate`で無効化可能。`--json`は常に無切り詰め）。

```sh
# Python
./ast-digger outline testdata/python/importproj/mathutils.py

# TypeScript
./ast-digger outline testdata/typescript/sample.ts

# Go
./ast-digger outline testdata/go/sample.go

# Java
./ast-digger outline testdata/java/sample.java

# JavaScript（.jsxも同一文法でパース可能）
./ast-digger outline testdata/javascript/sample.jsx

# Rust
./ast-digger outline testdata/rust/sample.rs

# SQL (Snowflake SQL / ANSI SQL)
./ast-digger outline testdata/sql/snowflake_sample.sql

# Markdown
./ast-digger outline testdata/markdown/sample.md

# JSON 出力（シンボル構造データの配列）
./ast-digger outline testdata/python/importproj/mathutils.py --json
```

Snowflake SQL対象ファイルの出力例（CTE DAGスケルトン）:

```sql
WITH
raw_orders AS (L6-L27, depends_on: [source_db.lake.orders, source_db.lake.blacklisted_users]) /* ... */,
cleaned_orders AS (L29-L48, depends_on: [raw_orders]) /* ... */,
user_master AS (L50-L62, depends_on: [source_db.lake.users]) /* ... */,
joined_sales AS (L64-L86, depends_on: [cleaned_orders, user_master, source_db.lake.tier_bonuses]) /* ... */,
tax_calculation AS (L88-L102, depends_on: [joined_sales]) /* ... */

INSERT INTO marts.daily_sales_summary (
...
```

Go対象ファイルの出力例:

```go
package sample

import "fmt"

// TopLevelFunction doubles x.
func TopLevelFunction(x int) int { /* ... */ }

// Greeter is a struct responsible for greetings.
type Greeter struct {
	Prefix string
}

// Greet returns a greeting string using a pointer receiver.
func (g *Greeter) Greet(name string) string { /* ... */ }

// Shout returns an uppercased message using a value receiver.
func (g Greeter) Shout(message string) string { /* ... */ }

// Animal describes something that can speak.
type Animal interface {
	// Speak returns the sound the animal makes.
	Speak() string
}

// ... (以下、型エイリアス・named type・トップレベル関数が続く)
```

---

### `symbol` — ピンポイントコード抽出

```sh
ast-digger symbol <file_path> <symbol_path> [--json] [--no-truncate]
```

`symbol_path` はドット区切りで指定します（例: `ClassName.method_name`。Goの場合は `ReceiverType.MethodName`）。抽出コードが300行を超える場合はテキスト・JSONいずれの出力でも自動的に切り詰められます（`--no-truncate`で無効化可能）。

```sh
# メソッドを抽出
./ast-digger symbol testdata/python/sample.py Greeter.shout

# レシーバメソッドを抽出（Go）
./ast-digger symbol testdata/go/sample.go Greeter.Greet

# クラスメソッドを抽出（TypeScript）
./ast-digger symbol testdata/typescript/sample.ts Greeter.shout

# メソッドを抽出（Java。コンストラクタも kind: "method" として同様に扱われる）
./ast-digger symbol testdata/java/sample.java SampleClass.greet

# クラスメソッドを抽出（JavaScript）
./ast-digger symbol testdata/javascript/sample.js Greeter.shout

# メソッド/関数を抽出（Rust）
./ast-digger symbol testdata/rust/sample.rs Greeter.greet

# CTEブロックを抽出（SQL）
./ast-digger symbol testdata/sql/snowflake_sample.sql cleaned_orders

# サブクエリを添字またはエイリアスで抽出（SQL）
./ast-digger symbol testdata/sql/snowflake_sample.sql "joined_sales/sub_items"

# 上流CTE依存コードを一括自動追跡（SQL: --follow-imports）
./ast-digger symbol testdata/sql/snowflake_sample.sql tax_calculation --follow-imports

# セクション/コードブロックを抽出（Markdown）
./ast-digger symbol testdata/markdown/sample.md "Section 1"

# JSON 出力（メタデータ付き）
./ast-digger symbol testdata/python/sample.py Greeter.shout --json
```

JSON出力例:

```json
{
  "name": "shout",
  "kind": "method",
  "parent": "Greeter",
  "file_path": "testdata/python/sample.py",
  "start_line": 16,
  "end_line": 19,
  "signature": "@staticmethod\ndef shout(message: str) -> str",
  "docstring": "Shout a message.",
  "code": "@staticmethod\n    def shout(message: str) -> str:\n        \"\"\"Shout a message.\"\"\"\n        return message.upper()"
}
```

シンボルが見つからない場合は、類似するシンボル名候補が最大5件、終了コード4とともに提示されます:

```sh
./ast-digger symbol testdata/python/sample.py Greeter.shuot
```

```
Error: symbol not found: "Greeter.shuot" in testdata/python/sample.py
Did you mean?
  - Greeter.shout
  - Greeter
  - Greeter.__init__
```

---

### `locate` — シンボルの行範囲を特定

```sh
ast-digger locate <file_path> <symbol_path> [--json]
```

コードを読み込まずに、シンボルの**開始行・終了行だけ**を高速に返します。巨大なシンボルを`symbol`で取得する前に、あらかじめサイズを確認する用途に向いています。

```sh
./ast-digger locate testdata/python/sample.py Greeter.shout
# → testdata/python/sample.py:16-19

./ast-digger locate testdata/python/sample.py Greeter.shout --json
# → {"file": "testdata/python/sample.py", "symbol": "shout", "start_line": 16, "end_line": 19}

# SQL: CTEおよびサブクエリの行範囲特定
./ast-digger locate testdata/sql/snowflake_sample.sql cleaned_orders
# → testdata/sql/snowflake_sample.sql:29-48

./ast-digger locate testdata/sql/snowflake_sample.sql "joined_sales/sub_items"
# → testdata/sql/snowflake_sample.sql:80-84
```

---

### `references` — プロジェクト横断の参照探索

```sh
ast-digger references <symbol_name> [--project-root DIR] [--limit N] [--json]
```

`--project-root`（デフォルト`.`）配下の全対応ファイルから指定シンボルが**参照・呼び出されている箇所**を探索します。コメントや文字列リテラル内の出現は除外されます。件数が`--limit`（デフォルト50）を超えると切り詰められ、超過分を絞り込むためのヒントがstderrに出力されます（JSON出力時は`truncated`/`total_count`/`limit`/`hint`キーとして返却）。

```sh
./ast-digger references target_func --project-root testdata/python/references
```

```
testdata/python/references/caller_a.py:2: target_func()  # actual reference on this line
testdata/python/references/caller_b.py:6: value = target_func
```

Snowflake SQL での重複テーブルスキャン探索（Icepick SNOW-005 ルール連携）の例:

```sh
./ast-digger references raw_orders --project-root testdata/sql
```

上限超過時のヒント表示の例:

```sh
./ast-digger references bulk_symbol --project-root testdata/python/references_bulk --limit 5
```

```
testdata/python/references_bulk/callers.py:2: bulk_symbol()  # call site 0
testdata/python/references_bulk/callers.py:3: bulk_symbol()  # call site 1
testdata/python/references_bulk/callers.py:4: bulk_symbol()  # call site 2
testdata/python/references_bulk/callers.py:5: bulk_symbol()  # call site 3
testdata/python/references_bulk/callers.py:6: bulk_symbol()  # call site 4
# ... (truncated: 5 of 60 results shown. Use --limit to increase or narrow down using --project-root)
```

> **注意（Go/Java対象コード固有の挙動）**: GoおよびJava版パーサーの`FindReferencesInFile`は、定義箇所自体を参照結果から除外しません。そのため、`.go`/`.java`ファイルを対象に`references`を実行すると、呼び出し箇所に加えて定義行も含まれます（Python/TypeScript/JavaScript/Rust/SQLの各パーサーは定義箇所を除外します）。

---

### `resolve-import` — インポート定義元の解決

```sh
ast-digger resolve-import <file_path> <symbol_name> [--json]
```

対象ファイルのインポート文（またはSQL内のCTE定義）を解析し、指定シンボルの**定義元ファイルと行番号**を解決します。

インポートはカレントディレクトリをプロジェクトルート（Pythonでいう`sys.path[0]`相当）とみなして解決するため、モジュール参照を含むファイルを対象にする場合は、そのファイルが属するプロジェクトルート（例えば`go.mod`やパッケージの起点ディレクトリ）をカレントディレクトリにして実行してください。

Javaの場合、プロジェクトルート配下に`src/main/java`（Maven/Gradleの慣習的なソースルート）が存在すればそちらを優先し、なければプロジェクトルート自体をソースルートとして解決します。単一インポート（`import com.example.Foo;`）とワイルドカードインポート（`import com.example.*;`）の両方に対応します。

```sh
cd testdata/python/importproj
../../../ast-digger resolve-import main.py add
# → mathutils.py:1

../../../ast-digger resolve-import main.py add --json
# → {"file_path": "mathutils.py", "start_line": 1}
```

```sh
# Java（プロジェクトルート = testdata/java/importproj、src/main/java をソースルートとして解決）
cd testdata/java/importproj
../../../ast-digger resolve-import src/main/java/com/example/app/Main.java MathUtils
# → .../testdata/java/importproj/src/main/java/com/example/util/MathUtils.java:3

# JavaScript
cd ../../../testdata/javascript/importproj
../../../ast-digger resolve-import main.js add
# → mathutils.js:1

# Rust
cd ../../../testdata/rust/importproj
../../../ast-digger resolve-import main.rs add
# → utils.rs:1

# SQL（同一ファイル内のCTE定義行番号解決）
./ast-digger resolve-import testdata/sql/snowflake_sample.sql cleaned_orders
# → snowflake_sample.sql:29

# Markdown（リンク/アンカーの参照先解決）
cd ../../../testdata/markdown/references
../../../ast-digger resolve-import ref.md "#section-2"
# → ref.md:3
```

---

### `feedback` — フィードバックの記録

```sh
ast-digger feedback <message> [--json]
```

AI エージェントがツール利用中に発生した問題や摩擦を `.workspace_cache/feedback.jsonl` にタイムスタンプ付きで記録します。

```sh
./ast-digger feedback "outline command was helpful"
# → Feedback recorded successfully.

./ast-digger feedback "outline command was helpful" --json
```

```json
{
  "message": "Feedback recorded successfully."
}
```

---

### `agent-context` — CLI スキーマの取得

```sh
ast-digger agent-context
```

登録済みの全コマンド・引数・オプションのスキーマを、常にJSONで出力します。AI エージェントがツール利用を開始する前に実行することで、正確な呼び出し方を自動学習できます。

出力の抜粋:

```json
"directory": {
  "description": "Print directory tree structure populated with incremental cached symbol outline.",
  "arguments": [
    { "name": "dir_path", "type": "string", "required": false, "default": "." }
  ],
  "options": [
    { "name": "--limit", "type": "integer", "default": "50", "description": "Max files to display in the output to avoid token bloating." },
    { "name": "--no-recursive", "type": "boolean", "default": "false", "description": "Scan only the top level of the directory tree; overrides --recursive." },
    { "name": "--recursive", "type": "boolean", "default": "true", "description": "Scan recursively down the directory tree (pass --recursive=false or --no-recursive to scan only the top level)." },
    { "name": "--json", "type": "boolean", "default": "false", "description": "Output structure in JSON format" }
  ]
}
```

---

### `change-impact` — 意味的変更インパクト解析・意図とドキュメント充足度評価

```sh
ast-digger change-impact [--base REF] [--max-statements N] [--require-doc SCOPE] [--fail-on-missing-doc] [--json]
```

Git差分（デフォルト `HEAD` との比較、未ステージング・ステージング済みを含む）をAST解析し、構文ステートメント（文・式・宣言）の増減、循環複雑度（Cyclomatic Complexity）の差分、影響シンボル一覧、およびDocstring充足度を算出します。さらに、モジュール内構造変化や複数モジュール横断変更を検知した場合に、外部ドキュメント（モジュール概要やADRなど）の同期を促すアドバイザリー（`documentation_advisory`）を生成します。

#### ガードレール検証機能
- `--max-statements <int>`: 追加ステートメント数が上限を超過した場合、終了コード 2 で失敗します（0 = 無制限）。
- `--fail-on-missing-doc`: 必須Docstring（`--require-doc` で指定された `exported`, `all`, `none`）が欠落している場合、終了コード 2 で失敗します。

```sh
# 変更インパクトサマリーをテキスト表示
./ast-digger change-impact

# ガードレール付き実行（50ステートメント以内、公開APIのDocstring必須）
./ast-digger change-impact --max-statements 50 --require-doc exported --fail-on-missing-doc
```

JSON出力例（`--json`）:

```json
{
  "summary": {
    "impact_level": "MODERATE",
    "statements_added": 42,
    "statements_deleted": 2,
    "complexity_delta": 3,
    "symbols_added": 1,
    "symbols_modified": 2,
    "symbols_deleted": 0,
    "files_changed": 2,
    "modules_affected": [
      "internal/parser"
    ]
  },
  "symbols": [
    {
      "file": "internal/parser/sql.go",
      "symbol_path": "extractCteQueryBody",
      "kind": "function",
      "visibility": "private",
      "change_type": "added",
      "statements_delta": 28,
      "has_docstring": false
    }
  ],
  "audit": {
    "public_api_doc_coverage": 1.0,
    "private_api_doc_coverage": 0.5,
    "missing_docstrings": [
      "internal/parser/sql.go::extractCteQueryBody"
    ]
  },
  "documentation_advisory": {
    "requires_intent_review": true,
    "reason": "STRUCTURAL_CHANGE_IN_MODULE",
    "affected_scopes": [
      {
        "scope": "internal/parser",
        "category": "module_level",
        "description": "New types or private helper parsing logic were introduced.",
        "prompt_action": "Verify if module-level documentation (such as overview docs, specifications, or architectural guides) should be created or updated to preserve design intent."
      }
    ]
  }
}
```

---

## AIエージェントの推奨ワークフロー

推奨される基本的な探索フロー（ルートの [`SKILL.md`](SKILL.md) で定義済み）は以下の通りです：

```
1. directory     → プロジェクト全体のシンボル構成を俯瞰（--json推奨）
2. outline       → 対象ファイルのスケルトンで構造を把握（--json推奨）
3. locate        → 目的のシンボルの行範囲・サイズを事前確認
4. symbol        → サイズを確認した上でピンポイントに実装コードを抽出
5. references    → 変更の影響範囲をプロジェクト横断で確認（--project-rootで絞り込み推奨）
6. change-impact → タスク実装後の変更規模・AST複雑度・Docstring充足度・外部ドキュメント同期アドバイザリーを自己監査（ガードレール検証）
```

### 避けるべきアンチパターン

- **巨大シンボルの無計画な全取得**: `symbol`コマンドを`--no-truncate`付きで無計画に読み込むと、LLMのコンテキストウィンドウを浪費し推論精度の低下を招きます。まず`locate`でサイズを確認してください。
- **無制限の参照探索**: `references`コマンドは`--project-root`で対象を絞り込まずに巨大プロジェクト全体をスキャンすると時間がかかります。

---

## キャッシュと差分スキャン

`directory` コマンドはプロジェクトルートの `.workspace_cache/symbols.db`（SQLite）にシンボルインデックスをキャッシュし、`feedback` コマンドは同じく `.workspace_cache/feedback.jsonl` にフィードバックを追記します。

- **初回実行**: 全ファイルをスキャンしてキャッシュを構築
- **2回目以降**: `mtime` と `SHA-256 ハッシュ`で変更を検知し、変更ファイルのみ再スキャン
- `node_modules`, `.git`, `.venv`, `__pycache__` は自動的に除外

## 📜 更新履歴

### 0.3.1 (2026-10-21)

- **新コマンド `change-impact` (意味的変更インパクト解析・意図とドキュメント充足度評価)**:
  - Git差分（作業ツリー/指定base）に対して、物理行数ではなく AST ステートメント純増減および循環複雑度差分（Cyclomatic Complexity Delta）を言語横断（Go, Python, TypeScript, Rust, Java, SQL）で計測。
  - 変更行とシンボルの突合による変更シンボル一覧の特定（追加・変更・削除）。
  - 可視性（Public/Private）に応じた Docstring 充足率の監査および欠落シンボルの特定（削除シンボルの除外対応）。
  - モジュール内の大規模構造変化や複数モジュール横断変更を検知し、設計意図を維持するための外部ドキュメントや ADR の更新・作成を促す汎用アドバイザリー（`documentation_advisory`）を出力。
  - ガードレール検証機能（`--max-statements <int>`, `--fail-on-missing-doc`）を実装。違反時でもエージェントが自律修正できるようレポートを標準出力に先行出力した上で終了コード 2 を返却。
  - 機械可読な `--json` フォーマット出力に対応。
- **パーサー基盤の共通拡張**:
  - `internal/parser.Parser` インターフェースに `ParseBytes` を追加し、一時ディスク I/O ゼロでメモリ上のソースコードバイト列を直接 Tree-sitter で AST パース可能に拡張。
- **アーキテクチャ決定レコード (ADR)**:
  - `doc/adr/0006-semantic-change-impact-and-intent-verification.md` を追加。

### 0.3.0 (2026-09-21)

- **Snowflake SQL 対応**:
  - `tree-sitter-sql` を導入し、Snowflake 方言（`QUALIFY`、`MERGE INTO`、`LATERAL FLATTEN` 等）に耐性のある SQL パーサーを実装。
  - CTE（`WITH cte AS`）およびビュー（`CREATE [SECURE|MATERIALIZED] VIEW`）、動的テーブル（`CREATE DYNAMIC TABLE`）のシンボル抽出と階層把握に対応。
  - サブクエリ添字（`cte/subquery[0]`）およびエイリアス名（`cte/alias`）によるピンポイント抽出に対応（CTE 外出しリファクタリング支援）。
  - テーブル・上流 CTE のデータフロー依存関係（`depends_on`）の抽出と、数千行のクエリを圧縮するスケルトン出力（`-- CTE Outline`）をサポート。
  - 上流 CTE 依存自動追跡（`--follow-imports`）および重複テーブルスキャン・シンボル参照探索（`references`）を実装（`icepick` 連携対応）。
  - 同一ファイルに対する方言警告の重複出力を抑制。
- **CLI / スキーマ統合**:
  - `agent-context` に SQL（`.sql`）を登録（計7言語、9拡張子に対応）。
  - 全 CLI コマンドの SQL 統合テストを追加。

---

## ライセンス

Apache License 2.0
