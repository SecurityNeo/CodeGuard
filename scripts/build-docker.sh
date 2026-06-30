#!/bin/bash
# AI Code Optimizer Docker 构建脚本
# 使用方法: ./scripts/build-docker.sh [VERSION] [TAG]

set -e

VERSION=${1:-1.2.0}
TAG=${2:-latest}
BUILD_TIME=$(date +%Y-%m-%dT%H:%M:%S)
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

echo "=========================================="
echo "Building AI Code Optimizer Docker Image"
echo "=========================================="
echo "Version:    $VERSION"
echo "Tag:        $TAG"
echo "Build Time: $BUILD_TIME"
echo "Git Commit: $GIT_COMMIT"
echo ""

# 检查是否已经编译
cd "$(dirname "$0")/.."

if [ ! -f backend/codeguard ]; then
    echo "Error: backend/codeguard not found!"
    echo "Please compile first:"
    echo "  cd backend && go build -o codeguard ./cmd/main.go"
    exit 1
fi

# 使用快速构建模式（本地预编译二进制）
echo "Building image using pre-compiled binary..."
docker build \
    -f Dockerfile.quick \
    --build-arg VERSION="$VERSION" \
    --build-arg BUILD_TIME="$BUILD_TIME" \
    --build-arg GIT_COMMIT="$GIT_COMMIT" \
    -t "codeguard:$TAG" \
    -t "codeguard:v$VERSION" \
    .

echo ""
echo "=========================================="
echo "Build Complete!"
echo "=========================================="
echo ""
echo "Images created:"
echo "  codeguard:$TAG"
echo "  codeguard:v$VERSION"
echo ""
echo "Run with:"
echo "  docker run -d -p 8080:8080 \\"
echo "    -e DB_PASSWORD=your_password \\"
echo "    -e ENCRYPTION_KEY=your_32byte_key \\"
echo "    -e DB_HOST=your_db_host \\"
echo "    codeguard:$TAG"
echo ""
