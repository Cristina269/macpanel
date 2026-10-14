#!/bin/bash
# MacPanel 构建脚本
set -e
cd "$(dirname "$0")"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
go vet ./...
go build -trimpath -ldflags "-s -w" -o macpanel .
echo "构建完成: $(pwd)/macpanel ($(du -h macpanel | cut -f1))"
