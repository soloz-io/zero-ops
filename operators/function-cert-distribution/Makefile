# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/soloz-io/zero-ops/function-cert-distribution:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
GOBIN ?= $(shell go env GOBIN)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Build function binary
build:
	go build -o bin/function-cert-distribution .

# Build function binary for multiple platforms
build-all:
	GOOS=linux GOARCH=amd64 go build -o bin/function-cert-distribution-linux-amd64 .
	GOOS=darwin GOARCH=amd64 go build -o bin/function-cert-distribution-darwin-amd64 .
	GOOS=windows GOARCH=amd64 go build -o bin/function-cert-distribution-windows-amd64.exe .

# Build Docker image
docker-build:
	docker build -t ${IMG} .

# Push Docker image
docker-push:
	docker push ${IMG}

# Build and push Docker image
docker-build-push: docker-build docker-push

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Run linter (if golangci-lint is installed)
lint:
	golangci-lint run

# Run all checks
check: fmt vet test

# Update dependencies
deps-update:
	go get -u ./...
	go mod tidy

# Download dependencies
deps:
	go mod download

# Generate documentation (if applicable)
docs:
	@echo "No documentation generation configured"

# Install function locally (for development)
install: build
	cp bin/function-cert-distribution ${GOBIN}/

.PHONY: test test-coverage build build-all docker-build docker-push docker-build-push clean fmt vet lint check deps-update deps docs install