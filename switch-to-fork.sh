#!/bin/sh

# ===========================================================================
#  komari-agent 一键切换脚本
#  把已经装好的官方版 agent 换成本仓库构建的版本, 保留原有的 endpoint/token,
#  并且可以在切换的同时调整启动参数。
#
#  用法:
#    sudo sh switch-to-fork.sh                  # 交互式, 会让你确认和改参数
#    sudo sh switch-to-fork.sh --yes            # 全自动, 直接用下面的默认配置
#    sudo sh switch-to-fork.sh --dry-run        # 只演示不改动, 用来预览
#    sudo sh switch-to-fork.sh --revert         # 回滚到切换前的二进制和服务配置
#    curl -fsSL <raw-url> | sudo sh             # 一键 (交互仍然可用, 走 /dev/tty)
#    curl -fsSL <raw-url> | sudo sh -s -- --yes # 一键且不交互
# ===========================================================================

# ============================= 可配置选项 =================================
# 这些都可以用同名环境变量覆盖, 例如:
#   KOMARI_ADD_FLAGS="--disable-security-warning" sh switch-to-fork.sh --yes

# 目标仓库, 形如 owner/name
TARGET_REPO="${KOMARI_TARGET_REPO:-dann2333/komari-agent}"

# 目标版本: auto = 优先正式版, 没有正式版就用最新快照
#           latest = 只用正式版; snapshot = 只用最新快照; 也可以直接写 tag
TARGET_VERSION="${KOMARI_TARGET_VERSION:-auto}"

# 服务名, 留空则自动探测 (默认会先找 komari-agent)
SERVICE_NAME="${KOMARI_SERVICE_NAME:-}"

# GitHub 加速前缀, 例如 https://ghfast.top, 留空表示直连
GITHUB_PROXY="${KOMARI_GITHUB_PROXY:-}"

# true = 直连失败后不再尝试内置的加速镜像
NO_MIRROR="${KOMARI_NO_MIRROR:-false}"

# 交互模式: auto = 有终端就交互; yes = 强制交互; no = 不交互
INTERACTIVE="${KOMARI_INTERACTIVE:-auto}"

# 切换时自动追加的参数 (空格分隔), 例: "--disable-security-warning"
ADD_FLAGS="${KOMARI_ADD_FLAGS:-}"

# 切换时自动移除的参数 (空格分隔), 例: "--disable-web-ssh"
REMOVE_FLAGS="${KOMARI_REMOVE_FLAGS:-}"

# true = 保留备份目录, 方便之后 --revert
KEEP_BACKUP="${KOMARI_KEEP_BACKUP:-true}"
# ==========================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
NC='\033[0m'

log_info()    { printf '%b\n' "${NC} $1"; }
log_success() { printf '%b\n' "${GREEN}[OK]${NC} $1"; }
log_warning() { printf '%b\n' "${YELLOW}[WARNING]${NC} $1"; }
log_error()   { printf '%b\n' "${RED}[ERROR]${NC} $1"; }
log_step()    { printf '%b\n' "${CYAN}==>${NC} $1"; }
log_config()  { printf '%b\n' "${CYAN}[CONFIG]${NC} $1"; }

EUID=${EUID:-$(id -u)}
DRY_RUN=false
REVERT=false

usage() {
    cat <<'USAGE'
komari-agent 切换脚本

  --repo <owner/name>     目标仓库 (默认 dann2333/komari-agent)
  --version <ver>         auto | latest | snapshot | 具体 tag
  --service-name <name>   指定服务名, 默认自动探测
  --ghproxy <prefix>      GitHub 加速前缀
  --no-mirror             直连失败时不再尝试内置镜像
  --add-flag <flag>       切换时追加的参数, 可重复
  --remove-flag <flag>    切换时移除的参数, 可重复
  -y, --yes               不交互, 直接按配置执行
  -i, --interactive       强制交互 (即使从管道运行)
  --dry-run               只显示将要做什么, 不改动任何东西
  --revert                回滚到切换前的二进制和服务配置
  -h, --help              显示本帮助
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --repo)          TARGET_REPO="$2"; shift 2 ;;
        --version)       TARGET_VERSION="$2"; shift 2 ;;
        --service-name)  SERVICE_NAME="$2"; shift 2 ;;
        --ghproxy)       GITHUB_PROXY="$2"; shift 2 ;;
        --no-mirror)     NO_MIRROR=true; shift ;;
        --add-flag)      ADD_FLAGS="$ADD_FLAGS $2"; shift 2 ;;
        --remove-flag)   REMOVE_FLAGS="$REMOVE_FLAGS $2"; shift 2 ;;
        -y|--yes)        INTERACTIVE=no; shift ;;
        -i|--interactive) INTERACTIVE=yes; shift ;;
        --dry-run)       DRY_RUN=true; shift ;;
        --revert)        REVERT=true; shift ;;
        -h|--help)       usage; exit 0 ;;
        *)               log_error "未知参数: $1"; usage; exit 1 ;;
    esac
