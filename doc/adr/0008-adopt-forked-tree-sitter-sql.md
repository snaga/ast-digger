# ADR-0008: Adopt Forked tree-sitter-sql Grammar for Snowflake and CTE Support

- **ステータス**: Accepted
- **日付**: 2026-09-21
- **起票者**: AST-Digger Core Team

## コンテキストと課題

AST-Digger は SQL ファイル（特に Snowflake 等のモダンデータウェアハウス環境で多用される大規模 CTE や CREATE VIEW 等）の構文木解析をサポートする必要があった。
しかし、標準的に参照される `github.com/DerekStride/tree-sitter-sql` の最新リリース版では以下の課題が存在した：

1. **CGO ビルド互換性の不具合**: 最新の `go-tree-sitter` (v0.24+) とのバインディング時にシンボル不整合や CGO コンパイルエラーが発生する問題があった。
2. **モダン構文・CTEサポートの不足**: Snowflake 固有の拡張構文やネストされた CTE の走査においてパースエラーとなり、完全な AST 走査が困難であった。

コミュニティフォークである `github.com/mdovale/tree-sitter-sql` ではこれらのビルド問題および Snowflake 対応が修正・コミットされていた。

## 決定事項

1. `go.mod` において `replace` ディレクティブを採用し、`github.com/DerekStride/tree-sitter-sql` を `github.com/mdovale/tree-sitter-sql`（特定コミット `v0.3.12-0.20260414211745-83a3d443f094`）へリプレイスする。
2. これにより CGO ビルド互換性を確保しつつ、Snowflake CTE / サブクエリ / VIEW 定義の精密パースを可能にする。

## 影響とリスク

- **メリット**:
  - Windows / Linux / macOS における CGO ビルドが正常に通過する。
  - Snowflake SQL の CTE や動的テーブル、ビュー定義を安全かつ高精度にパース可能。
- **リスクと将来の復帰条件**:
  - サードパーティ個人フォークへの依存リスクが存在する。
  - **公式復帰条件**: 上流の公式リポジトリまたは主要メンテナリポジトリにおいて `go-tree-sitter` 最新版とのビルド互換および Snowflake CTE パースがサポートされた公式タグがリリースされた時点で、フォーク依存を解消し公式リポジトリへ復帰する。
