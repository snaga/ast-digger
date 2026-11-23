# ADR-0009: Direct CGO Binding for tree-sitter-markdown

- **ステータス**: Accepted
- **日付**: 2026-10-21
- **起票者**: AST-Digger Core Team

## コンテキストと課題

Markdown ドキュメントの見出し構造（# H1〜H6）やセクション境界を AST 解析するため、公式の `github.com/tree-sitter-grammars/tree-sitter-markdown` を導入する必要があった。
しかし、Go 用の独立したバインディングパッケージ（`bindings/go`）が提供されていない、あるいは `tree-sitter-markdown` の複雑なスキャナ実装（CommonMark 仕様に準拠した C99/C11 スキャナ）が標準の Go ラッパーで直接 CGO ビルドしにくい制約が存在した。

## 決定事項

1. `internal/parser/markdown/` パッケージを新設し、自前の `binding.go` を配置する。
2. `go.mod` で管理される `github.com/tree-sitter-grammars/tree-sitter-markdown` のソースコード（`parser.c`, `scanner.c`）を `#cgo CFLAGS: -std=c11` および `#include` で直接コンパイル・バインディングする手法を採用する。
3. `unsafe.Pointer(C.tree_sitter_markdown())` を返す最小限のインターフェースを提供し、上位の `internal/parser/languages.go` および `MarkdownParser` から透過的に利用可能とする。

## 影響とリスク

- **メリット**:
  - サードパーティの非公式 Go ラッパーライブラリを介さず、公式の C 文法定義を直接安全にビルド可能。
  - CGO ビルド時のコンパイルフラグ（`-std=c11`）を局所的に明示指定できる。
- **管理方針**:
  - `go.mod` における `tree-sitter-markdown` のバージョン更新時は、CGO include パスや C コンパイル互換性を検証した上で引き上げる。