done

# --------------------------- 参数串处理 ------------------------------------
# 参数统一按空格分隔的字符串保存, 与 install.sh 写进服务文件的格式保持一致。

flag_has() {
    for _t in $1; do
        [ "$_t" = "$2" ] && return 0
        case "$_t" in "$2"=*) return 0 ;; esac
    done
    return 1
}

flag_value() {
    _want=0
    for _t in $1; do
        if [ "$_want" = 1 ]; then
            case "$_t" in
                -*) return 1 ;;
                *) printf '%s' "$_t"; return 0 ;;
            esac
        fi
        [ "$_t" = "$2" ] && _want=1
        case "$_t" in "$2"=*) printf '%s' "${_t#*=}"; return 0 ;; esac
    done
    return 1
}

flag_unset() {
    _out=""
    _drop_value=0
    for _t in $1; do
        if [ "$_drop_value" = 1 ]; then
            _drop_value=0
            case "$_t" in
                -*) ;;
                *) continue ;;
            esac
        fi
        if [ "$_t" = "$2" ]; then
            _drop_value=1
            continue
        fi
        case "$_t" in "$2"=*) continue ;; esac
        _out="$_out $_t"
    done
    printf '%s' "${_out# }"
}

flag_set() {
    _rest=$(flag_unset "$1" "$2")
    if [ -n "$3" ]; then
        printf '%s' "${_rest:+$_rest }$2 $3"
    else
        printf '%s' "${_rest:+$_rest }$2"
    fi
}

mask_secret() {
    _v="$1"
    _len=$(printf '%s' "$_v" | wc -c | tr -d ' ')
    if [ "$_len" -le 8 ]; then
        printf '********'
    else
        printf '%s****%s' "$(printf '%s' "$_v" | cut -c1-4)" "$(printf '%s' "$_v" | cut -c$((_len - 3))-)"
    fi
}

# 把参数串里的 token 打码后返回, 用于日志输出
mask_args() {
    _prev=""
    _out=""
    for _t in $1; do
        case "$_t" in
            --token=*|-t=*) _out="$_out ${_t%%=*}=$(mask_secret "${_t#*=}")" ;;
            *)
                case "$_prev" in
                    --token|-t) _out="$_out $(mask_secret "$_t")" ;;
                    *)          _out="$_out $_t" ;;
                esac
                ;;
        esac
        _prev="$_t"
    done
    printf '%s' "${_out# }"
}

print_args() {
    _prev=""
    _line=""
    for _t in $1; do
        case "$_t" in
            -*)
                [ -n "$_line" ] && echo "    $_line"
                _line="$_t"
                _prev="$_t"
                ;;
            *)
                case "$_prev" in
                    --token|-t) _line="$_line $(mask_secret "$_t")" ;;
                    *)          _line="$_line $_t" ;;
                esac
                echo "    $_line"
                _line=""
                _prev=""
                ;;
        esac
    done
    [ -n "$_line" ] && echo "    $_line"
    [ -z "$1" ] && echo "    (无)"
    return 0
}

# --------------------------- 服务探测与解析 --------------------------------
# 解析结果:
#   INIT_SYSTEM   systemd | systemd-user | openrc | procd | upstart | launchd
#   SERVICE_FILE  服务文件路径
#   SERVICE_NAME  服务名
#   AGENT_PATH    agent 二进制路径
#   CURRENT_ARGS  当前启动参数

INIT_SYSTEM=""
SERVICE_FILE=""
AGENT_PATH=""
CURRENT_ARGS=""

# 从服务文件里取出 "二进制路径 + 参数" 那一行的内容
extract_exec_line() {
    _file="$1"
    case "$INIT_SYSTEM" in
        systemd|systemd-user)
            sed -n 's/^ExecStart=[-@+!]*//p' "$_file" | head -n 1
            ;;
        openrc)
            _cmd=$(sed -n 's/^command="\(.*\)"$/\1/p' "$_file" | head -n 1)
            _cmd_args=$(sed -n 's/^command_args="\(.*\)"$/\1/p' "$_file" | head -n 1)
            printf '%s %s' "$_cmd" "$_cmd_args"
            ;;
        procd)
            _cmd=$(sed -n 's/^PROG="\(.*\)"$/\1/p' "$_file" | head -n 1)
            _cmd_args=$(sed -n 's/^ARGS="\(.*\)"$/\1/p' "$_file" | head -n 1)
            printf '%s %s' "$_cmd" "$_cmd_args"
            ;;
        upstart)
            sed -n 's/^[[:space:]]*exec[[:space:]]\{1,\}//p' "$_file" | head -n 1
            ;;
        launchd)
            awk '
                /<key>ProgramArguments<\/key>/ { inkey = 1; next }
                inkey && /<array>/ { inarray = 1; inkey = 0; next }
                inarray && /<\/array>/ { exit }
                inarray {
                    line = $0
                    sub(/^[[:space:]]*<string>/, "", line)
                    sub(/<\/string>[[:space:]]*$/, "", line)
                    printf "%s ", line
                }
            ' "$_file"
            ;;
    esac
}

