# blockchain-ai-tools — monorepo Makefile
#
# Multi-module Go workspace. Iterating targets discover modules dynamically
# from apps/ and libs/, so every target works with zero modules today and
# scales automatically as you add them via `make new-app` / `make new-lib`.

MODULE_BASE := github.com/rootwarp/blockchain-ai-tools

# Module directories: every dir under apps/ or libs/ that has a go.mod.
MODULES := $(shell find apps libs -name go.mod -exec dirname {} \; 2>/dev/null | sort)

GO ?= go

ROOT := $(CURDIR)
BIN := $(ROOT)/bin

NO_MODULES_MSG := no modules yet — create one with 'make new-app name=foo'

.DEFAULT_GOAL := help

## help: Show this help.
.PHONY: help
help:
	@echo "blockchain-ai-tools — available targets:"
	@grep -E '^## [a-zA-Z0-9_-]+:' $(MAKEFILE_LIST) \
		| sed -E 's/^## ([a-zA-Z0-9_-]+): /\1\t/' \
		| awk -F'\t' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Modules ($(words $(MODULES))): $(MODULES)"

## build: Build all modules (app binaries -> bin/, libs compile-checked).
.PHONY: build
build:
	@test -n "$(MODULES)" || echo "$(NO_MODULES_MSG)"
	@for m in $(MODULES); do echo ">> build $$m"; \
		case $$m in \
			apps/*) ( cd $$m && $(GO) build -trimpath -buildvcs=true -o "$(BIN)/" ./... ) || exit 1 ;; \
			*)      ( cd $$m && $(GO) build ./... )            || exit 1 ;; \
		esac; \
	done

## test: Run tests in all modules.
.PHONY: test
test:
	@test -n "$(MODULES)" || echo "$(NO_MODULES_MSG)"
	@for m in $(MODULES); do echo ">> test $$m"; ( cd $$m && $(GO) test ./... ) || exit 1; done

## vet: Run go vet in all modules.
.PHONY: vet
vet:
	@test -n "$(MODULES)" || echo "$(NO_MODULES_MSG)"
	@for m in $(MODULES); do echo ">> vet $$m"; ( cd $$m && $(GO) vet ./... ) || exit 1; done

## tidy: Run go mod tidy in all modules.
.PHONY: tidy
tidy:
	@test -n "$(MODULES)" || echo "$(NO_MODULES_MSG)"
	@for m in $(MODULES); do echo ">> tidy $$m"; ( cd $$m && $(GO) mod tidy ) || exit 1; done

## fmt: Format all Go code (gofmt -s).
.PHONY: fmt
fmt:
	@files=$$(find apps libs -name '*.go' 2>/dev/null); \
	if [ -n "$$files" ]; then gofmt -w -s $$files && echo "formatted $$(printf '%s\n' $$files | wc -l | tr -d ' ') file(s)"; \
	else echo "no .go files yet"; fi

## lint: Run golangci-lint in all modules.
.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found on PATH."; \
		echo "install: https://golangci-lint.run/welcome/install/"; exit 1; }
	@test -n "$(MODULES)" || echo "$(NO_MODULES_MSG)"
	@for m in $(MODULES); do echo ">> lint $$m"; ( cd $$m && golangci-lint run ) || exit 1; done

## new-app: Scaffold a new app module. Usage: make new-app name=foo
.PHONY: new-app
new-app:
	@scripts/new-module.sh app "$(name)" "$(MODULE_BASE)"

## new-lib: Scaffold a new library module. Usage: make new-lib name=foo
.PHONY: new-lib
new-lib:
	@scripts/new-module.sh lib "$(name)" "$(MODULE_BASE)"

## clean: Remove build artifacts.
.PHONY: clean
clean:
	@rm -rf bin/
	@echo "cleaned."
