.PHONY: build clean test install sqlc-generate migrate-up migrate-down build-all build-auth-proxy build-mcp-server build-kube-sbt build-hub cli-release cli-fetch publish-local

# Build variables
AUTH_PROXY_BINARY=auth-proxy
MCP_SERVER_BINARY=mcp-server
KUBE_SBT_API_BINARY=kube-sbt
SOLOZ_BINARY=soloz
BUILD_DIR=bin
GO=go
DATABASE_URL?=postgres://localhost:5432/zeroops?sslmode=disable

# Build all binaries
build-all:
	@echo "Building all binaries..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(AUTH_PROXY_BINARY) ./cmd/auth-proxy
	$(GO) build -o $(BUILD_DIR)/$(MCP_SERVER_BINARY) ./cmd/mcp-server
	$(GO) build -o $(BUILD_DIR)/$(KUBE_SBT_API_BINARY) ./cmd/kube-sbt
	$(GO) build -o $(BUILD_DIR)/$(SOLOZ_BINARY) ./cmd/soloz
	@echo "✓ All builds complete"

# Build the hub binary (main CLI)
build:
	@echo "Building $(SOLOZ_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(SOLOZ_BINARY) ./cmd/soloz
	@echo "✓ Build complete: $(BUILD_DIR)/$(SOLOZ_BINARY)"

# Build the auth-proxy binary
build-auth-proxy:
	@echo "Building $(AUTH_PROXY_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(AUTH_PROXY_BINARY) ./cmd/auth-proxy
	@echo "✓ Build complete: $(BUILD_DIR)/$(AUTH_PROXY_BINARY)"

# Build the mcp-server binary
build-mcp-server:
	@echo "Building $(MCP_SERVER_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(MCP_SERVER_BINARY) ./cmd/mcp-server
	@echo "✓ Build complete: $(BUILD_DIR)/$(MCP_SERVER_BINARY)"

# Build the kube-sbt binary
build-kube-sbt:
	@echo "Building $(KUBE_SBT_API_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(KUBE_SBT_API_BINARY) ./cmd/kube-sbt
	@echo "✓ Build complete: $(BUILD_DIR)/$(KUBE_SBT_API_BINARY)"

# Build the hub binary
build-hub:
	@echo "Building $(SOLOZ_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(SOLOZ_BINARY) ./cmd/soloz
	@echo "✓ Build complete: $(BUILD_DIR)/$(SOLOZ_BINARY)"

# Package, gate and publish the bundle from here, at VERSION.
#
# The same script the release workflow runs (scripts/package/publish.sh), so what
# this puts in the registry went through the same gates as a release. What it
# avoids is the ~9m30s a dispatch costs, nearly all of which is a cold runner
# refetching third-party charts this machine already has cached.
#
# OWNER is the GHCR namespace; it must be the one a scaffolded bundle names, or
# the cluster will resolve charts from somewhere this did not publish to.
#
# Requires an authenticated helm -- the script does not log in, because the
# credential belongs to whoever is running it:
#
#   echo $$GITHUB_TOKEN | helm registry login ghcr.io -u <you> --password-stdin
#
# SKIP_PUSH=1 packages and gates without publishing, which spends no version.
# Use it to find a packaging mistake before committing to an -rc number.
OWNER ?= soloz-io
publish-local:
	@test -n "$(VERSION)" || { echo "usage: make publish-local VERSION=0.1.16-rc.1 [OWNER=soloz-io] [SKIP_PUSH=1]"; exit 1; }
	SKIP_PUSH=$(SKIP_PUSH) ./scripts/package/publish.sh "$(VERSION)" "$(OWNER)"

