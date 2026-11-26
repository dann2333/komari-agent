#!/bin/sh
# 发布产物的统一构建入口：各个工作流和 build_all.sh 都调它，garble 的参数只写在这里。
#
# 用 garble 去掉二进制里认得出这个项目的痕迹：模块路径、函数名、文件名，以及字符串（-literals）；
# -tiny 再去掉位置信息、构建信息和 panic 的堆栈。只混淆项目自己的包，标准库和第三方依赖不动
# （全部包一起混淆的话，-literals 会让体积涨到三倍）。
#
# garble 只认它自己被编译时用的那个 Go 版本，所以 Go 和 garble 必须配套安装：
#   go install mvdan.cc/garble@v0.18.0    用 Go 1.27.x 装，装出来的 garble 也只配 1.27.x
# 用法: release-build.sh <goos> <goarch> <version> <输出文件> [agent|switcher] ["额外的 ldflags"]
set -eu

GARBLE_VERSION=v0.18.0
# 只混淆项目的包。主包要单列，/*/ 再覆盖所有子包；garble v0.18 不认只有 /... 的写法。
export GOGARBLE=github.com/komari-monitor/komari-agent,github.com/komari-monitor/komari-agent/*

if [ $# -lt 4 ]; then
    echo "usage: $0 <goos> <goarch> <version> <output> [agent|switcher] [\"extra ldflags\"]" >&2
    exit 2
fi
goos=$1
goarch=$2
version=$3
out=$4
target=${5:-agent}
extra=${6:-}

if ! command -v garble >/dev/null 2>&1; then
    PATH="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin:$PATH"
    export PATH
fi
if ! command -v garble >/dev/null 2>&1; then
    echo "garble not found; install it with Go 1.27.x: go install mvdan.cc/garble@$GARBLE_VERSION" >&2
    exit 1
fi

case "$target" in
    agent)    package=.          ; version_flag="-X github.com/komari-monitor/komari-agent/update.CurrentVersion=$version" ;;
    switcher) package=./switcher ; version_flag="-X main.buildVersion=$version" ;;
    *) echo "unknown target: $target (agent or switcher)" >&2; exit 2 ;;
esac

export GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0
# shellcheck disable=SC2086
garble -literals -tiny build -trimpath -ldflags="-s -w $version_flag $extra" -o "$out" "$package"