parse_service_file() {
    _exec=$(extract_exec_line "$SERVICE_FILE")
    _exec=$(printf '%s' "$_exec" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    [ -z "$_exec" ] && return 1
    AGENT_PATH=$(printf '%s' "$_exec" | cut -d' ' -f1)
    CURRENT_ARGS=$(printf '%s' "$_exec" | cut -s -d' ' -f2-)
    CURRENT_ARGS=$(printf '%s' "$CURRENT_ARGS" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    [ -n "$AGENT_PATH" ]
}

# 按给定服务名在各种 init 系统里找服务文件
locate_service_by_name() {
    _name="$1"

    if [ -f "/etc/systemd/system/${_name}.service" ]; then
        INIT_SYSTEM="systemd"; SERVICE_FILE="/etc/systemd/system/${_name}.service"; return 0
    fi
    if [ -f "/lib/systemd/system/${_name}.service" ]; then
        INIT_SYSTEM="systemd"; SERVICE_FILE="/lib/systemd/system/${_name}.service"; return 0
    fi
    _user_unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/${_name}.service"
    if [ -f "$_user_unit" ]; then
        INIT_SYSTEM="systemd-user"; SERVICE_FILE="$_user_unit"; return 0
    fi
    if [ -f "/etc/init.d/${_name}" ]; then
        if grep -q "USE_PROCD" "/etc/init.d/${_name}" 2>/dev/null; then
            INIT_SYSTEM="procd"
        else
            INIT_SYSTEM="openrc"
        fi
        SERVICE_FILE="/etc/init.d/${_name}"; return 0
    fi
    if [ -f "/etc/init/${_name}.conf" ]; then
        INIT_SYSTEM="upstart"; SERVICE_FILE="/etc/init/${_name}.conf"; return 0
    fi
    for _plist in "/Library/LaunchDaemons/com.komari.${_name}.plist" \
                  "$HOME/Library/LaunchAgents/com.komari.${_name}.plist"; do
        if [ -f "$_plist" ]; then
            INIT_SYSTEM="launchd"; SERVICE_FILE="$_plist"; return 0
        fi
    done
    return 1
}

# 没指定服务名时, 扫描出所有看起来像 komari agent 的服务
discover_service() {
    if [ -n "$SERVICE_NAME" ]; then
        locate_service_by_name "$SERVICE_NAME" && return 0
        log_error "找不到名为 ${SERVICE_NAME} 的服务"
        return 1
    fi

    if locate_service_by_name "komari-agent"; then
        SERVICE_NAME="komari-agent"
        return 0
    fi

    log_step "未找到默认服务名, 开始扫描..."
    for _candidate in /etc/systemd/system/*.service \
                      "${XDG_CONFIG_HOME:-$HOME/.config}"/systemd/user/*.service \
                      /etc/init.d/* \
                      /etc/init/*.conf \
                      /Library/LaunchDaemons/com.komari.*.plist \
                      "$HOME"/Library/LaunchAgents/com.komari.*.plist; do
        [ -f "$_candidate" ] || continue
        grep -qi "komari" "$_candidate" 2>/dev/null || continue
        _base=$(basename "$_candidate")
        _base=${_base%.service}
        _base=${_base%.conf}
        _base=${_base%.plist}
        _base=${_base#com.komari.}
        if locate_service_by_name "$_base"; then
            SERVICE_NAME="$_base"
            log_info "扫描到服务: ${GREEN}${SERVICE_NAME}${NC}"
            return 0
        fi
    done
    return 1
}

service_ctl() {
    _action="$1"
    case "$INIT_SYSTEM" in
        systemd)      systemctl "$_action" "${SERVICE_NAME}.service" ;;
        systemd-user) systemctl --user "$_action" "${SERVICE_NAME}.service" ;;
        openrc)       rc-service "$SERVICE_NAME" "$_action" ;;
        procd)        "/etc/init.d/${SERVICE_NAME}" "$_action" ;;
        upstart)      initctl "$_action" "$SERVICE_NAME" ;;
        launchd)
            case "$_action" in
                stop)  launchctl bootout "$LAUNCHD_DOMAIN" "$SERVICE_FILE" 2>/dev/null || true ;;
                start) launchctl bootstrap "$LAUNCHD_DOMAIN" "$SERVICE_FILE" ;;
            esac
            ;;
    esac
}

service_is_running() {
    case "$INIT_SYSTEM" in
        systemd)      systemctl is-active --quiet "${SERVICE_NAME}.service" ;;
        systemd-user) systemctl --user is-active --quiet "${SERVICE_NAME}.service" ;;
        openrc)       rc-service "$SERVICE_NAME" status >/dev/null 2>&1 ;;
        procd)
            # 用 ^ 锚定命令行开头, 否则命令行里碰巧带有这个路径的其他进程
            # (比如正在跑的运维命令) 会让已经挂掉的服务被误判成还活着
            pgrep -f "^${AGENT_PATH}" >/dev/null 2>&1
            ;;
        upstart)      initctl status "$SERVICE_NAME" 2>/dev/null | grep -q "start/running" ;;
        launchd)      launchctl print "${LAUNCHD_DOMAIN}/com.komari.${SERVICE_NAME}" >/dev/null 2>&1 ;;
        *)            return 1 ;;
    esac
}

# --------------------------- 交互式参数编辑 --------------------------------

read_tty() {
    REPLY_VALUE=""
    if ! { printf '%s' "$1" > /dev/tty; } 2>/dev/null; then
        log_error "拿不到终端, 无法交互, 请改用 --yes 配合 --add-flag/--remove-flag"
        exit 1
    fi
    IFS= read -r REPLY_VALUE < /dev/tty || REPLY_VALUE=""
}

toggle_bool_flag() {
    if flag_has "$NEW_ARGS" "$1"; then
        NEW_ARGS=$(flag_unset "$NEW_ARGS" "$1")
        log_info "已移除 ${GREEN}$1${NC}"
    else
        NEW_ARGS=$(flag_set "$NEW_ARGS" "$1" "")
        log_info "已加上 ${GREEN}$1${NC}"
    fi
}

prompt_value_flag() {
    _flag="$1"
    _desc="$2"
    _current=$(flag_value "$NEW_ARGS" "$_flag") || _current=""
    read_tty "$_desc
  $_flag 当前值: ${_current:-(未设置)}
  输入新值 (留空表示删除该参数): "
    if [ -z "$REPLY_VALUE" ]; then
        NEW_ARGS=$(flag_unset "$NEW_ARGS" "$_flag")
        log_info "已移除 ${GREEN}$_flag${NC}"
    else
        NEW_ARGS=$(flag_set "$NEW_ARGS" "$_flag" "$REPLY_VALUE")
        log_info "已设置 ${GREEN}$_flag $REPLY_VALUE${NC}"
    fi
}

bool_state() {
    if flag_has "$NEW_ARGS" "$1"; then printf '已开启'; else printf '未开启'; fi
}

value_state() {
    _v=$(flag_value "$NEW_ARGS" "$1") || _v=""
    if [ -z "$_v" ]; then
        printf '未设置'
    else
        printf '%s' "$_v"
    fi
}

edit_flags_interactive() {
    while :; do
        echo ""
        printf '%b\n' "${WHITE}========== 切换后的启动参数 ==========${NC}"
        print_args "$NEW_ARGS"
        printf '%b\n' "${WHITE}=====================================${NC}"
        echo ""
        echo "  1) 开关 --disable-security-warning   当前: $(bool_state --disable-security-warning)   (禁用所有平台的安全警告提示)"
        echo "  2) 开关 --disable-auto-update        当前: $(bool_state --disable-auto-update)   (禁用自动更新)"
        echo "  3) 开关 --disable-web-ssh            当前: $(bool_state --disable-web-ssh)   (禁用远程控制)"
        echo "  4) 设置 --update-repo                当前: $(value_state --update-repo)   (自动更新的仓库, 留空则用二进制内置的 ${TARGET_REPO})"
        echo "  5) 设置 --update-api-url             当前: $(value_state --update-api-url)   (自建 GitHub 兼容 API)"
        echo "  6) 设置 --endpoint                   当前: $(value_state --endpoint)"
        echo "  7) 设置 --token"
        echo "  8) 设置 --interval                   当前: $(value_state --interval)   (采集间隔, 秒)"
        echo "  a) 手动添加/修改任意参数"
        echo "  d) 删除某个参数"
        echo "  e) 直接编辑整行参数"
        echo "  r) 还原成切换前的参数"
        echo ""
        read_tty "选择操作 (直接回车表示确认并继续, q 退出): "
        case "$REPLY_VALUE" in
            "")  return 0 ;;
            1)   toggle_bool_flag --disable-security-warning ;;
            2)   toggle_bool_flag --disable-auto-update ;;
            3)   toggle_bool_flag --disable-web-ssh ;;
            4)   prompt_value_flag --update-repo "自动更新使用的发布仓库, 形如 owner/name" ;;
            5)   prompt_value_flag --update-api-url "自动更新使用的 API 基地址, GitHub Enterprise 需填到 /api/v3" ;;
            6)   prompt_value_flag --endpoint "面板地址" ;;
            7)   prompt_value_flag --token "面板 token" ;;
            8)   prompt_value_flag --interval "数据采集间隔, 单位秒" ;;
            a|A)
                read_tty "要添加/修改的参数名 (含 --): "
                _name="$REPLY_VALUE"
                case "$_name" in
                    --*) ;;
                    *) log_warning "参数名需要以 -- 开头"; continue ;;
                esac
                read_tty "参数值 (布尔开关留空): "
                NEW_ARGS=$(flag_set "$NEW_ARGS" "$_name" "$REPLY_VALUE")
                ;;
            d|D)
                read_tty "要删除的参数名 (含 --): "
                [ -n "$REPLY_VALUE" ] && NEW_ARGS=$(flag_unset "$NEW_ARGS" "$REPLY_VALUE")
                ;;
            e|E)
                read_tty "输入完整的参数行: "
                NEW_ARGS=$(printf '%s' "$REPLY_VALUE" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
                ;;
            r|R)
                NEW_ARGS="$CURRENT_ARGS"
                log_info "已还原成切换前的参数"
                ;;
            q|Q)
                log_warning "已取消, 没有做任何改动"
                exit 0
                ;;
            *)
                log_warning "无法识别的选择: $REPLY_VALUE"
                ;;
        esac
    done
}

# --------------------------- 平台与下载 ------------------------------------

# 读取 ELF 头的 EI_DATA 字节判断字节序: 1 = 小端, 2 = 大端, 无法判断时输出空
elf_data_encoding() {
    for _probe in /bin/sh /proc/self/exe; do
        [ -r "$_probe" ] || continue
        _encoding=$(od -An -tu1 -j 5 -N 1 "$_probe" 2>/dev/null | tr -d ' \n')
        if [ "$_encoding" = "1" ] || [ "$_encoding" = "2" ]; then
            printf '%s' "$_encoding"
            return 0
        fi
    done
    return 1
}

detect_platform() {
    case "$(uname -s)" in
        Linux)   os_name="linux" ;;
        Darwin)  os_name="darwin" ;;
        FreeBSD) os_name="freebsd" ;;
        MINGW*|MSYS*|CYGWIN*) os_name="windows" ;;
        *) log_error "不支持的操作系统: $(uname -s)"; return 1 ;;
    esac

    arch=$(uname -m)
    case "$arch" in
        x86_64)            arch="amd64" ;;
        aarch64|arm64)     arch="arm64" ;;
        loongarch64|loong64) arch="loong64" ;;
        i386|i686)         arch="386" ;;
        armv7*|armv6*)     arch="arm" ;;
        riscv64|s390x|ppc64|ppc64le|mips|mipsel|mipsle|mips64|mips64el|mips64le)
            if [ "$os_name" != "linux" ]; then
                log_error "$arch 架构只发布 Linux 版本"
                return 1
            fi
            case "$arch" in
                mips|mips64)
                    _encoding=$(elf_data_encoding)
                    if [ "$_encoding" != "2" ]; then
                        [ -z "$_encoding" ] && log_warning "无法判断 MIPS 字节序, 按小端处理"
                        arch="${arch}le"
                    fi
                    ;;
                mipsel)    arch="mipsle" ;;
                mips64el)  arch="mips64le" ;;
            esac
            ;;
        *) log_error "不支持的架构: $arch"; return 1 ;;
    esac

    file_name="komari-agent-${os_name}-${arch}"
    [ "$os_name" = "windows" ] && file_name="${file_name}.exe"
    return 0
}

github_api() {
    # 出错信息自己给, 不让 curl 的原始报错糊到屏幕上
    curl -fsSL --connect-timeout 15 \
        -H "Accept: application/vnd.github+json" \
        -H "User-Agent: komari-agent-switch" "$1" 2>/dev/null
}

resolve_latest_release() {
    _json=$(github_api "https://api.github.com/repos/${TARGET_REPO}/releases/latest") || return 1
    printf '%s\n' "$_json" |
        grep -o '"tag_name":[[:space:]]*"[^"]*"' |
        sed 's/.*"\([^"]*\)"$/\1/' |
        head -n 1
}

resolve_snapshot_release() {
    _json=$(github_api "https://api.github.com/repos/${TARGET_REPO}/releases?per_page=100") || return 1
    printf '%s\n' "$_json" |
        grep -o '"tag_name":[[:space:]]*"Snapshot-[^"]*"' |
        sed 's/.*"\(Snapshot-[^"]*\)".*/\1/' |
        LC_ALL=C sort -r |
        head -n 1
}

no_release_hint() {
    log_error "取不到 ${TARGET_REPO} 的 $1"
    log_info "确认一下仓库名是否写对, 以及本机能否访问 GitHub API"
    log_info "网络受限的话可以用 --ghproxy <前缀> 指定加速地址"
}

resolve_version() {
    case "$TARGET_VERSION" in
        latest)
            resolved_version=$(resolve_latest_release)
            [ -n "$resolved_version" ] || { no_release_hint "正式版 release"; return 1; }
            ;;
        snapshot)
            resolved_version=$(resolve_snapshot_release)
            [ -n "$resolved_version" ] || { no_release_hint "快照版 release"; return 1; }
            ;;
        auto)
            resolved_version=$(resolve_latest_release)
            if [ -z "$resolved_version" ]; then
                log_warning "没有拿到 ${TARGET_REPO} 的正式版, 改找最新快照"
                resolved_version=$(resolve_snapshot_release)
                [ -n "$resolved_version" ] || { no_release_hint "任何 release"; return 1; }
            fi
            ;;
        *)
            resolved_version="$TARGET_VERSION"
            ;;
    esac
    return 0
}

