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
TOPOLVM_VERSION ?= 17.3.1
VOLSYNC_VERSION ?= 0.17.0
VOLSYNC_CHART_COMMIT ?= d90957908caa97c9088111ad574f2c12ae3a4916
ARGOCD_VERSION ?= 3.6.1
VELERO_VERSION ?= 1.18.4
VELERO_CHART_VERSION ?= 12.2.1
VELERO_AWS_PLUGIN_VERSION ?= 1.14.4
MDBOOK_VERSION ?= 0.5.4
MDBOOK := $(BIN_DIR)/mdbook

.PHONY: vet fmt generate generate-topolvm generate-ocm-controller generate-volsync generate-argocd generate-velero build build-cross build-release build-analyze lint install-mdbook docs docs-serve

vet:
	go vet ./...

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

generate: generate-topolvm generate-ocm-controller generate-volsync generate-argocd generate-velero
	go generate ./internal/docs

generate-topolvm:
	TOPOLVM_VERSION=$(TOPOLVM_VERSION) ./hack/generate-topolvm.sh

generate-ocm-controller:
	./hack/generate-ocm-controller.sh

generate-volsync:
	VOLSYNC_VERSION=$(VOLSYNC_VERSION) VOLSYNC_CHART_COMMIT=$(VOLSYNC_CHART_COMMIT) ./hack/generate-volsync.sh

generate-argocd:
	ARGOCD_VERSION=$(ARGOCD_VERSION) ./hack/generate-argocd.sh

generate-velero:
	VELERO_VERSION=$(VELERO_VERSION) VELERO_CHART_VERSION=$(VELERO_CHART_VERSION) VELERO_AWS_PLUGIN_VERSION=$(VELERO_AWS_PLUGIN_VERSION) ./hack/generate-velero.sh

fix:
	go fix ./...

build: fmt vet generate fix
	$(MAKE) -s build-cross

install: build
	install $(BINARY) ~/.local/bin

build-release: fmt vet generate
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro

build-analyze: fmt vet generate
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro

build-cross:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/dfmicro
