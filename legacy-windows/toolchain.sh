#!/usr/bin/env bash
# 从一份原版 Go 1.24.x 做出能在 Windows 7 / Server 2008 R2 上运行产物的工具链。
# 做法是给 runtime 加回退（见 patch.py），然后用原版当 bootstrap 重新编译一遍。
#   bash toolchain.sh <原版 GOROOT> <目标目录>
# 要求：python3；目标机器上能拉 Go 模块（装 garble）。
set -euo pipefail
STOCK=${1:?stock GOROOT}
DEST=${2:?destination dir}
HERE=$(cd "$(dirname "$0")" && pwd)
# garble 只认它编译时用的 Go；v0.14.2 是最后一个支持 1.24 的版本
GARBLE_VERSION=v0.14.2

rm -rf "$DEST"
cp -a "$STOCK" "$DEST"
# 从模块代理下下来的工具链 zip 会把 src/go.mod 改名成 _go.mod
if [ ! -f "$DEST/src/go.mod" ] && [ -f "$DEST/src/_go.mod" ]; then
  cp "$DEST/src/_go.mod" "$DEST/src/go.mod"
fi
chmod +x "$DEST"/bin/* "$DEST"/src/*.bash "$DEST"/pkg/tool/*/* 2>/dev/null || true

python3 "$HERE/patch.py" --root "$DEST"
( cd "$DEST/src" && GOROOT_BOOTSTRAP="$STOCK" GOTOOLCHAIN=local ./make.bash >/dev/null )

export GOROOT="$DEST" PATH="$DEST/bin:$PATH" GOTOOLCHAIN=local
GOBIN="$DEST/bin" go install "mvdan.cc/garble@$GARBLE_VERSION"
"$DEST/bin/go" version
"$DEST/bin/garble" version | head -1
