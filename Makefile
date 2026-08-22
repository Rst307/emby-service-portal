# Emby Service Portal development helpers.
# Usage: make <target>   (see the list below)

GO      ?= go
BINARY   = dist/emby-service-portal
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  = -s -w \
	-X github.com/Rst307/emby-service-portal/internal/buildinfo.Version=$(VERSION) \
	-X github.com/Rst307/emby-service-portal/internal/buildinfo.Commit=$(COMMIT)

.PHONY: help fmt fmt-check vet test race coverage build linux run check clean

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

fmt: ## Format all Go sources
	gofmt -w $$(git ls-files '*.go')

fmt-check: ## Fail when any Go source is not formatted
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))" || { echo "gofmt needed:"; gofmt -l $$(git ls-files '*.go'); exit 1; }

vet: ## Run go vet
	$(GO) vet ./...

test: ## Run all tests
	$(GO) test -count=1 ./...

race: ## Run tests with the race detector
	$(GO) test -race -count=1 ./...

coverage: ## Run tests and print coverage per function
	$(GO) test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

build: ## Build a native binary into dist/ (embeds current git version)
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags='$(LDFLAGS)' -o $(BINARY) ./cmd/emby-service-portal

linux: ## Cross-compile a static Linux amd64 binary into dist/
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		$(GO) build -trimpath -buildvcs=false -ldflags='$(LDFLAGS)' \
		-o $(BINARY)-linux-amd64 ./cmd/emby-service-portal

windows: ## Cross-compile a Windows amd64 binary into dist/
	mkdir -p dist
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		$(GO) build -trimpath -buildvcs=false -ldflags='$(LDFLAGS)' \
		-o $(BINARY)-windows-amd64.exe ./cmd/emby-service-portal

run: ## Build and run locally (requires exported ESP_* variables)
	$(GO) run ./cmd/emby-service-portal

check: fmt-check vet test race ## Full quality gate (matches CI)

clean: ## Remove build artifacts
	rm -rf dist coverage*.out