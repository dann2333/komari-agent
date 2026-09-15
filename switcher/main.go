// komari-switch: 把已经装好的 komari agent 换成另一个仓库的构建。
//
// 和 switch-to-fork.sh 干的是同一件事, 但它是一个静态编译的小程序:
// 不依赖 curl/wget, 也不受 sh/dash/busybox 的差异影响 —— 有些机器上的
// curl 根本没编 https, 脚本在那种环境里寸步难行。
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultRepo = "dann2333/komari-agent"
	backupDir   = ".komari-switch-backup"
)

// 构建时用 -X main.buildVersion=... 注入
var buildVersion = "dev"

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, " ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type config struct {
	repo        string
	version     string
	serviceName string
	ghproxy     string
	mirrors     stringList
	noMirror    bool
	addFlags    stringList
	removeFlags stringList
	localFile   string
	yes         bool
	dryRun      bool
	revert      bool
	keepBackup  bool
}

const (
	red    = "\033[0;31m"
	green  = "\033[0;32m"
	yellow = "\033[0;33m"
	cyan   = "\033[0;36m"
	white  = "\033[1;37m"
	reset  = "\033[0m"
)

var noColor = os.Getenv("NO_COLOR") != ""

func paint(color, s string) string {
	if noColor {
		return s
	}
	return color + s + reset
}

func logInfo(format string, a ...interface{}) { fmt.Printf("  "+format+"\n", a...) }
func logStep(format string, a ...interface{}) {
	fmt.Printf("%s %s\n", paint(cyan, "==>"), fmt.Sprintf(format, a...))
}
func logOK(format string, a ...interface{}) {
	fmt.Printf("%s %s\n", paint(green, "[OK]"), fmt.Sprintf(format, a...))
}
func logWarn(format string, a ...interface{}) {
	fmt.Printf("%s %s\n", paint(yellow, "[WARNING]"), fmt.Sprintf(format, a...))
}
func logError(format string, a ...interface{}) {
	fmt.Printf("%s %s\n", paint(red, "[ERROR]"), fmt.Sprintf(format, a...))
}
func logConf(k, v string) {
	fmt.Printf("%s %s %s\n", paint(cyan, "[CONFIG]"), padRight(k, 12), paint(green, v))
}

// 中文是双宽字符, 按字节数对齐会歪, 这里按显示宽度算
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115F,
			r >= 0x2E80 && r <= 0xA4CF,
			r >= 0xAC00 && r <= 0xD7A3,
			r >= 0xF900 && r <= 0xFAFF,
			r >= 0xFE30 && r <= 0xFE6F,
			r >= 0xFF00 && r <= 0xFF60,
			r >= 0xFFE0 && r <= 0xFFE6:
			w += 2
		default:
			w++
		}
	}
	return w
}

