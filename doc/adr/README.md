# Architecture Decision Records (ADR)

このディレクトリは、AST-Digger の技術的な意思決定プロセスと、その背景・結論を記録・管理する場所です。

## 決定一覧

| 番号 | タイトル | ステータス | 日付 |
| :--- | :--- | :--- | :--- |
| 0001 | [Cache Database Language Field Addition](0001-add-language-field-to-cache-db.md) | Accepted | 2026-07-20 |
| 0002 | [Agent-Native --json Flag Unification](0002-unify-to-json-flag.md) | Accepted | 2026-07-20 |
| 0005 | [Accept CGO and Descope Cross-Compilation](0005-accept-cgo-and-descope-cross-compilation.md) | Accepted | 2026-07-20 |
| 0006 | [Semantic Change Impact and Intent Verification](0006-semantic-change-impact-and-intent-verification.md) | Accepted | 2026-10-21 |
| 0007 | [Built-in MCP Server and Tool Consolidation](0007-builtin-mcp-server-and-tool-consolidation.md) | Accepted | 2026-11-08 |
| 0008 | [Adopt Forked tree-sitter-sql Grammar for Snowflake and CTE Support](0008-adopt-forked-tree-sitter-sql.md) | Accepted | 2026-09-21 |
| 0009 | [Direct CGO Binding for tree-sitter-markdown](0009-vendor-tree-sitter-markdown.md) | Accepted | 2026-10-21 |

> [!NOTE]
> **欠番（0003, 0004）について**:
> 当初検討されていた旧 CLI 引数仕様および Python プロトタイプ版からの移行アーキテクチャ案（0003, 0004）は、Go 版の設計統合および ADR-0005（CGO 受容と Go 単一バイナリ化）策定に伴い廃止・統合されたため欠番となっています。
