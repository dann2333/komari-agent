# komari-agent

## 配置方式

agent 参数可以通过命令行参数、环境变量或 JSON 配置文件传入。

最小启动示例：

```bash
./komari-agent --endpoint "https://example.com" --token "your-token"
```

使用环境变量：

```bash
export AGENT_ENDPOINT="https://example.com"
export AGENT_TOKEN="your-token"
./komari-agent
```

使用 JSON 配置文件：

```bash
./komari-agent --config ./config.json
```

`config.json` 示例：

```json
{
  "endpoint": "https://example.com",
  "token": "your-token",
  "interval": 3,
  "disable_auto_update": false,
  "disable_web_ssh": false,
  "ignore_unsafe_cert": false
}
```

配置优先级从低到高为：默认值、命令行参数、环境变量、JSON 配置文件。

常用配置项：

表中支持版本表示该参数本身首次在发布 tag 中出现；环境变量和 JSON 配置文件方式从 `1.1.33` 起支持，早于最早 tag 的参数记为 `0.0.9`。

| JSON 字段 | 环境变量 | 命令行参数 | 说明 | 支持版本 |
| --- | --- | --- | --- | --- |
| `endpoint` | `AGENT_ENDPOINT` | `--endpoint`, `-e` | 面板地址 | `0.0.9` |
| `token` | `AGENT_TOKEN` | `--token`, `-t` | agent token | `0.0.9` |
| `interval` | `AGENT_INTERVAL` | `--interval`, `-i` | 数据采集间隔，单位秒 | `0.0.9` |
| `disable_auto_update` | `AGENT_DISABLE_AUTO_UPDATE` | `--disable-auto-update` | 禁用自动更新 | `0.0.9` |
| `disable_web_ssh` | `AGENT_DISABLE_WEB_SSH` | `--disable-web-ssh` | 禁用远程控制 | `0.0.9` |
| `ignore_unsafe_cert` | `AGENT_IGNORE_UNSAFE_CERT` | `--ignore-unsafe-cert`, `-u` | 忽略不安全证书 | `0.0.9` |
| `include_nics` | `AGENT_INCLUDE_NICS` | `--include-nics` | 仅统计指定网卡，逗号分隔 | `0.0.22` |
| `exclude_nics` | `AGENT_EXCLUDE_NICS` | `--exclude-nics` | 排除指定网卡，逗号分隔 | `0.0.22` |
| `include_mountpoints` | `AGENT_INCLUDE_MOUNTPOINTS` | `--include-mountpoint` | 仅统计指定挂载点，分号分隔 | `0.1.0` |
| `month_rotate` | `AGENT_MONTH_ROTATE` | `--month-rotate` | 流量统计每月重置日期，`0` 为禁用 | `0.1.0` |
| `auto_discovery_key` | `AGENT_AUTO_DISCOVERY_KEY` | `--auto-discovery` | 自动发现密钥 | `1.0.40` |
| `custom_dns` | `AGENT_CUSTOM_DNS` | `--custom-dns` | 自定义 DNS 服务器 | `1.0.80` |
| `enable_gpu` | `AGENT_ENABLE_GPU` | `--gpu` | 启用详细 GPU 监控 | `1.0.80` |
| `disable_compression` | `AGENT_DISABLE_COMPRESSION` | `--disable-compression` | 禁用 v2 传输压缩 | `1.2.10` |
| `prefer_ip_version` | `AGENT_PREFER_IP_VERSION` | `--prefer-ip-version` | 优先使用 IP 版本，可选 `4` 或 `6` | 未发布 |
| `reconnect_interval` | `AGENT_RECONNECT_INTERVAL` | `--reconnect-interval`, `-c` | 重连退避上限，单位秒。断线会立刻重连一次，失败后从 1 秒起逐次翻倍，最多等这么久 | `0.0.9` |
| `disable_security_warning` | `AGENT_DISABLE_SECURITY_WARNING` | `--disable-security-warning` | 禁用所有平台的安全警告提示（Linux MOTD、Windows 通知），并清理已写入的提示 | 未发布 |
| `update_repo` | `AGENT_UPDATE_REPO` | `--update-repo` | 自动更新使用的发布仓库，形如 `owner/name`，默认 `dann2333/komari-agent` | 未发布 |
| `update_api_url` | `AGENT_UPDATE_API_URL` | `--update-api-url` | 自动更新使用的 GitHub 兼容 API 基地址，默认 `https://api.github.com`；GitHub Enterprise 需填写到 `/api/v3` | 未发布 |

## 从官方版切换过来

已经装了官方版 agent 的话，用 `switch-to-fork.sh` 可以直接换成本仓库的版本，
原有的 endpoint、token 和其它参数都会保留：

```bash
# 交互式：会列出当前参数，可以逐项改完再切
curl -fsSL https://raw.githubusercontent.com/dann2333/komari-agent/main/switch-to-fork.sh | sudo sh

# 不交互，直接按默认配置切换
curl -fsSL https://raw.githubusercontent.com/dann2333/komari-agent/main/switch-to-fork.sh | sudo sh -s -- --yes

# 先看看它打算做什么，不改动任何东西
sudo sh switch-to-fork.sh --dry-run

# 换回切换前的版本
sudo sh switch-to-fork.sh --revert
```

脚本会自动找到已安装的服务（systemd / systemd user / OpenRC / procd / upstart / launchd），
读出当前启动参数，下载对应平台的二进制，替换后重启服务。
切换前会把原二进制和服务文件备份到 `<安装目录>/.komari-switch-backup`，
新版本起不来会自动回滚。

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--repo <owner/name>` | 目标仓库，默认 `dann2333/komari-agent` |
| `--version <ver>` | `auto`（默认，优先正式版）/ `latest` / `snapshot` / 具体 tag |
| `--service-name <name>` | 指定服务名，默认自动探测 |
| `--add-flag <flag>` | 切换时追加参数，可重复，如 `--add-flag --disable-security-warning` |
| `--remove-flag <flag>` | 切换时移除参数，可重复 |
| `--ghproxy <prefix>` | GitHub 加速前缀 |
| `-y, --yes` | 不交互 |
| `--dry-run` | 只预览不改动 |
| `--revert` | 回滚到切换前的版本 |

脚本开头的可配置项也可以用环境变量覆盖，例如
`KOMARI_ADD_FLAGS="--disable-security-warning" sudo -E sh switch-to-fork.sh --yes`。

完整参数可运行：

```bash
./komari-agent --help
```

详见 `cmd/flags/flags.go` 及 `cmd/root.go`
