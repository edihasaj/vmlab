# vmlab — top-level make targets. Thin wrappers around `go`, kept short on
# purpose. Defaults to `help`.

BINARY      ?= vmlab
PKG         ?= ./...
BIN_DIR     ?= $(shell go env GOPATH)/bin
SMOKE_HOST  ?= edis-mac-studio
SMOKE_VM    ?= Windows 11

.DEFAULT_GOAL := help

.PHONY: help build install test vet lint cover smoke-parallels clean

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Targets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

build: ## Compile the binary into ./bin/$(BINARY).
	@mkdir -p bin
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

install: ## go install into $GOPATH/bin.
	go install ./cmd/$(BINARY)
	@echo "installed: $(BIN_DIR)/$(BINARY)"

test: ## Run all tests.
	go test $(PKG)

vet: ## go vet the whole module.
	go vet $(PKG)

cover: ## Test with coverage, drop a coverage.out for tooling.
	go test -coverprofile=coverage.out $(PKG)
	@go tool cover -func=coverage.out | tail -1

smoke-parallels: ## Live smoke against a real Parallels guest (needs SSH access).
	HOST=$(SMOKE_HOST) VM=$(SMOKE_VM) ./scripts/smoke-parallels.sh $(SMOKE_HOST) "$(SMOKE_VM)"

clean: ## Remove ./bin and coverage artefacts.
	rm -rf bin coverage.out
