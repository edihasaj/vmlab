# vmlab — top-level make targets. Thin wrappers around `go`, kept short on
# purpose. Defaults to `help`.

BINARY      ?= vmlab
PKG         ?= ./...
PREFIX      ?=
BIN_DIR     ?= $(shell go env GOPATH)/bin
SMOKE_HOST  ?= mac-studio.local
SMOKE_VM    ?= Windows 11

.DEFAULT_GOAL := help

.PHONY: help build install test vet lint cover fmt fmt-check install-hooks smoke-parallels clean

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Compile the binary into ./bin/$(BINARY).
	@mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

install: ## Install the binary. With PREFIX set -> $(PREFIX)/bin, else go install into $GOPATH/bin.
ifeq ($(strip $(PREFIX)),)
	go install ./cmd/$(BINARY)
	@echo "installed: $(BIN_DIR)/$(BINARY)"
else
	@mkdir -p "$(PREFIX)/bin"
	go build -o "$(PREFIX)/bin/$(BINARY)" ./cmd/$(BINARY)
	@echo "installed: $(PREFIX)/bin/$(BINARY)"
endif

test: ## Run all tests.
	go test $(PKG)

vet: ## go vet the whole module.
	go vet $(PKG)

fmt: ## Rewrite all Go sources via gofmt.
	gofmt -w .

fmt-check: ## Fail if any Go source needs gofmt (mirrors CI's fmt gate).
	@fmt_out=$$(gofmt -l .); \
	if [ -n "$$fmt_out" ]; then \
		echo "gofmt issues (run 'make fmt'):"; \
		echo "$$fmt_out"; \
		exit 1; \
	fi

lint: fmt-check vet ## fmt-check + vet (umbrella).

install-hooks: ## Symlink scripts/pre-commit.sh into .git/hooks/pre-commit.
	@mkdir -p .git/hooks
	@ln -sf ../../scripts/pre-commit.sh .git/hooks/pre-commit
	@chmod +x scripts/pre-commit.sh
	@echo "installed: .git/hooks/pre-commit -> scripts/pre-commit.sh"

cover: ## Test with coverage, drop a coverage.out for tooling.
	go test -coverprofile=coverage.out $(PKG)
	@go tool cover -func=coverage.out | tail -1

smoke-parallels: ## Live smoke against a real Parallels guest (needs SSH access).
	HOST=$(SMOKE_HOST) VM=$(SMOKE_VM) ./scripts/smoke-parallels.sh $(SMOKE_HOST) "$(SMOKE_VM)"

clean: ## Remove ./bin and coverage artefacts.
	rm -rf bin coverage.out
