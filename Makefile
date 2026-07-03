.ONESHELL:
.DELETE_ON_ERROR:
.SHELLFLAGS := -eu -c
SHELL := bash
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

.PHONY: fmt test

fmt:
	go fmt ./...

test:
	go test ./...