# SPDX-License-Identifier: MIT
# go-ha-catalog — developer Makefile
#
# Tabs are required by GNU make. The whitespace rules below pin sane
# shell behaviour so a failing recipe step actually aborts the target
# instead of silently moving on.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

GO            ?= go
GOFUMPT       ?= gofumpt
GOLANGCI_LINT ?= golangci-lint
MODULE        := github.com/SukramJ/go-ha-catalog

export CGO_ENABLED := 0

# Path to a local home-assistant/core checkout for catalog regeneration.
HA_CORE ?= ../core

.PHONY: help
help: ## show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: setup
setup: ## install developer tooling (gofumpt, golangci-lint)
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

.PHONY: test
test: ## run the full test suite with race detector
	CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=120s ./...

.PHONY: test-cover
test-cover: ## run tests + coverage report
	CGO_ENABLED=1 $(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -20

.PHONY: vet
vet: ## run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## format with gofumpt (writes in place)
	$(GOFUMPT) -w .

.PHONY: fmt-check
fmt-check: ## fail when sources are not gofumpt-clean
	@diff=$$($(GOFUMPT) -l .); \
	if [ -n "$$diff" ]; then \
	  echo "gofumpt would rewrite:"; echo "$$diff"; exit 1; \
	fi

.PHONY: lint
lint: ## run golangci-lint
	$(GOLANGCI_LINT) run ./...

.PHONY: tidy
tidy: ## sync go.mod
	$(GO) mod tidy

.PHONY: check
check: vet fmt-check lint test ## the pre-commit / pre-push gate

.PHONY: regenerate
regenerate: ## regenerate data/ + gen_vocab.go from $(HA_CORE) and re-run the tests
	script/regenerate.sh $(HA_CORE)
	$(MAKE) --no-print-directory test

.PHONY: generate
generate: ## regenerate only gen_vocab.go from the committed data/ (no venv needed)
	$(GO) run ./script/gen -data data -out gen_vocab.go
	$(GOFUMPT) -w gen_vocab.go

.PHONY: snapshot-version
snapshot-version: ## print the embedded Home Assistant snapshot version
	@sed -n 's/^const SnapshotVersion = "\(.*\)"$$/\1/p' hacatalog.go

.PHONY: snapshot-ref
snapshot-ref: ## print the exact core checkout the catalog was cut from
	@sed -n 's/^const SnapshotRef = "\(.*\)"$$/\1/p' hacatalog.go

.PHONY: clean
clean: ## remove build artefacts (keeps the venv; use clean-venv for that)
	rm -f coverage.out gen_vocab.go.broken

.PHONY: clean-venv
clean-venv: ## remove the extraction venv
	rm -rf .venv
