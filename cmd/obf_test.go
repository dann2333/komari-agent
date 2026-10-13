package cmd

import "testing"

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
