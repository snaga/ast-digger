# MCP (Model Context Protocol) サーバーパッケージ (`internal/mcp`)

## 1. パッケージの責務
本パッケージは、AIエージェントと `ast-digger` をシームレスに連携させるための **Model Context Protocol (MCP)** stdio トランスポート対応 JSON-RPC サーバーを提供します。
`ast-digger` のコア解析能力（アウトライン抽出、シンボルピンポイント抽出、依存関係追跡、意味的変更インパクト解析）を、エージェント指向に統合・再構成した4大コアツール（`ast_explore`, `ast_symbol`, `ast_trace`, `ast_audit`）として外部AIエージェントに提供します。

## 2. 依存制約
本パッケージは以下のモジュールおよび内部パッケージにのみ依存します：
- `github.com/mark3labs/mcp-go`: MCP プロトコル仕様（JSON-RPC 2.0、Stdio トランスポート）の準拠
- `internal/outline`: AST アウトライン生成、ディレクトリツリー、シンボル抽出、インポート・参照解決
- `internal/parser`: 言語パーサー群および共通ASTデータ構造
- `internal/impact`: 意味的変更規模（Statements/Complexity差分）および Docstring 監査

※ 循環参照を防ぐため、CLI層（`cmd/ast-digger`）から本パッケージを参照し、本パッケージから `cmd/` を参照してはなりません。

## 3. 設計方針・決定理由 (L1 Intent)
- **単一バイナリ内包型 (Self-Contained)**:
  - ADR-0007 に基づき、Node.js や Python などの外部ランタイムを一切要求せず、単一の Go バイナリ（`ast-digger mcp`）として自己完結します。
- **インプロセス実行によるミリ秒応答 (Zero-Overhead)**:
  - プロセス間通信や子プロセス再起動のオーバーヘッドを排除し、`internal/outline` や `internal/impact` を同一プロセス内から直接関数呼び出しします。
- **プロトコル整合性の保証 (Stdio Isolation)**:
  - MCP stdio トランスポートにおいて、標準出力（stdout）は純粋な JSON-RPC メッセージストリームとして厳密に扱われる必要があります。
  - 診断メッセージ、エラーログ、サードパーティ製ライブラリのデバッグ出力が stdout に混入することを防ぐため、サーバー起動時にログ出力先を強制的に `os.Stderr` に退避・分離します。
- **障害耐性とプロセス保護 (Robustness)**:
  - パニックリカバリ機構およびシグナルハンドリングを備え、万一のパースエラーや不正な引数入力時にも MCP サーバープロセス全体がクラッシュせず、MCP プロトコル準拠のエラーレスポンスを返却します。
