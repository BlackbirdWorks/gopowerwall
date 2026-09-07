.PHONY: build build-linux build-proxy install-deps lint lint-fix test integration-test total-coverage proto bench clean upgrade all

BINARY_NAME=gopowerwall
PROXY_NAME=proxy
MODULE=github.com/blackbirdworks/gopowerwall
VERSION_PKG=$(MODULE)/pkgs/version
BUILD_VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-w -s -X $(VERSION_PKG).BuildVersion=$(BUILD_VERSION)

build:
	go build \
		-trimpath \
		-ldflags "$(LDFLAGS)" \
		-o bin/$(BINARY_NAME) ./cmd/gopowerwall
	go build \
		-trimpath \
		-ldflags "$(LDFLAGS)" \
		-o bin/$(PROXY_NAME) ./cmd/proxy

build-linux:
	CGO_ENABLED=0 GOOS=linux go build \
		-tags 'netgo osusergo static_build' \
		-trimpath \
		-ldflags "$(LDFLAGS)" \
		-o bin/$(BINARY_NAME) ./cmd/gopowerwall
	CGO_ENABLED=0 GOOS=linux go build \
		-tags 'netgo osusergo static_build' \
		-trimpath \
		-ldflags "$(LDFLAGS)" \
		-o bin/$(PROXY_NAME) ./cmd/proxy

install-deps:
	@echo "Checking for golangci-lint..."
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		if command -v brew >/dev/null 2>&1; then \
			brew install golangci-lint; \
		else \
			echo "Homebrew not found. Trying go install from source..."; \
			GOMODCACHE=$$(mktemp -d) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest; \
		fi \
	else \
		echo "golangci-lint is already installed."; \
		if command -v brew >/dev/null 2>&1; then \
			echo "Upgrading golangci-lint via brew..."; \
			brew upgrade golangci-lint || true; \
		fi \
	fi
	@echo "Checking for fieldalignment..."
	@if ! command -v fieldalignment >/dev/null 2>&1; then \
		echo "Installing fieldalignment..."; \
		go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest; \
	else \
		echo "fieldalignment is already installed."; \
	fi

lint: install-deps
	golangci-lint run --timeout 10m ./...
	go tool govulncheck ./...

lint-fix: install-deps
	@echo "Running fieldalignment..."
	fieldalignment -fix ./... || true
	@echo "Running golangci-lint with --fix..."
	golangci-lint run --fix ./...

test:
	go tool gotestsum --format pkgname -- -race -shuffle on -short ./...

integration-test:
	go tool gotestsum --format pkgname -- -race -shuffle on -timeout 10m -tags=integration ./test/integration/...

total-coverage:
	$(eval COVERPKGS := $(shell go list ./... | grep -v -E '(/test/|/proto/)' | tr '\n' ',' | sed 's/,$$//'))
	@echo "Running unit tests with coverage..."
	go tool gotestsum --format pkgname -- -race -shuffle on -short -timeout 5m \
		-coverpkg=$(COVERPKGS) -coverprofile=unit-coverage.out -covermode=atomic ./...
	@echo "Running integration tests with coverage..."
	@if [ -d test/integration ]; then \
		go tool gotestsum --format pkgname -- -race -shuffle on -timeout 10m -tags=integration \
			-coverpkg=$(COVERPKGS) -coverprofile=integration-coverage.out -covermode=atomic ./test/integration/...; \
	else \
		echo "No integration tests yet; skipping."; \
		echo "mode: atomic" > integration-coverage.out; \
	fi
	@echo "Merging coverage profiles..."
	@echo "mode: atomic" > coverage.out
	@tail -n +2 unit-coverage.out >> coverage.out
	@tail -n +2 integration-coverage.out >> coverage.out
	@rm -f unit-coverage.out integration-coverage.out
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

# Regenerate the Go bindings for the vendored Tesla protobuf definitions.
# Output is byte-reproducible with protoc-gen-go v1.36.12.
proto:
	protoc --proto_path=proto/tedapiv2 --go_out=. --go_opt=module=$(MODULE) proto/tedapiv2/*.proto
	protoc --proto_path=proto/tedapi --go_out=. --go_opt=module=$(MODULE) proto/tedapi/tedapi.proto
	protoc --proto_path=proto/tedapi/combined --go_out=. --go_opt=module=$(MODULE) proto/tedapi/combined/tedapi_combined.proto
	protoc --proto_path=proto/teslapower --go_out=. --go_opt=module=$(MODULE) proto/teslapower/tesla.proto

bench:
	go test -bench=. -benchmem ./...

clean:
	rm -rf bin/ coverage.out coverage.html

upgrade:
	go get -u ./...
	go mod tidy

all:
	make lint-fix
	make total-coverage
