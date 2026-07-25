# AST-Digger (Go 実装版) ビルドスクリプト
#
# CGO 依存（tree-sitter バインディング）を許容する方針のため、ビルドホストに
# Cコンパイラ（gcc/clang等）が必要です。詳細は README.md および
# SPECS/tech.md「CGO 依存に関する既知の制約」を参照してください。
#
# クロスコンパイル（GOOS/GOARCH の切り替え）はスコープ外です。各プラットフォーム
# 向けのバイナリは、そのプラットフォーム上でのネイティブビルドで生成してください
# (doc/adr/0005-accept-cgo-and-descope-cross-compilation.md 参照)。
#
# 使い方:
#   make build         通常ビルド（./ast-digger または ./ast-digger.exe を生成）
#   make build-static  可能な範囲で静的リンクを試みるビルド（macOSでは通常ビルドにフォールバック）
#   make test          go test ./...
#   make lint          gofmt -l . / go vet ./... / golangci-lint run ./...
#   make clean         生成物の削除
#
# make が利用できない環境（素のWindows PowerShell等）では、このMakefileの各
# targetが実行しているコマンドを直接シェルから実行してください（README.md
# の「ビルド手順」節にPowerShell向けの代替コマンドを掲載しています）。

BINARY := ast-digger
CMD := ./cmd/ast-digger
GOOS := $(shell go env GOOS)
GOEXE := $(shell go env GOEXE)
OUT := $(BINARY)$(GOEXE)

VERSION ?= dev

.PHONY: build build-static test lint clean

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o $(OUT) $(CMD)

build-static:
ifeq ($(GOOS),darwin)
	@echo "macOS はAppleのOS制約により完全な静的リンクができないため、通常ビルド（システムライブラリへの動的リンクを許容）にフォールバックします。"
	CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(OUT) $(CMD)
else
	CGO_ENABLED=1 go build -ldflags '-s -w -X main.version=$(VERSION) -linkmode external -extldflags "-static"' -o $(OUT) $(CMD)
endif

test:
	go test ./...

lint:
	@fmtout="$$(gofmt -l .)"; \
	if [ -n "$$fmtout" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$fmtout"; \
		exit 1; \
	fi
	go vet ./...
	golangci-lint run ./...

clean:
	rm -f $(BINARY) $(BINARY).exe
