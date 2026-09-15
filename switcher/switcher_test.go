package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitArgsKeepsQuotedTokens(t *testing.T) {
	got := splitArgs(`/opt/komari/agent -e https://a.b -t abc --name "my host"`)
	want := []string{"/opt/komari/agent", "-e", "https://a.b", "-t", "abc", "--name", "my host"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("splitArgs = %q, want %q", got, want)
	}
	if round := splitArgs(joinArgs(got)); strings.Join(round, "|") != strings.Join(want, "|") {
		t.Fatalf("join/split round trip = %q, want %q", round, want)
	}
}

func TestFlagHelpers(t *testing.T) {
	args := []string{"-e", "https://a.b", "-t", "tok", "--interval=5", "--gpu"}

	if !hasFlag(args, "--interval") || flagValue(args, "--interval") != "5" {
		t.Fatalf("--interval=5 not understood: %q", args)
	}
	if flagValue(args, "-e") != "https://a.b" {
		t.Fatalf("-e value = %q", flagValue(args, "-e"))
	}
	// 布尔开关后面跟的是另一个参数, 不能把它当成取值吃掉
	if got := unsetFlag(args, "--gpu"); len(got) != 5 {
		t.Fatalf("unsetFlag(--gpu) = %q", got)
	}
	if got := unsetFlag(args, "-t"); strings.Join(got, " ") != "-e https://a.b --interval=5 --gpu" {
		t.Fatalf("unsetFlag(-t) = %q", got)
	}
	if got := setFlag(args, "--interval", "9"); flagValue(got, "--interval") != "9" || hasFlag(got[:len(got)-2], "--interval") {
		t.Fatalf("setFlag did not replace the old value: %q", got)
	}
}

func TestMaskArgsHidesToken(t *testing.T) {
	got := maskArgs([]string{"-e", "https://a.b", "-t", "cdCLsecretFrNM", "--token=abcdefghijkl"})
	if strings.Contains(got, "cdCLsecretFrNM") || strings.Contains(got, "abcdefghijkl") {
		t.Fatalf("token leaked: %s", got)
	}
	if !strings.Contains(got, "cdCL****FrNM") {
		t.Fatalf("unexpected masking: %s", got)
	}
}

func TestLooksLikeBinaryRejectsHTML(t *testing.T) {
	dir := t.TempDir()
	html := filepath.Join(dir, "page.html")
	os.WriteFile(html, []byte("<html><body>502 Bad Gateway</body></html>"), 0o644)
	if looksLikeBinary(html) {
		t.Fatal("an HTML error page must not pass as an executable")
	}
	elf := filepath.Join(dir, "bin")
	os.WriteFile(elf, append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 64)...), 0o755)
	if !looksLikeBinary(elf) {
		t.Fatal("ELF must be accepted")
	}
}

func TestParseAndWriteSystemdUnit(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "komari-agent.service")
	body := "[Unit]\nDescription=Komari Agent\n\n[Service]\nExecStart=-/opt/komari/agent -e https://a.b -t tok\nRestart=always\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := os.WriteFile(unit, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &service{Init: "systemd", Name: "komari-agent", File: unit}
	if err := parseService(svc); err != nil {
		t.Fatal(err)
	}
	if svc.Binary != "/opt/komari/agent" || flagValue(svc.Args, "-t") != "tok" {
		t.Fatalf("parsed binary=%q args=%q", svc.Binary, svc.Args)
	}

	newArgs := setFlag(svc.Args, "--disable-security-warning", "")
	if err := svc.writeArgs(svc.Binary, newArgs); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(unit)
	got := string(out)
	if !strings.Contains(got, "ExecStart=-/opt/komari/agent -e https://a.b -t tok --disable-security-warning") {
		t.Fatalf("ExecStart not rewritten as expected:\n%s", got)
	}
	// 其它行必须原样留着
	for _, line := range []string{"Description=Komari Agent", "Restart=always", "WantedBy=multi-user.target"} {
		if !strings.Contains(got, line) {
			t.Fatalf("%q disappeared from the unit file:\n%s", line, got)
		}
	}
}

