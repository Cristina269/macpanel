#!/bin/bash
# MacPanel build script
set -e
cd "$(dirname "$0")"
# China mirror by default; override with GOPROXY=https://proxy.golang.org,direct elsewhere
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
go vet ./...
go build -trimpath -ldflags "-s -w" -o macpanel .
echo "Build OK: $(pwd)/macpanel ($(du -h macpanel | cut -f1))"
