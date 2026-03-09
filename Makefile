.PHONY: build clean test install sqlc-generate migrate-up migrate-down run

# Build variables
BINARY_NAME=zero-ops
API_BINARY_NAME=zero-ops-api
BUILD_DIR=bin
GO=go
DATABASE_URL?=postgres://localhost:5432/zeroops?sslmode=disable

# Build the CLI binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) cmd/zero-ops/main.go
	@echo "✓ Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

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
	$(GO) build -o $(BUILD_DIR)/$(API_BINARY_NAME) cmd/zero-ops-api/main.go
	@echo "✓ Build complete: $(BUILD_DIR)/$(API_BINARY_NAME)"

# Run the API server
run: build-api
	@echo "Starting $(API_BINARY_NAME)..."
	@./$(BUILD_DIR)/$(API_BINARY_NAME)
