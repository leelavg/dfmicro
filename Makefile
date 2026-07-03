.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

BIN_DIR := bin
BINARY := $(BIN_DIR)/dfmicro

.PHONY: vet fmt build

vet:
	go vet ./...

fmt:
	go fmt ./...

build: fmt vet
	mkdir -p $(BIN_DIR)
	go build -o $(BINARY) ./cmd/dfmicro

