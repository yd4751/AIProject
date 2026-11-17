#!/usr/bin/env bash
# frptun 多平台构建脚本 (macOS / Linux)
set -e

OUT="dist"
VERSION="1.0.0"
mkdir -p "$OUT"

build() {
  local os=$1 arch=$2
  local name="frptun-${os}-${arch}"
  [ "$os" = "windows" ] && name="${name}.exe"
  echo ">>> building $name"
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w" -o "$OUT/$name" ./cmd/frptun
}

build linux amd64
build linux arm64
build windows amd64
build darwin amd64
build darwin arm64

echo "=== 构建完成 ==="
ls -lh "$OUT"