download_agent() {
    _out="$1"
    _base="https://github.com/${TARGET_REPO}/releases/download/${resolved_version}/${file_name}"

    if [ -n "$GITHUB_PROXY" ]; then
        _urls="${GITHUB_PROXY}/${_base}"
    elif [ "$NO_MIRROR" = "true" ]; then
        _urls="$_base"
    else
        _urls="
${_base}
https://ghfast.top/${_base}
https://gh-proxy.com/${_base}
https://ghproxy.net/${_base}
"
    fi

    for _u in $_urls; do
        log_info "下载: ${CYAN}${_u}${NC}"
        if curl -fL --connect-timeout 15 -o "$_out" "$_u" && [ -s "$_out" ]; then
            chmod +x "$_out"
            return 0
        fi
        rm -f "$_out"
    done
    return 1
}

# --------------------------- 服务文件改写 ----------------------------------

# 用新行替换文件里第一处匹配, 通过覆盖写入保留原有权限和 inode
replace_first_line() {
    _file="$1"
    _pattern="$2"
    _newline="$3"
    _tmp="${TMPDIR:-/tmp}/komari-switch-$$.tmp"
    awk -v pattern="$_pattern" -v newline="$_newline" '
        !replaced && $0 ~ pattern { print newline; replaced = 1; next }
        { print }
    ' "$_file" > "$_tmp" || return 1
    cat "$_tmp" > "$_file" || { rm -f "$_tmp"; return 1; }
    rm -f "$_tmp"
}

