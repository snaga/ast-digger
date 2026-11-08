# 0007. 単一バイナリ内包型MCPサーバーの導入と4大コアツール集約によるエージェントハーネス設計

* **ステータス**: 承認 (Accepted)
* **日付**: 2026-11-08

## コンテキスト (Context)

AIコーディングエージェント（Antigravity、Claude Code等）による大規模コードベース改修において、システムプロンプトやスキル（`.agents/skills/`）で「コード探索には `ast-digger` を使え」と指示しても、実際には素朴な `read_file` や `ripgrep` に退行（フォールバック）してしまう認知力学的問題が確認された。

この原因は主に以下の4点にある：
1. **アフォーダンスの次元差（1級市民 vs 2次間接参照）**: `read_file` は直接のツール呼び出し（1級市民）であるのに対し、CLIは `run_command` の文字列組み立てを介する「2段階の間接参照」であり認知的摩擦が大きい。
2. **事前学習の強烈な重力**: 事前学習コーパス内の無数の `cat` や `read_file` パターンが、プロンプト指示の引力を圧倒する。
3. **遅延ロードの盲点**: 「ファイルを読む」という日常的行動に対してエージェントがスキルの必要性を認識しない。
4. **コンテキスト希釈**: 長大なプロンプト内でCLI利用指示への Attention が低下する。

これに対し、Model Context Protocol（MCP）化によってツール定義ブロックに型安全に常駐させ、1級市民として直接発火可能にすることが極めて有効である。
しかし、現行のCLIサブコマンド（全8種）を1対1で漫然とMCPツールとして公開すると、ツール選定の迷走（Tool Selection Degradation）や、手軽な `read_file` への逃避（Tool Competition）という新たな罠に直面する。また、外部ラッパー（Node.jsや別バイナリ）を作るか、Go本体バイナリに内包するかというアーキテクチャ上の決定が必要であった。

## 決定 (Decision)

以下の通り、**単一バイナリ内包型のMCPサーバー実装**および**エージェントのメンタルモデルに即した4大コアツール集約**を採用する。

1. **単一バイナリ内包型MCPサーバー (`ast-digger mcp`) の採用**:
   - 外部ランタイム（Node.js / Python）や別バイナリを一切要求せず、既存の `ast-digger` Goバイナリ内に `mcp` サブコマンド（stdio JSON-RPCハンドラ）を実装する。
   - インプロセスで `internal/parser`, `internal/outline`, `internal/db` などのコアロジックを直接呼び出すことで、プロセス生成オーバーヘッドを排除しミリ秒単位の高速応答を実現する。
   - Go製MCPライブラリ（`github.com/mark3labs/mcp-go` 等）を採用し、堅牢なJSON Schema出力とメッセージングを担保する。

2. **エージェント指向の4大コアツールへの統合・集約**:
   - 既存の8コマンドをそのまま露出させず、エージェントの開発ワークフローに適合した以下の4ツールに集約する：
     - **`ast_explore`**（構造把握）: `directory` と `outline` を統合。パス種別（ディレクトリ/ファイル）に応じて階層ツリーまたはASTアウトラインを返却。
     - **`ast_symbol`**（局所抽出）: `symbol` と `locate` を統合。シンボルの実装コード取得と編集用の行範囲特定（`locate_only`）を単一インターフェースで提供。
     - **`ast_trace`**（依存追跡）: `resolve-import` と `references` を統合。順引き（定義元ジャンプ）と逆引き（プロジェクト全体での被参照検索）を `action` 引数で切り替え。
     - **`ast_audit`**（品質検証）: `change-impact` を直接MCP化。意味的変更規模・循環複雑度差分およびDocstring充足度を検証。

3. **Description Hacking による負のアフォーダンス設計**:
   - `read_file` への安易な逃避を抑止するため、`ast_explore` や `ast_symbol` の Tool Description に「CRITICAL: Call this tool FIRST before calling read_file」といった強い制約テキストを含め、Attention を強制的に惹きつける。

## 影響 (Consequences)

### メリット (Positive)
- **Zero-Dependency の維持**: ユーザー環境に Node.js 等を追加インストールさせることなく、`ast-digger` 単一バイナリの配布だけでエージェント環境（Claude Desktop、Antigravity、Cursor 等）の MCP サーバーとして即座に導入可能。
- **パフォーマンスとリソース効率**: プロセス起動コストとJSONの再シリアライズコストが完全にゼロになり、レスポンス速度が極めて高速。
- **認知的負荷の最小化**: ツール数が4個に厳選されるため、LLMがツール選定に迷わず、トークン消費と推論エラーを最小化できる。
- **SDDへの決定論的組み込み**: `doc_internal/tasks.md` 内のタスクToDoとして4ツールの呼び出しを直接指定できるようになり、エージェントの実装規約遵守率が飛躍的に向上する。

### デメリット / リスク (Negative / Risks)
- **Goバイナリへの依存増加**: MCPプロトコル用ライブラリがGoの依存関係に追加される（ただしPure Goライブラリを選定するため、CGO関連の新たなビルド制約は生じない）。
- **`ast-digger` バイナリの責務拡大**: CLIツールとしての機能に加え、長寿命なstdio常駐サーバーとしての責務を持つため、シグナルハンドリングやメモリリーク対策を意識する必要がある。

## 検討した選択肢 (Considered Alternatives)

1. **外部ラッパー型（Node.js / TypeScript の MCP サーバーから CLI を exec 呼び出し）**:
   - 公式の `@modelcontextprotocol/sdk` が使えて開発は容易だが、Node.js ランタイムや npm インストールが必須となり、本プロジェクトの重要原則である「Zero Dependency」「単一バイナリ配布」を損なう。またリクエストごとのプロセス起動オーバーヘッドが大きい。
2. **Go 製別バイナリ型（`ast-digger-mcp`）**:
   - CLI と MCP を完全に別成果物としてビルドする案。責務は明確に分離されるが、配布アーカイブ内に2つのバイナリを同梱する必要が生じ、ユーザーの PATH 設定や導入手順が煩雑化する。
3. **現行8コマンドの1対1 MCP化**:
   - CLI のコマンド体系をそのまま8つの MCP ツールとして公開する案。ツールの機能的重複（`symbol` vs `locate`, `outline` vs `directory`）により LLM のツール選定精度が劣化し（Tool Selection Degradation）、結果として `read_file` にフォールバックするリスクが高いため却下。
