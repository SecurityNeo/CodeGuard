.PHONY: build build-linux build-linux-arm64 run test docker docker-build docker-push clean fmt install dev deps lint vet package release migrate help

APP_NAME=codeguard
BUILD_DIR=build
DOCKER_IMAGE=codeguard:latest
DOCKER_REGISTRY?=

# 版本信息（可通过环境变量覆盖）
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%Y-%m-%dT%H:%M:%S)
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 编译参数
LDFLAGS=-ldflags "-w -s \
    -X main.Version=$(VERSION) \
    -X main.BuildTime=$(BUILD_TIME) \
    -X main.GitCommit=$(GIT_COMMIT)"

# 默认目标
.DEFAULT_GOAL := build

# ========== 开发命令 ==========

dev:
	go run $(LDFLAGS) ./cmd/main.go

run: dev

deps:
	go mod download
	go mod tidy
	go mod verify

fmt:
	go fmt ./...

lint:
	golangci-lint run ./... || true

vet:
	go vet ./...

test:
	go test -v -race ./...

# ========== 编译命令 ==========

# 本地编译（当前平台）
build: clean
	@echo "Building $(APP_NAME) v$(VERSION) ($(GIT_COMMIT))..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME) ./cmd/main.go
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME)"
	@echo "File size: $$(ls -lh $(BUILD_DIR)/$(APP_NAME) | awk '{print $$5}')"

# Linux AMD64 交叉编译
build-linux: clean
	@echo "Building $(APP_NAME) for Linux AMD64..."
	@mkdir -p $(BUILD_DIR)/linux-amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) \
		-o $(BUILD_DIR)/linux-amd64/$(APP_NAME) ./cmd/main.go
	@echo "Linux build complete: $(BUILD_DIR)/linux-amd64/$(APP_NAME)"

# Linux ARM64 交叉编译（用于服务器部署）
build-linux-arm64: clean
	@echo "Building $(APP_NAME) for Linux ARM64..."
	@mkdir -p $(BUILD_DIR)/linux-arm64
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) \
		-o $(BUILD_DIR)/linux-arm64/$(APP_NAME) ./cmd/main.go
	@echo "Linux ARM64 build complete."

# 全平台编译
build-all: build build-linux build-linux-arm64
	@echo "All platforms built successfully."

# ========== 打包命令 ==========

# 打包（包含前端静态文件）
package: build
	@echo "Packaging with static files..."
	@mkdir -p $(BUILD_DIR)/package
	cp $(BUILD_DIR)/$(APP_NAME) $(BUILD_DIR)/package/
	cp -r ../prototype $(BUILD_DIR)/package/
	cp Dockerfile $(BUILD_DIR)/package/ 2>/dev/null || true
	cp .env.example $(BUILD_DIR)/package/ 2>/dev/null || true
	@echo "Package created at: $(BUILD_DIR)/package/"

# 创建发布压缩包
release: build-linux
	@echo "Creating release archive..."
	@mkdir -p $(BUILD_DIR)/release
	cp $(BUILD_DIR)/linux-amd64/$(APP_NAME) $(BUILD_DIR)/release/
	cp -r ../prototype $(BUILD_DIR)/release/
	cp .env.example $(BUILD_DIR)/release/ 2>/dev/null || true
	cp ../docs/功能清单-FINAL.md $(BUILD_DIR)/release/ 2>/dev/null || true
	cd $(BUILD_DIR)/release && tar -czf ../$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz .
	@echo "Release archive: $(BUILD_DIR)/$(APP_NAME)-$(VERSION)-linux-amd64.tar.gz"

# ========== Docker 命令 ==========

docker-build:
	@echo "Building Docker image (context: project root)..."
	@cd .. && docker build -f backend/Dockerfile -t $(APP_NAME):$(VERSION) -t $(APP_NAME):latest .
	@echo "Docker image built: $(APP_NAME):$(VERSION)"

docker: docker-build

docker-push: docker-build
	@if [ -n "$(DOCKER_REGISTRY)" ]; then \
		docker tag $(APP_NAME):$(VERSION) $(DOCKER_REGISTRY)/$(APP_NAME):$(VERSION); \
		docker tag $(APP_NAME):latest $(DOCKER_REGISTRY)/$(APP_NAME):latest; \
		docker push $(DOCKER_REGISTRY)/$(APP_NAME):$(VERSION); \
		docker push $(DOCKER_REGISTRY)/$(APP_NAME):latest; \
		echo "Pushed to $(DOCKER_REGISTRY)"; \
	else \
		echo "DOCKER_REGISTRY not set, skipping push"; \
	fi

docker-run:
	@if [ -f .env ]; then \
		docker run -p 8080:8080 --env-file .env $(APP_NAME):latest; \
	else \
		docker run -p 8080:8080 $(APP_NAME):latest; \
	fi

# ========== 安装命令 ==========

install: build
	@echo "Installing to /usr/local/bin/$(APP_NAME)..."
	@cp $(BUILD_DIR)/$(APP_NAME) /usr/local/bin/$(APP_NAME) || \
		echo "Warning: Need sudo to install to /usr/local/bin"
	@echo "Install complete. Run '$(APP_NAME)' to start."

# ========== 清理命令 ==========

clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@go clean -cache 2>/dev/null || true
	@echo "Clean complete."

# ========== 数据库迁移 ==========

migrate:
	go run $(LDFLAGS) ./cmd/main.go

# ========== 帮助 ==========

help:
	@echo "CodeGuard - Makefile"
	@echo ""
	@echo "Development Commands:"
	@echo "  make dev          - Run in development mode"
	@echo "  make run          - Alias for make dev"
	@echo "  make deps         - Download dependencies"
	@echo "  make fmt          - Format Go code"
	@echo "  make lint         - Run linter"
	@echo "  make test         - Run tests"
	@echo ""
	@echo "Build Commands:"
	@echo "  make build        - Build for current platform"
	@echo "  make build-linux  - Cross-compile for Linux AMD64"
	@echo "  make build-all    - Build for all platforms"
	@echo ""
	@echo "Package Commands:"
	@echo "  make package      - Package with static files"
	@echo "  make release      - Create release tarball"
	@echo ""
	@echo "Docker Commands:"
	@echo "  make docker           - Build Docker image"
	@echo "  make docker-run       - Run Docker container"
	@echo "  make docker-push      - Push to registry (set DOCKER_REGISTRY)"
	@echo ""
	@echo "Other Commands:"
	@echo "  make install      - Install binary to /usr/local/bin"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make help         - Show this help"
