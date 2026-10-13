package cmd

import (
	"strings"
	"testing"
)

func TestObfuscateRoundTrip(t *testing.T) {
	in := "https://example.com/secret?token=hunter2"
	got := deobfuscate(obfuscate(in))
	if got != in {
		t.Fatalf("round trip = %q", got)
	}
	flags.Endpoint = obfuscate("https://example.com")
	flags.Token = "plain-token"
	deobfuscateConfig()
	if flags.Endpoint != "https://example.com" || flags.Token != "plain-token" {
		t.Fatalf("endpoint=%q token=%q", flags.Endpoint, flags.Token)
	}
}

func TestPackArgvRoundTrip(t *testing.T) {
	in := []string{"-e", "https://example.com", "-t", "hunter2", "--interval", "5"}
	got, err := unpackArgv(packArgv(in))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != strings.Join(in, " ") {
		t.Fatalf("unpacked = %q", got)
	}
	unit := "ExecStart=-/opt/komari/agent -e https://example.com -t hunter2\n"
	next, ok := rewriteServiceText(unit, "/opt/komari/agent")
	if !ok || strings.Contains(next, "https://example.com") || !strings.Contains(next, "ExecStart=-/opt/komari/agent --x x.") {
		t.Fatalf("migrated unit = %q ok=%v", next, ok)
	}
	quoted := "ExecStart=\"/opt/komari/agent\" -e https://example.com -t hunter2\n"
	qnext, ok := rewriteServiceText(quoted, "/opt/komari/agent")
	if !ok || !strings.Contains(qnext, "ExecStart=\"/opt/komari/agent\" --x x.") || strings.Contains(qnext, "https://example.com") {
		t.Fatalf("quoted unit = %q", qnext)
	}
	longer := "ExecStart=/opt/komari/agent-helper -e https://example.com\n"
	if _, ok := rewriteServiceText(longer, "/opt/komari/agent"); ok {
		t.Fatal("prefix path was rewritten")
	}
	again, ok := rewriteServiceText(next, "/opt/komari/agent")
	if ok {
		t.Fatalf("packed unit was rewritten again: %q", again)
	}
}
