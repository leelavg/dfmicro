.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

BIN_DIR := bin
BINARY := $(BIN_DIR)/dfmicro

.PHONY: vet fmt generate build build-release build-analyze

vet:
	go vet ./...

fmt:
	go fmt ./...

generate:
	go generate ./internal/docs

build: fmt vet generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/dfmicro

build-release: fmt vet generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/dfmicro

build-analyze: fmt vet generate
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/dfmicro

