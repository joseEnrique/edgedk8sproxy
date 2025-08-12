# Makefile for Multiplexer Server
# Build from macOS for multiple architectures

# Variables
BINARY_NAME=server
BUILD_DIR=build
DOCKER_IMAGE=joseenrique/servermultplexer
DOCKER_TAG=latest

# Go build flags
LDFLAGS=-ldflags "-s -w"
CGO_ENABLED=0

# Build targets
.PHONY: all clean build build-all docker docker-push help

# Default target
all: build

# Help
help:
	@echo "Available targets:"
	@echo "  build        - Build for current platform"
	@echo "  build-all    - Build for AMD64 platforms (linux/amd64, darwin/amd64)"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker       - Build Docker image with buildx (linux/amd64)"
	@echo "  docker-multi - Build multi-platform Docker image (linux/amd64,linux/arm64)"
	@echo "  docker-push  - Build and push Docker image with buildx"
	@echo "  test         - Run tests"
	@echo "  run          - Run server locally"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@go clean

# Build for current platform
build: clean
	@echo "🔨 Building for current platform..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./server
	@echo "✅ Built: $(BUILD_DIR)/$(BINARY_NAME)"

# Build for AMD64 platforms
build-all: clean
	@echo "🔨 Building for AMD64 platforms..."
	@mkdir -p $(BUILD_DIR)
	
	@echo "Building for linux/amd64..."
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./server
	
	@echo "Building for darwin/amd64..."
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./server
	
	@echo "✅ Built AMD64 binaries in $(BUILD_DIR)/"

# Build Docker image with buildx
docker:
	@echo "🐳 Building Docker image with buildx..."
	@docker buildx build --platform linux/amd64 -t $(DOCKER_IMAGE):$(DOCKER_TAG) ./server
	@echo "✅ Docker image built: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Build multi-platform Docker image with buildx
docker-multi:
	@echo "🐳 Building multi-platform Docker image with buildx..."
	@docker buildx build --platform linux/amd64,linux/arm64 -t $(DOCKER_IMAGE):$(DOCKER_TAG) ./server
	@echo "✅ Multi-platform Docker image built: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Build and push Docker image with buildx
docker-push:
	@echo "📤 Building and pushing Docker image with buildx..."
	@docker buildx build --platform linux/amd64 --push -t $(DOCKER_IMAGE):$(DOCKER_TAG) ./server
	@echo "✅ Docker image built and pushed: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Run tests
test:
	@echo "🧪 Running tests..."
	@cd server && go test -v ./...

# Run server locally
run: build
	@echo "🚀 Running server locally..."
	@./$(BUILD_DIR)/$(BINARY_NAME)

# Install dependencies
deps:
	@echo "📦 Installing dependencies..."
	@cd server && go mod download
	@echo "✅ Dependencies installed"

# Format code
fmt:
	@echo "🎨 Formatting code..."
	@go fmt ./server/...
	@echo "✅ Code formatted"

# Lint code
lint:
	@echo "🔍 Linting code..."
	@golangci-lint run ./server/
	@echo "✅ Code linted"

# Show build info
info:
	@echo "📊 Build Information:"
	@echo "  Binary Name: $(BINARY_NAME)"
	@echo "  Build Directory: $(BUILD_DIR)"
	@echo "  Docker Image: $(DOCKER_IMAGE):$(DOCKER_TAG)"
	@echo "  Go Version: $(shell go version)"
	@echo "  Architecture: $(shell uname -m)"
	@echo "  OS: $(shell uname -s)" 

# Testing commands
test-server:
	@echo "🧪 Testing server endpoints..."
	@curl -s http://localhost:8080/test | jq '.' || echo "Server not running or jq not installed"

test-websocket:
	@echo "🔌 Testing WebSocket (5 second timeout)..."
	@timeout 5s curl -i -N -H "Connection: Upgrade" \
		-H "Upgrade: websocket" \
		-H "Sec-WebSocket-Version: 13" \
		-H "Sec-WebSocket-Key: x3JJHMbDL1EzLkh9GBhXDw==" \
		"http://localhost:8080/ws?id=test-client" || echo "WebSocket test completed (timeout)"

test-kubectl:
	@echo "🔌 Testing kubectl endpoint..."
	@curl -s -X POST http://localhost:8080/kubectl \
		-H "Content-Type: application/json" \
		-d '{"client_id": "test", "port": 8081}' || echo "kubectl endpoint not working"

test-all: test-server test-websocket test-kubectl
	@echo "🎉 All tests completed!" 