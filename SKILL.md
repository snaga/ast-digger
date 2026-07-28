# AST-Digger スキルマニフェスト (SKILL.md)

## 🎯 概要
AST-Digger を用いて大規模プロジェクトのコードをコンテキスト効率的に探索するためのプレイブック（スキルマニフェスト）です。AIエージェント（Claude Code / Codex / Antigravity 等）が本ツールを自律的に呼び出す際のガイドラインとして設計されています。

## 🛠️ 推奨ワークフロー (Best Practices)

0. **機能・スキーマの自己把握 (Self Introspection - 初回推奨)**:
   ツールの利用開始時や対応機能を確認したい場合は、まず `agent-context` コマンドを実行して最新のサブコマンド一覧、全引数・全オプションの機械可読なJSONスキーマを取得します。
   - **例**: `ast-digger agent-context`

1. **全体像のロード (Explore Directory)**:
   まずは `directory` コマンドを実行して、プロジェクトのファイル構成とどのようなシンボルが定義されているかを高速に把握します。
   - **例**: `ast-digger directory [dir_path] --json`

2. **ファイル構造の把握 (Outline File)**:
   対象のソースファイルの `outline` を実行して、クラスやメソッドのシグネチャ（スケルトン）のみをトークンを抑えて取得します。
   - **例**: `ast-digger outline [file_path] --json`

3. **事前位置特定とサイズ確認 (Locate Symbol - 強く推奨)**:
   取得したいシンボル（クラスやメソッド、あるいはMarkdownの見出しセクション・コードブロック）がどれだけ巨大であるか分からない場合、まず `locate` コマンドを実行して定義行数を確認します。
   - **例**: `ast-digger locate [file_path] [symbol_path] --json`
   - **重要**: 出力行数が300行を超える巨大シンボルの場合は、デフォルトで自動切り詰めが働くため、事前にサイズを確認した上で必要な部分のみ読み込むか、すべて読む必要がある場合のみ `--no-truncate` を付与して取得してください。

4. **コード・セクションのピンポイント抽出 (Extract Symbol)**:
   対象シンボルが特定され、かつ許容サイズ内であれば `symbol` コマンドでコード本体またはドキュメントの特定セクション／コードブロックを抽出します。
   - **コード抽出**: `ast-digger symbol [file_path] [symbol_path]`
   - **Markdownセクション抽出**: `ast-digger symbol doc.md "アーキテクチャ/新しい言語の追加"`
   - **Markdownコードブロック抽出**: `ast-digger symbol doc.md "アーキテクチャ/code_block:mermaid"` や `"code_block:go#2"`
   - **Frontmatter抽出**: `ast-digger symbol doc.md "frontmatter"`

5. **影響範囲・ドキュメントリンクの追跡 (Tracer & Link Resolver)**:
   修正による影響範囲やドキュメント間の依存関係を調査する場合は、`references`（呼び出し箇所・見出し参照一覧）や `resolve-import`（インポート解決・相対ドキュメントリンク解決）を組み合わせて探索します。
   - **Markdown相関リンク解決**: `ast-digger resolve-import doc.md "SPECS/design.md#architecture"`

6. **摩擦・問題点の記録 (Feedback Log)**:
   ツールの利用中にエラーや摩擦・不都合が発生した場合は、`feedback` コマンドを用いて記録を残します。
   - **例**: `ast-digger feedback "error or friction description"`

---

## 📝 Markdown ドキュメント探索のポイント (Markdown Playbook)

* **目次とコードブロック位置の即時検出**:
  `outline` コマンドで Markdown を読んだ際、文章本文は省略され、見出し階層（H1〜H6）および `<!-- [code_block:mermaid] L50-L75 -->` / `<!-- [frontmatter] -->` などの位置マーカーだけが返るため、トークンを一切無駄にせず目的の章やダイアグラムの場所を特定できます。
* **図やコードのみのピンポイント読込**:
  ドキュメント全体の長文テキストをロードすることなく、`symbol doc.md "章/code_block:mermaid"` のように特定の Mermaid 構成図やサンプルコードのみを切り出して理解できます。

---

## 🚫 避けるべきアンチパターン

* **巨大シンボル・巨大セクションの無計画な全取得**:
  `ast-digger symbol` で巨大なクラス全体や大規模セクション全体を `--no-truncate` で無計画に読み込むことは、LLMのコンテキストウィンドウを浪費し、推論の精度低下を招くため避けてください。
* **無制限の参照探索**:
  `references` コマンドを実行する際は、必要に応じて `--project-root` を絞り込んでください。無制限に巨大プロジェクトをスキャンすると処理に時間がかかる原因になります。