# Build the CLI in its RELEASED shape, at VERSION.
#
# `build` above produces a development binary: ADR-068 makes it read platform
# manifests from the working directory and makes `tenant scaffold` render a
# bundle that points ArgoCD at this repository at a branch. A tenant runs neither.
# This target produces what a tenant runs -- platform content embedded, bundle
# pointing at the published OCI chart -- so an end-to-end test locally exercises
# the path that ships.
#
# VERSION must already be published by the release workflow, because the bundle
# this binary renders names a chart by version and ArgoCD pulls it from GHCR:
#
#   gh workflow run publish-platform-charts.yml -f version=0.1.16-rc.1
#
# A prerelease suffix keeps the real version free -- ADR-063 consumes a version
# by publishing it, so testing on 0.1.16-rc.1 leaves 0.1.16 available.
# See docs/runbooks/local-release-path-testing.md.
cli-release:
	@test -n "$(VERSION)" || { echo "usage: make cli-release VERSION=0.1.16-rc.1"; exit 1; }
	TARGETS=host ./scripts/package/build-cli.sh "$(VERSION)" $(BUILD_DIR)
	@cp $(BUILD_DIR)/soloz-$$($(GO) env GOOS)-$$($(GO) env GOARCH) $(BUILD_DIR)/$(SOLOZ_BINARY)
	@echo "✓ $(BUILD_DIR)/$(SOLOZ_BINARY) declares $(VERSION)"

# Fetch the exact binary CI built for VERSION, rather than rebuilding it.
#
# Use this when what you are testing is the charts or the manifests: it removes
# the local toolchain from the question entirely, so a failure is the platform's
# and not the laptop's. Use cli-release instead when the change under test is in
# the CLI itself.
# RUN=<id> overrides which run to take it from. Without a run id `gh run
# download` looks only at the most recent run, which is the wrong one as soon as
# anything else has been pushed since -- so the run is resolved by asking which
# completed publish run carries this version's artifact.
cli-fetch:
	@test -n "$(VERSION)" || { echo "usage: make cli-fetch VERSION=0.1.16-rc.1 [RUN=<id>]"; exit 1; }
	@mkdir -p $(BUILD_DIR)
	@run="$(RUN)"; \
	if [ -z "$$run" ]; then \
	  run=$$(gh run list --workflow=publish-platform-charts.yml --status=success \
	          --limit 30 --json databaseId --jq '.[].databaseId' \
	        | while read -r id; do \
	            if gh api "repos/{owner}/{repo}/actions/runs/$$id/artifacts" \
	                 --jq '.artifacts[].name' 2>/dev/null \
	               | grep -qx "soloz-$(VERSION)"; then echo "$$id"; break; fi; \
	          done); \
	fi; \
	test -n "$$run" || { echo "no successful publish run carries soloz-$(VERSION); pass RUN=<id>"; exit 1; }; \
	echo "fetching soloz-$(VERSION) from run $$run"; \
	gh run download "$$run" --name "soloz-$(VERSION)" --dir $(BUILD_DIR)
	@cp $(BUILD_DIR)/soloz-$$($(GO) env GOOS)-$$($(GO) env GOARCH) $(BUILD_DIR)/$(SOLOZ_BINARY)
	@chmod +x $(BUILD_DIR)/$(SOLOZ_BINARY)
	@echo "✓ $(BUILD_DIR)/$(SOLOZ_BINARY) is the CI build of $(VERSION)"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "✓ Clean complete"

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...
	./scripts/validate-spokepool-compositions.sh
	./scripts/validate-argocd-seed-parity.sh
	./scripts/validate-cell-id-contract.sh
	./scripts/validate-component-descriptors.sh
	@echo "✓ Tests complete"

# Install binary to system
install: build
	@echo "Installing $(BINARY_NAME)..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "✓ Installed to /usr/local/bin/$(BINARY_NAME)"

# Initialize Go module
init:
	@echo "Initializing Go module..."
	$(GO) mod init github.com/soloz-io/zero-ops
	$(GO) mod tidy
	@echo "✓ Go module initialized"

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	@echo "✓ Format complete"

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run
	@echo "✓ Lint complete"

# NOTE: the three targets below address internal/db, which does not exist in this
# repository. They were inert before zero-ops-api was removed; the only migrations
# in the tree lived under internal/zero-ops-api/db/migrations and went with it.
# Left rather than deleted silently: repoint them at a real directory, or remove
# them deliberately.
# Generate sqlc code
sqlc-generate:
	@echo "Generating sqlc code..."
	@cd internal/db && sqlc generate
	@echo "✓ sqlc generation complete"

# Run database migrations up
migrate-up:
	@echo "Running database migrations up..."
	@atlas migrate apply --dir file://internal/db/migrations --url "$(DATABASE_URL)"
	@echo "✓ Migrations applied"

# Run database migrations down
migrate-down:
	@echo "Rolling back database migrations..."
	@atlas migrate down --dir file://internal/db/migrations --url "$(DATABASE_URL)"
	@echo "✓ Migrations rolled back"
