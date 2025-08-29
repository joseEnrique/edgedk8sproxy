# Makefile for Multiplexer Server
# Build from macOS for multiple architectures

# Variables
# Server
BINARY_NAME=server
BUILD_DIR=build
DOCKER_IMAGE_SERVER=quixpublic.azurecr.io/servermultiplexer
DOCKER_TAG=latest
# Agent
AGENT_BINARY=agent
DOCKER_IMAGE_AGENT=quixpublic.azurecr.io/agentmultiplexer

# Go build flags
LDFLAGS=-ldflags "-s -w"
CGO_ENABLED=0

# Build targets
.PHONY: all clean build build-all docker docker-push help agent-build agent-build-all agent-docker agent-docker-push docker-multi docker-multi-push push-all

# Default target
all: build

# Help
help:
	@echo "Available targets:"
	@echo "  build        - Build for current platform"
	@echo "  build-all    - Build server for AMD64/ARM64 (linux/amd64,linux/arm64,darwin/amd64,darwin/arm64)"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker       - Build server Docker image with buildx (linux/amd64)"
	@echo "  docker-multi - Build multi-platform server image (linux/amd64,linux/arm64)"
	@echo "  docker-push  - Build and push multi-arch server image with buildx"
	@echo "  agent-build  - Build agent binary for current platform"
	@echo "  agent-build-all - Build agent for AMD64/ARM64 (linux/amd64,linux/arm64,darwin/amd64,darwin/arm64)"
	@echo "  agent-docker - Build agent Docker image with buildx (linux/amd64)"
	@echo "  agent-docker-multi - Build multi-platform agent image (linux/amd64,linux/arm64)"
	@echo "  agent-docker-push - Build and push multi-arch agent image with buildx"
	@echo "  push-all     - Build and push server+agent multi-arch images"
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
	@echo "Building for linux/arm64..."
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./server
	@echo "Building for darwin/amd64..."
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./server
	@echo "Building for darwin/arm64..."
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./server
	@echo "✅ Built AMD64 binaries in $(BUILD_DIR)/"

# Build Docker image with buildx
docker:
	@echo "🐳 Building Docker image with buildx..."
	@docker buildx build --platform linux/amd64 -t $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG) ./server
	@echo "✅ Docker image built: $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG)"

# Build multi-platform Docker image with buildx
docker-multi:
	@echo "🐳 Building multi-platform Docker image with buildx..."
	@docker buildx build --platform linux/amd64,linux/arm64 -t $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG) ./server
	@echo "✅ Multi-platform Docker image built: $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG)"

# Build and push Docker image with buildx
docker-push:
	@echo "📤 Building and pushing Docker image with buildx..."
	@docker buildx build --platform linux/amd64,linux/arm64 --push -t $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG) ./server
	@echo "✅ Docker image built and pushed (multi-arch): $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG)"

# Agent build
agent-build: clean
	@echo "🔨 Building agent for current platform..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(AGENT_BINARY) ./agent
	@echo "✅ Built: $(BUILD_DIR)/$(AGENT_BINARY)"

agent-build-all: clean
	@echo "🔨 Building agent for AMD64/ARM64 platforms..."
	@mkdir -p $(BUILD_DIR)
	@echo "Building agent for linux/amd64..."
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(AGENT_BINARY)-linux-amd64 ./agent
	@echo "Building agent for linux/arm64..."
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(AGENT_BINARY)-linux-arm64 ./agent
	@echo "Building agent for darwin/amd64..."
	@GOOS=darwin GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(AGENT_BINARY)-darwin-amd64 ./agent
	@echo "Building agent for darwin/arm64..."
	@GOOS=darwin GOARCH=arm64 CGO_ENABLED=$(CGO_ENABLED) go build $(LDFLAGS) -o $(BUILD_DIR)/$(AGENT_BINARY)-darwin-arm64 ./agent
	@echo "✅ Built agent multi-arch binaries in $(BUILD_DIR)/"

# Agent Docker image
agent-docker:
	@echo "🐳 Building Agent Docker image with buildx..."
	@docker buildx build --platform linux/amd64 -t $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG) ./agent
	@echo "✅ Agent image built: $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG)"

agent-docker-multi:
	@echo "🐳 Building Agent multi-platform image with buildx..."
	@docker buildx build --platform linux/amd64,linux/arm64 -t $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG) ./agent
	@echo "✅ Agent multi-arch image built: $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG)"

agent-docker-push:
	@echo "📤 Building and pushing Agent image with buildx..."
	@docker buildx build --platform linux/amd64,linux/arm64 --push -t $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG) ./agent
	@echo "✅ Agent image built and pushed (multi-arch): $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG)"

# Convenience target to push both images multi-arch
push-all:
	@echo "📤 Building and pushing server and agent multi-arch images..."
	@docker buildx build --platform linux/amd64,linux/arm64 --push -t $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG) ./server
	@docker buildx build --platform linux/amd64,linux/arm64 --push -t $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG) ./agent
	@echo "✅ Pushed: $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG) and $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG)"

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
	@echo "  Server Image: $(DOCKER_IMAGE_SERVER):$(DOCKER_TAG)"
	@echo "  Agent  Image: $(DOCKER_IMAGE_AGENT):$(DOCKER_TAG)"
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