func TestParseAndWriteProcdScript(t *testing.T) {
	dir := t.TempDir()
	init := filepath.Join(dir, "komari-agent")
	body := "#!/bin/sh /etc/rc.common\nUSE_PROCD=1\nSTART=99\nPROG=\"/opt/komari/agent\"\nARGS=\"-e https://a.b -t tok\"\n"
	if err := os.WriteFile(init, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &service{Init: "procd", Name: "komari-agent", File: init}
	if err := parseService(svc); err != nil {
		t.Fatal(err)
	}
	if svc.Binary != "/opt/komari/agent" || len(svc.Args) != 4 {
		t.Fatalf("parsed binary=%q args=%q", svc.Binary, svc.Args)
	}
	if err := svc.writeArgs(svc.Binary, unsetFlag(svc.Args, "-t")); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(init)
	if !strings.Contains(string(out), "ARGS=\"-e https://a.b\"") {
		t.Fatalf("ARGS not rewritten:\n%s", out)
	}
	// 服务脚本得保持可执行, 否则回滚之后 init 起不来
	st, err := os.Stat(init)
	if err != nil || st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("init script lost its executable bit: %v %v", err, st.Mode())
	}
}

func TestParseAndWriteLaunchdPlist(t *testing.T) {
	dir := t.TempDir()
	plist := filepath.Join(dir, "com.komari.komari-agent.plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.komari.komari-agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/opt/komari/agent</string>
        <string>-e</string>
        <string>https://a.b</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &service{Init: "launchd", Name: "komari-agent", File: plist}
	if err := parseService(svc); err != nil {
		t.Fatal(err)
	}
	if svc.Binary != "/opt/komari/agent" || flagValue(svc.Args, "-e") != "https://a.b" {
		t.Fatalf("parsed binary=%q args=%q", svc.Binary, svc.Args)
	}
	if err := svc.writeArgs(svc.Binary, setFlag(svc.Args, "--gpu", "")); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(plist)
	got := string(out)
	if !strings.Contains(got, "<string>--gpu</string>") || strings.Count(got, "<string>/opt/komari/agent</string>") != 1 {
		t.Fatalf("plist rewritten wrong:\n%s", got)
	}
	if !strings.Contains(got, "<key>RunAtLoad</key>") {
		t.Fatalf("unrelated keys disappeared:\n%s", got)
	}
}

func TestCandidatesOrderAndMirrorHit(t *testing.T) {
	f := newFetcher(&config{mirrors: stringList{"https://my.mirror/"}})
	got := f.candidates("https://github.com/a/b")
	if got[0] != "https://github.com/a/b" || got[1] != "https://my.mirror/https://github.com/a/b" {
		t.Fatalf("direct then custom mirror expected, got %q", got[:2])
	}
	if len(got) != 2+len(builtinMirrors) {
		t.Fatalf("candidate count = %d", len(got))
	}

	f.noteHit("https://ghfast.top/https://github.com/a/b", "https://github.com/a/b")
	if f.mirrorHit != "https://ghfast.top" {
		t.Fatalf("mirrorHit = %q", f.mirrorHit)
	}
	got = f.candidates("https://github.com/a/b")
	if got[0] != "https://ghfast.top/https://github.com/a/b" {
		t.Fatalf("a mirror that worked should be tried first, got %q", got[0])
	}
	seen := map[string]bool{}
	for _, u := range got {
		if seen[u] {
			t.Fatalf("duplicate candidate %q", u)
		}
		seen[u] = true
	}

	f2 := newFetcher(&config{noMirror: true})
	if len(f2.candidates("https://github.com/a/b")) != 1 {
		t.Fatal("--no-mirror must only try the direct url")
	}
}

func TestDownloadURL(t *testing.T) {
	if got := downloadURL("o/r", "1.5.10", "asset", false); got != "https://github.com/o/r/releases/download/1.5.10/asset" {
		t.Fatalf("got %q", got)
	}
	if got := downloadURL("o/r", "latest", "asset", true); got != "https://github.com/o/r/releases/latest/download/asset" {
		t.Fatalf("got %q", got)
	}
}
