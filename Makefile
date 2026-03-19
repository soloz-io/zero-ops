.PHONY: build clean test install sqlc-generate migrate-up migrate-down run build-all build-auth-proxy build-mcp-server build-opensbt build-hub

# Build variables
API_BINARY_NAME=zero-ops-api
AUTH_PROXY_BINARY=auth-proxy
MCP_SERVER_BINARY=mcp-server
OPENSBT_BINARY=opensbt
HUB_BINARY=hub
BUILD_DIR=bin
GO=go
DATABASE_URL?=postgres://localhost:5432/zeroops?sslmode=disable

# Build all binaries
build-all:
	@echo "Building all binaries..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(API_BINARY_NAME) ./cmd/zero-ops-api
	$(GO) build -o $(BUILD_DIR)/$(AUTH_PROXY_BINARY) ./cmd/auth-proxy
	$(GO) build -o $(BUILD_DIR)/$(MCP_SERVER_BINARY) ./cmd/mcp-server
	$(GO) build -o $(BUILD_DIR)/$(OPENSBT_BINARY) ./cmd/opensbt
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

# Build the opensbt binary
build-opensbt:
	@echo "Building $(OPENSBT_BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(OPENSBT_BINARY) ./cmd/opensbt
	@echo "✓ Build complete: $(BUILD_DIR)/$(OPENSBT_BINARY)"

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

# Build the API binary
build-api:
	@echo "Building $(API_BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(API_BINARY_NAME) ./cmd/zero-ops-api
	@echo "✓ Build complete: $(BUILD_DIR)/$(API_BINARY_NAME)"

# Run the API server
run: build-api
	@echo "Starting $(API_BINARY_NAME)..."
	@./$(BUILD_DIR)/$(API_BINARY_NAME)
