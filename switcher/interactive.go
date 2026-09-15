package main

import (
	"fmt"
	"os"
	"strings"
)

// editArgs 是跑在终端里时的那个小菜单: 先把参数摆出来, 改完回车继续。
func editArgs(args, original []string, repo string) []string {
	boolState := func(name string) string {
		if hasFlag(args, name) {
			return paint(green, "已开启")
		}
		return "未开启"
	}
	valueState := func(name string) string {
		if v := flagValue(args, name); v != "" {
			return paint(green, v)
		}
		if hasFlag(args, name) {
			return paint(green, "已设置")
		}
		return "未设置"
	}
	toggle := func(name string) []string {
		if hasFlag(args, name) {
			fmt.Println("  已去掉 " + name)
			return unsetFlag(args, name)
		}
		fmt.Println("  已加上 " + name)
		return setFlag(args, name, "")
	}
	setValue := func(name, desc string) []string {
		v := prompt(fmt.Sprintf("%s (%s, 留空表示删掉这个参数): ", name, desc))
		if v == "" {
			return unsetFlag(args, name)
		}
		return setFlag(args, name, v)
	}

	for {
		fmt.Println()
		printArgs("切换后的启动参数", args)
		fmt.Println()
		fmt.Printf("  1) 开关 --disable-security-warning   当前: %s   (禁用所有平台的安全警告提示)\n", boolState("--disable-security-warning"))
		fmt.Printf("  2) 开关 --disable-auto-update        当前: %s   (禁用自动更新)\n", boolState("--disable-auto-update"))
		fmt.Printf("  3) 开关 --disable-web-ssh            当前: %s   (禁用远程控制)\n", boolState("--disable-web-ssh"))
		fmt.Printf("  4) 设置 --update-repo                当前: %s   (自动更新的仓库, 留空则用二进制内置的 %s)\n", valueState("--update-repo"), repo)
		fmt.Printf("  5) 设置 --update-api-url             当前: %s   (自建 GitHub 兼容 API)\n", valueState("--update-api-url"))
		fmt.Printf("  6) 设置 --endpoint                   当前: %s\n", valueState("--endpoint"))
		fmt.Printf("  7) 设置 --token\n")
		fmt.Printf("  8) 设置 --interval                   当前: %s   (采集间隔, 秒)\n", valueState("--interval"))
		fmt.Println("  a) 手动添加/修改任意参数")
		fmt.Println("  d) 删除某个参数")
		fmt.Println("  e) 直接编辑整行参数")
		fmt.Println("  r) 还原成切换前的参数")
		fmt.Println()

		switch strings.ToLower(prompt("选择操作 (直接回车表示确认并继续, q 退出): ")) {
		case "":
			return args
		case "1":
			args = toggle("--disable-security-warning")
		case "2":
			args = toggle("--disable-auto-update")
		case "3":
			args = toggle("--disable-web-ssh")
		case "4":
			args = setValue("--update-repo", "形如 owner/name")
		case "5":
			args = setValue("--update-api-url", "GitHub Enterprise 需填到 /api/v3")
		case "6":
			args = setValue("--endpoint", "面板地址")
		case "7":
			args = setValue("--token", "面板 token")
		case "8":
			args = setValue("--interval", "单位秒")
		case "a":
			name := prompt("要添加/修改的参数名 (含 --): ")
			if !strings.HasPrefix(name, "--") {
				logWarn("参数名需要以 -- 开头")
				continue
			}
			args = setFlag(args, name, prompt("参数值 (布尔开关留空): "))
		case "d":
			if name := prompt("要删除的参数名 (含 --): "); name != "" {
				args = unsetFlag(args, name)
			}
		case "e":
			args = splitArgs(prompt("输入完整的参数行: "))
		case "r":
			args = append([]string{}, original...)
			logInfo("已还原成切换前的参数")
		case "q":
			logWarn("已取消, 没有做任何改动")
			os.Exit(0)
		default:
			logWarn("无法识别的选择")
		}
	}
}
