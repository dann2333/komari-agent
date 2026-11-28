#!/usr/bin/env bash
# 用 toolchain.sh 做出的工具链编老系统 Windows 产物。资产名后缀 -legacy 由 -X 注入，
# 这条线的自更新只会找 komari-agent-windows-<arch>-legacy.exe。
#   bash build.sh <工具链根> <输出文件> [GOARCH=amd64] [版本=dev]
set -euo pipefail
TC=${1:?patched toolchain root}
OUT=${2:?output file}
ARCH=${3:-amd64}
VERSION=${4:-dev}
HERE=$(cd "$(dirname "$0")" && pwd)
export GOROOT="$TC" PATH="$TC/bin:$PATH" GOTOOLCHAIN=local
export CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH"
export GOGARBLE=github.com/komari-monitor/komari-agent,github.com/komari-monitor/komari-agent/*
cd "$HERE/.."
garble -literals -tiny build -trimpath \
  -ldflags="-s -w -X github.com/komari-monitor/komari-agent/update.CurrentVersion=${VERSION} -X github.com/komari-monitor/komari-agent/update.legacyAssetSuffix=-legacy" \
  -o "$OUT" .
ls -la "$OUT"
