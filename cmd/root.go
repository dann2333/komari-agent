package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/monitoring/netstatic"
	monitoring "github.com/komari-monitor/komari-agent/monitoring/unit"
	"github.com/komari-monitor/komari-agent/server"
	"github.com/komari-monitor/komari-agent/update"
	"github.com/spf13/cobra"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
)

var flags = pkg_flags.GlobalConfig

var warningPanelHost, warningRunAsUser string

var RootCmd = &cobra.Command{
	Use:   "komari-agent",
	Short: "komari agent",
	Long:  `komari agent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Notification helpers must not load the service's config or credentials.
		if flags.ShowWarning {
			if !flags.DisableSecurityWarning {
				ShowToast()
			}
			return nil
		}
		loadFromEnv() // 从环境变量加载配置，覆盖解析
		if flags.ConfigFile != "" {
			bytes, err := os.ReadFile(flags.ConfigFile)
			if err != nil {
				return fmt.Errorf("failed to read config file: %w", err)
			}
			err = json.Unmarshal(bytes, flags)
			if err != nil {
				return fmt.Errorf("failed to parse config file: %w", err)
			}
		}
		if flags.PreferIPVersion != "" && flags.PreferIPVersion != "4" && flags.PreferIPVersion != "6" {
			return fmt.Errorf("invalid --prefer-ip-version value %q: expected 4 or 6", flags.PreferIPVersion)
		}
		// 更新源，允许指向自建镜像或 fork，避免被拉回默认仓库
		if err := update.SetReleaseSource(flags.UpdateRepo, flags.UpdateAPIURL); err != nil {
			return err
		}
		// 捕获中止信号，优雅退出。
		// 这里不用 signal.NotifyContext：那样 RunE 正常返回时 defer 的 stop()
		// 也会让 ctx 结束，下面的收尾协程会抢先 os.Exit(0)，
		// 把本该 exit 1 的配置错误变成 exit 0，错误信息还可能来不及打完。
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		stopCtx, stop := context.WithCancel(context.Background())
		defer stop()

		stopWarning := func() {}
		switch {
		case flags.DisableSecurityWarning:
			// 清理此前版本或此前运行留下的警告，避免禁用后仍然显示
			removeSecurityWarning()
		case !flags.DisableWebSsh:
			stopWarning = startSecurityWarning(stopCtx)
		}
		defer stopWarning()
		go func() {
			select {
			case <-sigCh:
			case <-stopCtx.Done():
				// RunE 已经返回，收尾交给 defer，这里直接退出协程
				return
			}
			log.Printf("shutting down gracefully...")
			stop()
			stopWarning()
			netstatic.Stop()
			os.Exit(0)
		}()

		if flags.MonthRotate != 0 {
			err := netstatic.StartOrContinue()
			if err != nil {
				log.Println("Failed to start netstatic monitoring:", err)
			}
			nics, err := monitoring.InterfaceList()
			if err != nil {
				log.Println("Failed to get interface list for netstatic:", err)
			}
			err = netstatic.SetNewConfig(netstatic.NetStaticConfig{
				Nics: nics,
			})
			if err != nil {
				log.Println("Failed to set netstatic config:", err)
			}
		}

		log.Println("Komari Agent", update.CurrentVersion)
		log.Println("Github Repo:", update.Repo)

		// 设置 DNS 解析行为
		if flags.CustomDNS != "" {
			dnsresolver.SetCustomDNSServer(flags.CustomDNS)
			log.Printf("Using custom DNS server: %s", flags.CustomDNS)
		} else {
			// 未设置则使用系统默认 DNS（不使用内置列表）
			log.Printf("Using system default DNS resolver")
		}

		// Auto discovery
		if flags.AutoDiscoveryKey != "" {
			err := handleAutoDiscovery()
			if err != nil {
				return fmt.Errorf("auto-discovery failed: %w", err)
			}
		}
		diskList, err := monitoring.DiskList()
		if err != nil {
			log.Println("Failed to get disk list:", err)
		}
		log.Println("Monitoring Mountpoints:", diskList)
		interfaceList, err := monitoring.InterfaceList()
		if err != nil {
			log.Println("Failed to get interface list:", err)
		}
		log.Println("Monitoring Interfaces:", interfaceList)

		// 忽略不安全的证书
		if flags.IgnoreUnsafeCert {
			http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		// 自动更新。检查和下载都放后台：这里原本是同步调用，排在首次
		// WebSocket 连接之前，下载一个几十 MB 的新版本期间面板上就是离线。
		if !flags.DisableAutoUpdate {
			go func() {
				if err := update.CheckAndUpdate(); err != nil {
					log.Println("[ERROR]", err)
				}
				update.DoUpdateWorks()
			}()
		}
		go server.DoUploadBasicInfoWorks()
		for {
			server.UpdateBasicInfo()
			server.EstablishWebSocketConnection()
		}
	},
}

func Execute() {
	for i, arg := range os.Args {
		if arg == "-autoUpdate" || arg == "--autoUpdate" {
			log.Println("WARNING: The -autoUpdate flag is deprecated in version 0.0.9 and later. Use --disable-auto-update to configure auto-update behavior.")
			// 从参数列表中移除该参数，防止cobra解析错误
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			break
		}
		if arg == "-memory-mode-available" || arg == "--memory-mode-available" {
			//flags.MemoryIncludeCache = true
			log.Println("WARNING: The --memory-mode-available flag is deprecated in version 1.0.70 and later. Use --memory-include-cache to report memory usage including cache/buffer.")
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
		}
	}

	if err := RootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flags.Token, "token", "t", "", "API token")
	//RootCmd.MarkPersistentFlagRequired("token")
	RootCmd.PersistentFlags().StringVarP(&flags.Endpoint, "endpoint", "e", "", "API endpoint")
	//RootCmd.MarkPersistentFlagRequired("endpoint")
	RootCmd.PersistentFlags().StringVar(&flags.AutoDiscoveryKey, "auto-discovery", "", "Auto discovery key for the agent")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableAutoUpdate, "disable-auto-update", false, "Disable automatic updates")
	RootCmd.PersistentFlags().StringVar(&flags.UpdateRepo, "update-repo", "", "Release repository used for self-update, formatted as owner/name")
	RootCmd.PersistentFlags().StringVar(&flags.UpdateAPIURL, "update-api-url", "", "Base URL of the GitHub-compatible API used for self-update (e.g. https://ghe.example.com/api/v3)")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableWebSsh, "disable-web-ssh", false, "Disable remote control(web ssh and rce)")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableSecurityWarning, "disable-security-warning", false, "Disable the security warning notice on every platform (Linux MOTD, Windows toast)")
	//RootCmd.PersistentFlags().BoolVar(&flags.MemoryModeAvailable, "memory-mode-available", false, "[deprecated]Report memory as available instead of used.")
	RootCmd.PersistentFlags().Float64VarP(&flags.Interval, "interval", "i", 3.0, "Interval in seconds")
	RootCmd.PersistentFlags().BoolVarP(&flags.IgnoreUnsafeCert, "ignore-unsafe-cert", "u", false, "Ignore unsafe certificate errors")
	RootCmd.PersistentFlags().IntVarP(&flags.MaxRetries, "max-retries", "r", 3, "Maximum number of retries")
	RootCmd.PersistentFlags().IntVarP(&flags.ReconnectInterval, "reconnect-interval", "c", 5, "Upper bound in seconds for reconnect backoff; a dropped connection reconnects immediately and retries start at 1s")
	RootCmd.PersistentFlags().IntVar(&flags.InfoReportInterval, "info-report-interval", 5, "Interval in minutes for reporting basic info")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeNics, "include-nics", "", "Comma-separated list of network interfaces to include")
	RootCmd.PersistentFlags().StringVar(&flags.ExcludeNics, "exclude-nics", "", "Comma-separated list of network interfaces to exclude")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeMountpoints, "include-mountpoint", "", "Semicolon-separated list of mount points to include for disk statistics")
	RootCmd.PersistentFlags().IntVar(&flags.MonthRotate, "month-rotate", 0, "Month reset for network statistics (0 to disable)")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryIncludeCache, "memory-include-cache", false, "Include cache/buffer in memory usage")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryReportRawUsed, "memory-exclude-bcf", false, "Use \"raminfo.Used = v.Total - v.Free - v.Buffers - v.Cached\" calculation for memory usage")
	RootCmd.PersistentFlags().StringVar(&flags.CustomDNS, "custom-dns", "", "Custom DNS server to use (e.g. 8.8.8.8, 114.114.114.114). By default, the program uses the system DNS resolver.")
	RootCmd.PersistentFlags().BoolVar(&flags.EnableGPU, "gpu", false, "Enable detailed GPU monitoring (usage, memory, multi-GPU support)")
	RootCmd.PersistentFlags().BoolVar(&flags.ShowWarning, "show-warning", false, "Show security warning on Windows, run once as a subprocess")
	RootCmd.PersistentFlags().StringVar(&warningPanelHost, "warning-panel-host", "", "Panel host shown by the notification helper")
	RootCmd.PersistentFlags().StringVar(&warningRunAsUser, "warning-run-as-user", "", "Agent account shown by the notification helper")
	_ = RootCmd.PersistentFlags().MarkHidden("warning-panel-host")
	_ = RootCmd.PersistentFlags().MarkHidden("warning-run-as-user")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv4, "custom-ipv4", "", "Custom IPv4 address to use")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv6, "custom-ipv6", "", "Custom IPv6 address to use")
	RootCmd.PersistentFlags().BoolVar(&flags.GetIpAddrFromNic, "get-ip-addr-from-nic", false, "Get IP address from network interface")
	RootCmd.PersistentFlags().StringVar(&flags.ConfigFile, "config", "", "Path to the configuration file")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableCompression, "disable-compression", false, "Disable v2 gzip/permessage-deflate compression")
	RootCmd.PersistentFlags().StringVar(&flags.PreferIPVersion, "prefer-ip-version", "", "Prefer IP version for dashboard connections: 4 or 6")
	RootCmd.PersistentFlags().ParseErrorsWhitelist.UnknownFlags = true
}

func loadFromEnv() {
	val := reflect.ValueOf(flags).Elem()
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Get the env tag
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue
		}

		// Get the environment variable value
		envValue := os.Getenv(envTag)
		if envValue == "" {
			continue
		}

		// Set the field based on its type
		switch field.Kind() {
		case reflect.String:
			field.SetString(envValue)
		case reflect.Bool:
			if strings.ToLower(envValue) == "true" || envValue == "1" {
				field.SetBool(true)
			}
		case reflect.Int:
			if intVal, err := strconv.Atoi(envValue); err == nil {
				field.SetInt(int64(intVal))
			}
		case reflect.Float64:
			if floatVal, err := strconv.ParseFloat(envValue, 64); err == nil {
				field.SetFloat(floatVal)
			}
		}
	}
}