write_launchd_args() {
    _file="$1"
    _block=$(
        printf '        <string>%s</string>\n' "$AGENT_PATH"
        for _t in $NEW_ARGS; do
            printf '        <string>%s</string>\n' "$_t"
        done
    )
    _tmp="${TMPDIR:-/tmp}/komari-switch-$$.plist"
    awk -v block="$_block" '
        /<key>ProgramArguments<\/key>/ { print; inkey = 1; next }
        inkey && /<array>/ { print; print block; inkey = 0; inarray = 1; next }
        inarray && /<\/array>/ { print; inarray = 0; next }
        inarray { next }
        { print }
    ' "$_file" > "$_tmp" || return 1
    cat "$_tmp" > "$_file" || { rm -f "$_tmp"; return 1; }
    rm -f "$_tmp"
}

apply_args_to_service() {
    [ "$NEW_ARGS" = "$CURRENT_ARGS" ] && return 0
    case "$INIT_SYSTEM" in
        systemd|systemd-user)
            replace_first_line "$SERVICE_FILE" '^ExecStart=' "ExecStart=${AGENT_PATH}${NEW_ARGS:+ $NEW_ARGS}"
            ;;
        openrc)
            replace_first_line "$SERVICE_FILE" '^command_args=' "command_args=\"${NEW_ARGS}\""
            ;;
        procd)
            replace_first_line "$SERVICE_FILE" '^ARGS=' "ARGS=\"${NEW_ARGS}\""
            ;;
        upstart)
            replace_first_line "$SERVICE_FILE" '^[[:space:]]*exec[[:space:]]' "    exec ${AGENT_PATH}${NEW_ARGS:+ $NEW_ARGS}"
            ;;
        launchd)
            write_launchd_args "$SERVICE_FILE"
            ;;
        *)
            log_error "不知道怎么改写 ${INIT_SYSTEM} 的服务文件"
            return 1
            ;;
    esac
}

