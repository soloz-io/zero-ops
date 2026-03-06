.PHONY: build clean test install

# Build variables
BINARY_NAME=zero-ops
BUILD_DIR=bin
GO=go

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
