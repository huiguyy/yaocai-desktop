#!/bin/bash
# Build Go Spider for Windows (cross-compile from Mac/Linux)
set -e

export PATH=$HOME/go-sdk/go/bin:$PATH
cd "$(dirname "$0")"

VERSION=${1:-3.0.0}
OUTPUT="spider-go.exe"

echo "🕷️ Building yaocai-spider-go v${VERSION} for Windows amd64..."

# Cross-compile for Windows
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o "${OUTPUT}" \
    ./cmd/spider/

if [ $? -eq 0 ]; then
    SIZE=$(ls -lh "${OUTPUT}" | awk '{print $5}')
    echo "✅ Build successful: ${OUTPUT} (${SIZE})"
    echo "📋 Run on Windows: spider-go.exe -port 14391"
else
    echo "❌ Build failed!"
    exit 1
fi