reload_service_definition() {
    case "$INIT_SYSTEM" in
        systemd)      systemctl daemon-reload ;;
        systemd-user) systemctl --user daemon-reload ;;
        upstart)      initctl reload-configuration 2>/dev/null || true ;;
    esac
}

# --------------------------- 备份与回滚 ------------------------------------

backup_dir_for() {
    printf '%s/.komari-switch-backup' "$(dirname "$1")"
}

save_backup() {
    _dir=$(backup_dir_for "$AGENT_PATH")
    mkdir -p "$_dir" || return 1
    cp "$AGENT_PATH" "${_dir}/agent.bak" || return 1
    cp "$SERVICE_FILE" "${_dir}/service.bak" || return 1
    {
        echo "AGENT_PATH=$AGENT_PATH"
        echo "SERVICE_FILE=$SERVICE_FILE"
        echo "INIT_SYSTEM=$INIT_SYSTEM"
        echo "SERVICE_NAME=$SERVICE_NAME"
        echo "SAVED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    } > "${_dir}/meta"
    log_success "已备份原版本到 ${GREEN}${_dir}${NC}"
}

restore_backup() {
    _dir="$1"
    cp "${_dir}/agent.bak" "$AGENT_PATH" || return 1
    chmod +x "$AGENT_PATH"
    cp "${_dir}/service.bak" "$SERVICE_FILE" || return 1
    reload_service_definition
    return 0
}

do_revert() {
    discover_service || { log_error "没有找到已安装的 komari agent 服务"; exit 1; }
    parse_service_file || { log_error "无法解析服务文件 ${SERVICE_FILE}"; exit 1; }
    _dir=$(backup_dir_for "$AGENT_PATH")
    if [ ! -f "${_dir}/agent.bak" ] || [ ! -f "${_dir}/service.bak" ]; then
        log_error "找不到备份: ${_dir}"
        log_info "只有用本脚本切换过, 才会留下可回滚的备份"
        exit 1
    fi
    log_step "从 ${_dir} 回滚..."
    if [ "$DRY_RUN" = true ]; then
        log_warning "dry-run: 到此为止, 没有实际改动"
        exit 0
    fi
    service_ctl stop >/dev/null 2>&1 || true
    restore_backup "$_dir" || { log_error "回滚失败"; exit 1; }
    service_ctl start || { log_error "服务启动失败, 请手动检查"; exit 1; }
    log_success "已回滚到切换前的版本"
    exit 0
}

# ------------------------------- 主流程 ------------------------------------

printf '%b\n' "${WHITE}===========================================${NC}"
printf '%b\n' "${WHITE}      Komari Agent 版本切换脚本            ${NC}"
printf '%b\n' "${WHITE}===========================================${NC}"
echo ""

command -v curl >/dev/null 2>&1 || { log_error "需要 curl, 请先安装"; exit 1; }

log_step "查找已安装的 agent 服务..."
discover_service || {
    log_error "没有找到已安装的 komari agent 服务"
    log_info "本脚本只负责把装好的 agent 换成另一个版本。"
    log_info "还没装过的话请先用 install.sh 安装。"
    exit 1
}

case "$SERVICE_FILE" in
    /Library/LaunchDaemons/*) LAUNCHD_DOMAIN="system" ;;
    *)                        LAUNCHD_DOMAIN="gui/$(id -u)" ;;
esac

parse_service_file || {
    log_error "无法从 ${SERVICE_FILE} 解析出启动命令"
    exit 1
}

# 需要写服务文件和二进制, 用户级 systemd 服务除外
if [ "$EUID" -ne 0 ] && [ "$INIT_SYSTEM" != "systemd-user" ]; then
    log_error "需要 root 权限, 请用 sudo 运行"
    exit 1
fi

[ "$REVERT" = true ] && do_revert

if [ "$INTERACTIVE" = "auto" ]; then
    # /dev/tty 有可能存在却打不开 (容器里没有控制终端), 所以真的试着开一次。
    # 放在子 shell 里: 特殊内建命令重定向失败会直接终止当前 shell。
    if ( : > /dev/tty ) 2>/dev/null; then
        INTERACTIVE=yes
    else
        INTERACTIVE=no
    fi
fi

log_config "服务名:     ${GREEN}${SERVICE_NAME}${NC}"
log_config "init 系统:  ${GREEN}${INIT_SYSTEM}${NC}"
log_config "服务文件:   ${GREEN}${SERVICE_FILE}${NC}"
log_config "二进制:     ${GREEN}${AGENT_PATH}${NC}"
log_config "目标仓库:   ${GREEN}${TARGET_REPO}${NC}"
echo ""
printf '%b\n' "${WHITE}========== 当前启动参数 ==========${NC}"
print_args "$CURRENT_ARGS"
printf '%b\n' "${WHITE}=================================${NC}"

# 先套用命令行/配置里指定的增删, 再进入交互
NEW_ARGS="$CURRENT_ARGS"
for _f in $REMOVE_FLAGS; do
    NEW_ARGS=$(flag_unset "$NEW_ARGS" "$_f")
done
_pending_flag=""
for _f in $ADD_FLAGS; do
    case "$_f" in
        -*)
            [ -n "$_pending_flag" ] && NEW_ARGS=$(flag_set "$NEW_ARGS" "$_pending_flag" "")
            _pending_flag="$_f"
            ;;
        *)
            NEW_ARGS=$(flag_set "$NEW_ARGS" "$_pending_flag" "$_f")
            _pending_flag=""
            ;;
    esac
done
[ -n "$_pending_flag" ] && NEW_ARGS=$(flag_set "$NEW_ARGS" "$_pending_flag" "")

if [ "$INTERACTIVE" = "yes" ]; then
    edit_flags_interactive
elif [ "$NEW_ARGS" != "$CURRENT_ARGS" ]; then
    echo ""
    printf '%b\n' "${WHITE}========== 切换后的启动参数 ==========${NC}"
    print_args "$NEW_ARGS"
    printf '%b\n' "${WHITE}=====================================${NC}"
fi

log_step "确认目标平台..."
detect_platform || exit 1
log_info "平台: ${GREEN}${os_name}/${arch}${NC} -> ${GREEN}${file_name}${NC}"

log_step "解析目标版本..."
resolve_version || exit 1
log_info "将要安装: ${GREEN}${resolved_version}${NC}"

if [ "$DRY_RUN" = true ]; then
    echo ""
    log_warning "dry-run 模式, 下面这些操作都不会真的执行:"
    log_info "  1. 停止服务 ${SERVICE_NAME}"
    log_info "  2. 备份 ${AGENT_PATH} 和 ${SERVICE_FILE}"
    log_info "  3. 下载 ${TARGET_REPO} 的 ${resolved_version}/${file_name} 覆盖二进制"
    if [ "$NEW_ARGS" != "$CURRENT_ARGS" ]; then
        log_info "  4. 把启动参数改成: $(mask_args "$NEW_ARGS")"
    else
        log_info "  4. 启动参数保持不变"
    fi
    log_info "  5. 重新启动服务"
    exit 0
fi

if [ "$INTERACTIVE" = "yes" ]; then
    read_tty "确认开始切换? [Y/n] "
    case "$REPLY_VALUE" in
        n|N|no|NO) log_warning "已取消"; exit 0 ;;
    esac
fi

tmp_binary="${TMPDIR:-/tmp}/komari-agent-switch-$$"
log_step "下载新版本..."
download_agent "$tmp_binary" || {
    log_error "下载失败, 直连和镜像都没成功"
    log_info "可以用 --ghproxy <前缀> 指定加速地址后重试"
    rm -f "$tmp_binary"
    exit 1
}

# 下载的二进制先自己跑一下, 架构不对或文件损坏在这一步就能发现
if ! "$tmp_binary" --help >/dev/null 2>&1; then
    log_error "下载到的二进制无法在本机运行, 已放弃切换 (原服务未受影响)"
    rm -f "$tmp_binary"
    exit 1
fi
log_success "新二进制校验通过"

log_step "停止服务 ${SERVICE_NAME}..."
service_ctl stop >/dev/null 2>&1 || log_warning "停止服务时有告警, 继续"

save_backup || { log_error "备份失败, 已放弃切换"; rm -f "$tmp_binary"; exit 1; }
backup_path=$(backup_dir_for "$AGENT_PATH")

log_step "替换二进制..."
rm -f "$AGENT_PATH"
if ! cp "$tmp_binary" "$AGENT_PATH"; then
    log_error "写入失败, 正在回滚"
    restore_backup "$backup_path"
    service_ctl start || true
    rm -f "$tmp_binary"
    exit 1
fi
chmod +x "$AGENT_PATH"
rm -f "$tmp_binary"

# 保持原来的属主, 非 root 运行的服务换完还得能读能执行
if [ "$EUID" -eq 0 ] && [ -f "${backup_path}/agent.bak" ]; then
    _owner=$(ls -ln "${backup_path}/agent.bak" | awk '{print $3":"$4}')
    [ -n "$_owner" ] && chown "$_owner" "$AGENT_PATH" 2>/dev/null || true
fi

if [ "$NEW_ARGS" != "$CURRENT_ARGS" ]; then
    log_step "更新启动参数..."
    if ! apply_args_to_service; then
        log_error "改写服务文件失败, 正在回滚"
        restore_backup "$backup_path"
        service_ctl start || true
        exit 1
    fi
    reload_service_definition
fi

log_step "启动服务..."
if ! service_ctl start; then
    log_error "服务启动失败, 正在回滚到原版本"
    restore_backup "$backup_path"
    service_ctl start || true
    exit 1
fi

sleep 2
if service_is_running; then
    log_success "服务已经跑起来了"
else
    log_error "服务没能保持运行, 正在回滚到原版本"
    restore_backup "$backup_path"
    service_ctl start || true
    log_info "可以先看日志排查: 服务名 ${SERVICE_NAME}"
    exit 1
fi

if [ "$KEEP_BACKUP" != "true" ]; then
    rm -rf "$backup_path"
fi

echo ""
printf '%b\n' "${WHITE}===========================================${NC}"
log_success "切换完成"
log_config "仓库:     ${GREEN}${TARGET_REPO}${NC}"
log_config "版本:     ${GREEN}${resolved_version}${NC}"
log_config "参数:     ${GREEN}$(mask_args "$NEW_ARGS")${NC}"
if [ "$KEEP_BACKUP" = "true" ]; then
    log_config "备份:     ${GREEN}${backup_path}${NC}"
    log_info "想换回去: sudo sh switch-to-fork.sh --revert"
fi
printf '%b\n' "${WHITE}===========================================${NC}"
