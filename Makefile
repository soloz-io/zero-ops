.PHONY: build clean test install sqlc-generate migrate-up migrate-down build-all build-auth-proxy build-mcp-server build-kube-sbt build-hub

# Build variables
AUTH_PROXY_BINARY=auth-proxy
MCP_SERVER_BINARY=mcp-server
KUBE_SBT_API_BINARY=kube-sbt
HUB_BINARY=hub
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
	$(GO) build -o $(BUILD_DIR)/$(HUB_BINARY) ./cmd/hub
	@echo "✓ All builds complete"

# Build the hub binary (main CLI)
build:
	@echo "Building $(HUB_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(HUB_BINARY) ./cmd/hub
	@echo "✓ Build complete: $(BUILD_DIR)/$(HUB_BINARY)"

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
	@echo "Building $(HUB_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(HUB_BINARY) ./cmd/hub
	@echo "✓ Build complete: $(BUILD_DIR)/$(HUB_BINARY)"

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
