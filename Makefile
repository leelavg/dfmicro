.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

BIN_DIR := bin
BINARY := $(BIN_DIR)/dfmicro
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X dfmicro/internal/buildinfo.BuildTime=$(BUILD_TIME)
TOPOLVM_VERSION ?= 17.2.0
MDBOOK_VERSION ?= 0.5.4
MDBOOK := $(BIN_DIR)/mdbook

.PHONY: vet fmt generate generate-topolvm build build-cross build-release build-analyze build-fetch lint install-mdbook docs docs-serve

vet:
	go vet -tags fetch ./...

lint:
	golangci-lint run

install-mdbook:
	if [[ -x "$(MDBOOK)" && "$$($(MDBOOK) --version)" == "mdbook v$(MDBOOK_VERSION)" ]]; then
		exit 0
	fi
	case "$$(uname -s)/$$(uname -m)" in
		Linux/x86_64) target=x86_64-unknown-linux-gnu ;;
		Linux/aarch64|Linux/arm64) target=aarch64-unknown-linux-gnu ;;
		Darwin/x86_64) target=x86_64-apple-darwin ;;
		Darwin/arm64|Darwin/aarch64) target=aarch64-apple-darwin ;;
		*) echo "unsupported mdBook platform: $$(uname -s)/$$(uname -m)" >&2; exit 1 ;;
	esac
	tmp=$$(mktemp -d)
	trap 'rm -rf "$$tmp"' EXIT
	curl -fsSL "https://github.com/rust-lang/mdBook/releases/download/v$(MDBOOK_VERSION)/mdbook-v$(MDBOOK_VERSION)-$$target.tar.gz" -o "$$tmp/mdbook.tar.gz"
	tar -xzf "$$tmp/mdbook.tar.gz" -C "$$tmp"
	mkdir -p "$(BIN_DIR)"
	install -m 0755 "$$tmp/mdbook" "$(MDBOOK)"

docs: install-mdbook
	MDBOOK="$(abspath $(MDBOOK))" ./site/publish.sh

docs-serve: docs
	python3 -m http.server --directory site/public 8000

fmt:
	go fmt ./...

generate: generate-topolvm
	go generate ./internal/docs

generate-topolvm:
	TOPOLVM_VERSION=$(TOPOLVM_VERSION) ./hack/generate-topolvm.sh

fix:
	go fix -tags fetch ./...

build-fetch: fmt vet
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -tags fetch -o $(BIN_DIR)/fetch ./cmd/fetch

build: fmt vet generate fix
	$(MAKE) -s build-cross

install: build
	install $(BINARY) ~/.local/bin

build-release: fmt vet generate
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -tags '!fetch' -ldflags="-s -w $(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro

build-analyze: fmt vet generate
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -tags '!fetch' -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro

build-cross:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -tags '!fetch' -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro
