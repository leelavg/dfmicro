.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

BIN_DIR := bin
BINARY := $(BIN_DIR)/dfmicro

.PHONY: vet fmt build build-release

vet:
	go vet ./...

fmt:
	go fmt ./...

build: fmt vet
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -o $(BINARY) ./cmd/dfmicro

build-release: fmt vet
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/dfmicro