func padRight(s string, width int) string {
	if d := width - displayWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func fatal(format string, a ...interface{}) {
	logError(format, a...)
	os.Exit(1)
}

func usage() {
	fmt.Print(`komari-switch —— 把已装好的 komari agent 换成本仓库的版本

用法: komari-switch [选项]

  --repo <owner/name>     目标仓库 (默认 ` + defaultRepo + `)
  --version <ver>         auto | latest | snapshot | 具体 tag
  --snapshot              等价于 --version snapshot
  --latest                等价于 --version latest
  --service-name <name>   指定服务名, 默认自动探测
  --ghproxy <prefix>      只用这个加速前缀, 例如 https://ghfast.top
  --mirror <prefix>       往内置镜像列表最前面插一个, 可重复
  --no-mirror             只直连, 不用任何镜像
  --local-file <path>     用本地已经下好的二进制, 完全不联网
  --add-flag <flag>       切换时追加的启动参数, 可重复
  --remove-flag <flag>    切换时移除的启动参数, 可重复
  -y, --yes               不交互, 直接按配置执行
  --dry-run               只显示将要做什么, 不改动任何东西
  --revert                回滚到切换前的二进制和服务配置
  --no-keep-backup        切换成功后删掉备份目录
  -h, --help              显示本帮助
`)
}

// 自己解析命令行: flag 包对 "--add-flag --disable-web-ssh" 这种
// "取值本身以 - 开头" 的写法会当成缺参数, 而这恰恰是这里最常见的用法。
func parseArgs(argv []string) *config {
	cfg := &config{repo: defaultRepo, version: "auto", keepBackup: true}
	need := func(i int, name string) string {
		if i+1 >= len(argv) {
			fatal("%s 后面缺少取值", name)
		}
		return argv[i+1]
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		var inline string
		hasInline := false
		if strings.HasPrefix(arg, "--") {
			if eq := strings.Index(arg, "="); eq > 0 {
				inline, hasInline = arg[eq+1:], true
				arg = arg[:eq]
			}
		}
		value := func(name string) string {
			if hasInline {
				return inline
			}
			v := need(i, name)
			i++
			return v
		}
		switch arg {
		case "--repo":
			cfg.repo = value(arg)
		case "--version":
			cfg.version = value(arg)
		case "--snapshot", "-s":
			cfg.version = "snapshot"
		case "--latest", "--stable", "-l":
			cfg.version = "latest"
		case "--service-name":
			cfg.serviceName = value(arg)
		case "--ghproxy":
			cfg.ghproxy = value(arg)
		case "--mirror":
			cfg.mirrors.Set(value(arg))
		case "--no-mirror":
			cfg.noMirror = true
		case "--local-file":
			cfg.localFile = value(arg)
		case "--add-flag":
			cfg.addFlags.Set(value(arg))
		case "--remove-flag":
			cfg.removeFlags.Set(value(arg))
		case "-y", "--yes":
			cfg.yes = true
		case "--dry-run":
			cfg.dryRun = true
		case "--revert":
			cfg.revert = true
		case "--no-keep-backup":
			cfg.keepBackup = false
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "-v", "--program-version":
			fmt.Println("komari-switch", buildVersion)
			os.Exit(0)
		default:
			logError("未知参数: %s", arg)
			usage()
			os.Exit(1)
		}
	}
	return cfg
}

func assetName() string {
	name := fmt.Sprintf("komari-agent-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func main() {
	cfg := parseArgs(os.Args[1:])

	fmt.Println(paint(white, "==========================================="))
	fmt.Println(paint(white, "      Komari Agent 版本切换器"))
	fmt.Println(paint(white, "==========================================="))
	fmt.Println()

	logStep("查找已安装的 agent 服务...")
	svc, err := discoverService(cfg.serviceName)
	if err != nil {
		logError("%v", err)
		logInfo("本程序只负责把装好的 agent 换成另一个版本。")
		logInfo("还没装过的话请先用 install.sh 安装。")
		os.Exit(1)
	}

	if os.Geteuid() != 0 && svc.Init != "systemd-user" {
		fatal("需要 root 权限, 请用 sudo 运行")
	}

	if cfg.revert {
		doRevert(cfg, svc)
		return
	}

	logConf("服务名:", svc.Name)
	logConf("init 系统:", svc.Init)
	logConf("服务文件:", svc.File)
	logConf("二进制:", svc.Binary)
	if cfg.localFile == "" {
		logConf("目标仓库:", cfg.repo)
	}
	fmt.Println()
	printArgs("当前启动参数", svc.Args)

	newArgs := append([]string{}, svc.Args...)
	for _, f := range cfg.removeFlags {
		newArgs = unsetFlag(newArgs, f)
	}
	for _, raw := range cfg.addFlags {
		name, val := raw, ""
		if sp := strings.IndexAny(raw, " ="); sp > 0 {
			name, val = raw[:sp], raw[sp+1:]
		}
		newArgs = setFlag(newArgs, name, val)
	}

	interactive := !cfg.yes && isTerminal()
	if interactive {
		newArgs = editArgs(newArgs, svc.Args, cfg.repo)
	} else if !sameArgs(newArgs, svc.Args) {
		fmt.Println()
		printArgs("切换后的启动参数", newArgs)
	}

	asset := assetName()
	logStep("确认目标平台...")
	logInfo("平台: %s -> %s", paint(green, runtime.GOOS+"/"+runtime.GOARCH), paint(green, asset))

	var (
		f            = newFetcher(cfg)
		resolved     string
		latestChan   bool
		versionLabel string
	)
	if cfg.localFile != "" {
		if !isFile(cfg.localFile) {
			fatal("找不到 --local-file 指定的文件: %s", cfg.localFile)
		}
		versionLabel = "本地文件 " + cfg.localFile
	} else {
		logStep("解析目标版本...")
		resolved, latestChan, err = resolveVersion(f, cfg.repo, cfg.version)
		if err != nil {
			logError("%v", err)
			logInfo("确认一下仓库名是否写对, 以及本机能否访问 GitHub")
			logInfo("网络受限的话可以用 --ghproxy <前缀> 指定加速地址")
			os.Exit(1)
		}
		if latestChan {
			logWarn("查不到版本号 (GitHub API 和网页都没通)")
			logInfo("改用 releases/latest 直下通道, 装到的就是最新正式版")
			versionLabel = "latest (仓库最新正式版)"
		} else {
			versionLabel = resolved
		}
	}
	logInfo("将要安装: %s", paint(green, versionLabel))

	backupPath := filepath.Join(filepath.Dir(svc.Binary), backupDir)

	if cfg.dryRun {
		fmt.Println()
		logWarn("dry-run 模式, 下面这些操作都不会真的执行:")
		logInfo("  1. 停止服务 %s", svc.Name)
		logInfo("  2. 备份 %s 和 %s", svc.Binary, svc.File)
		if cfg.localFile != "" {
			logInfo("  3. 用 %s 覆盖二进制", cfg.localFile)
		} else {
			logInfo("  3. 下载 %s 的 %s %s 覆盖二进制", cfg.repo, versionLabel, asset)
		}
		if sameArgs(newArgs, svc.Args) {
			logInfo("  4. 启动参数保持不变")
		} else {
			logInfo("  4. 把启动参数改成: %s", maskArgs(newArgs))
		}
		logInfo("  5. 重新启动服务")
		return
	}

	if interactive && !confirm("确认开始切换? [Y/n] ") {
		logWarn("已取消")
		return
	}

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("komari-agent-switch-%d", os.Getpid()))
	defer os.Remove(tmp)

	if cfg.localFile != "" {
		logStep("使用本地文件 %s...", cfg.localFile)
		if err := copyFileMode(cfg.localFile, tmp, 0o755); err != nil {
			fatal("读不了 %s: %v", cfg.localFile, err)
		}
		if !looksLikeBinary(tmp) {
			fatal("%s 看着不像个可执行文件", cfg.localFile)
		}
	} else {
		logStep("下载新版本...")
		url := downloadURL(cfg.repo, resolved, asset, latestChan)
		if err := f.download(url, tmp, logInfo); err != nil {
			logError("下载失败, 直连和镜像都没成功: %v", err)
			logInfo("可以用 --ghproxy <前缀> 指定加速地址后重试")
			logInfo("也可以自己把 %s 下好, 再用 --local-file <路径> 指定", asset)
			os.Exit(1)
		}
	}

	// 下载的二进制先自己跑一下, 架构不对或文件损坏在这一步就能发现
	if err := exec.Command(tmp, "--help").Run(); err != nil {
		fatal("拿到的二进制无法在本机运行, 已放弃切换 (原服务未受影响)")
	}
	logOK("新二进制校验通过")

	logStep("停止服务 %s...", svc.Name)
	svc.control("stop")

	if err := saveBackup(svc, backupPath); err != nil {
		fatal("备份失败, 已放弃切换: %v", err)
	}
	logOK("已备份原版本到 %s", paint(green, backupPath))

	rollback := func(reason string) {
		logError("%s, 正在回滚到原版本", reason)
		if err := restoreBackup(svc, backupPath); err != nil {
			logError("回滚也失败了: %v", err)
			logInfo("备份还在 %s, 可以手工恢复", backupPath)
		}
		svc.control("start")
		os.Exit(1)
	}

	logStep("替换二进制...")
	uid, gid, hasOwner := fileOwner(svc.Binary)
	if err := replaceBinary(tmp, svc.Binary); err != nil {
		rollback(fmt.Sprintf("写入失败 (%v)", err))
	}
	// 保持原来的属主, 非 root 运行的服务换完还得能读能执行
	if hasOwner {
		chownFile(svc.Binary, uid, gid)
	}

	if !sameArgs(newArgs, svc.Args) {
		logStep("更新启动参数...")
		if err := svc.writeArgs(svc.Binary, newArgs); err != nil {
			rollback(fmt.Sprintf("改写服务文件失败 (%v)", err))
		}
		svc.reloadDefinition()
	}

	logStep("启动服务...")
	if err := svc.control("start"); err != nil {
		rollback(fmt.Sprintf("服务启动失败 (%v)", err))
	}
	time.Sleep(2 * time.Second)
	if !svc.isRunning() {
		rollback("服务没能保持运行")
	}
	logOK("服务已经跑起来了")

	if !cfg.keepBackup {
		os.RemoveAll(backupPath)
	}

	fmt.Println()
	fmt.Println(paint(white, "==========================================="))
	logOK("切换完成")
	if cfg.localFile == "" {
		logConf("仓库:", cfg.repo)
	}
	logConf("版本:", versionLabel)
	logConf("参数:", maskArgs(newArgs))
	if cfg.keepBackup {
		logConf("备份:", backupPath)
		logInfo("想换回去: sudo komari-switch --revert")
	}
	fmt.Println(paint(white, "==========================================="))
}

func doRevert(cfg *config, svc *service) {
	dir := filepath.Join(filepath.Dir(svc.Binary), backupDir)
	if !isFile(filepath.Join(dir, "agent.bak")) || !isFile(filepath.Join(dir, "service.bak")) {
		logError("找不到备份: %s", dir)
		logInfo("只有用本程序或 switch-to-fork.sh 切换过, 才会留下可回滚的备份")
		os.Exit(1)
	}
	logStep("从 %s 回滚...", dir)
	if cfg.dryRun {
		logWarn("dry-run: 到此为止, 没有实际改动")
		return
	}
	svc.control("stop")
	if err := restoreBackup(svc, dir); err != nil {
		fatal("回滚失败: %v", err)
	}
	if err := svc.control("start"); err != nil {
		fatal("服务启动失败, 请手动检查: %v", err)
	}
	logOK("已回滚到切换前的版本")
}

func saveBackup(svc *service, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := copyFile(svc.Binary, filepath.Join(dir, "agent.bak")); err != nil {
		return err
	}
	if err := copyFile(svc.File, filepath.Join(dir, "service.bak")); err != nil {
		return err
	}
	if uid, gid, ok := fileOwner(svc.Binary); ok {
		chownFile(filepath.Join(dir, "agent.bak"), uid, gid)
	}
	meta := fmt.Sprintf("AGENT_PATH=%s\nSERVICE_FILE=%s\nINIT_SYSTEM=%s\nSERVICE_NAME=%s\nSAVED_AT=%s\n",
		svc.Binary, svc.File, svc.Init, svc.Name, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(filepath.Join(dir, "meta"), []byte(meta), 0o644)
}

func restoreBackup(svc *service, dir string) error {
	if err := replaceBinary(filepath.Join(dir, "agent.bak"), svc.Binary); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(dir, "service.bak"), svc.File); err != nil {
		return err
	}
	svc.reloadDefinition()
	return nil
}

// replaceBinary 先删再写: 正在运行的程序文件直接覆盖会 ETXTBSY,
// 删掉再创建则是换了个 inode, 老进程该怎么跑还怎么跑。
func replaceBinary(src, dest string) error {
	mode := os.FileMode(0o755)
	if st, err := os.Stat(src); err == nil {
		mode = st.Mode().Perm() | 0o111 // 可执行位必须有
	}
	os.Remove(dest)
	return copyFileMode(src, dest, mode)
}

// copyFile 连权限一起拷: 服务脚本 (/etc/init.d/xxx) 是要能执行的,
// 按固定的 0644 写回去会把执行位抹掉, 回滚之后服务就起不来了。
func copyFile(src, dest string) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(src); err == nil {
		mode = st.Mode().Perm()
	}
	return copyFileMode(src, dest, mode)
}

func copyFileMode(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, mode)
}

func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func printArgs(title string, args []string) {
	header := "========== " + title + " =========="
	fmt.Println(paint(white, header))
	if len(args) == 0 {
		fmt.Println("    (无)")
	}
	masked := splitArgs(maskArgs(args))
	for i := 0; i < len(masked); i++ {
		line := masked[i]
		if strings.HasPrefix(line, "-") && i+1 < len(masked) && !strings.HasPrefix(masked[i+1], "-") {
			line += " " + masked[i+1]
			i++
		}
		fmt.Println("    " + line)
	}
	fmt.Println(paint(white, strings.Repeat("=", displayWidth(header))))
}

func isTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

var stdin = bufio.NewReader(os.Stdin)

func prompt(text string) string {
	fmt.Print(text)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func confirm(text string) bool {
	switch strings.ToLower(prompt(text)) {
	case "n", "no":
		return false
	}
	return true
}
