SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

.PHONY: help docs-check protocol-check aba-fmt aba-check aba-test platform-check hc-check mvp-local verify

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "; printf "Harness Platform targets:\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

docs-check: ## Run repository-local documentation consistency checks
	@./scripts/check-docs.sh

protocol-check: ## Validate AWP protocol sources with locally available tools
	@./scripts/check-protocol.sh

aba-fmt: ## Check ABA Rust formatting
	@cd aba && cargo fmt --all -- --check

aba-check: ## Compile-check ABA Rust workspace
	@cd aba && cargo check --workspace --all-targets

aba-test: ## Run ABA Rust tests
	@cd aba && cargo test --workspace --all-targets

platform-check: ## Run Platform checks after the pinned upstream source is imported
	@if [[ ! -f platform/go.mod ]]; then \
		echo "platform/go.mod is absent; import the pinned upstream source first" >&2; \
		exit 2; \
	fi
	@cd platform && go test ./...

hc-check: ## Run HC checks after its package workspace is initialized
	@if [[ ! -f hc/package.json ]]; then \
		echo "hc/package.json is absent; initialize the HC workspace first" >&2; \
		exit 2; \
	fi
	@cd hc && pnpm lint && pnpm typecheck && pnpm test

mvp-local: ## Run the complete initialized local MVP topology
	@./deploy/run-local-mvp.sh

verify: docs-check protocol-check aba-fmt aba-check aba-test ## Run checks available in the foundation stage
