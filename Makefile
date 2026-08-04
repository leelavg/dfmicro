.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

BIN_DIR := bin
BINARY := $(BIN_DIR)/dfmicro

.PHONY: vet fmt generate build build-cross build-release build-analyze build-fetch

vet:
	go vet -tags fetch ./...

fmt:
	go fmt ./...

generate:
	go generate ./internal/docs

fix:
	go fix -tags fetch ./...

build-fetch: fmt vet
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -tags fetch -o $(BIN_DIR)/fetch ./cmd/fetch

build: fmt vet generate fix
	$(MAKE) -s build-cross

build-release: fmt vet generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -tags '!fetch' -ldflags="-s -w" -o $(BINARY) ./cmd/dfmicro

build-analyze: fmt vet generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -tags '!fetch' -o $(BINARY) ./cmd/dfmicro

build-cross:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -tags '!fetch' -o $(BINARY) ./cmd/dfmicro
