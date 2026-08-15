package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testConfig() *Config {
	return &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"external.example.com": {Type: "proxy"},
			"app.example.com":      {Type: "service", IP: "127.0.0.1", Port: 8080},
		},
	}
}

func testRules(t *testing.T) []ProxyRule {
	t.Helper()
	rules, err := resolveProxyRules(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestBlockyContainsOnlyProxyDomains(t *testing.T) {
	list, err := renderBlockyProxyList(testRules(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(list)
	if !strings.Contains(text, "*.external.example.com\n") {
		t.Fatal("proxy domain missing from Blocky list")
	}
	if strings.Contains(text, "app.example.com") {
		t.Fatal("service domain must not be written to Blocky list")
	}
	cfg, err := renderBlocky(testConfig(), testRules(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "blockType: 203.0.113.10") {
		t.Fatal("Blocky custom blockType missing")
	}
}

func TestAngieSeparatesPassthroughAndReverseProxy(t *testing.T) {
	stream := renderAngieStream(testConfig(), testRules(t))
	if !strings.Contains(stream, ".external.example.com $ssl_preread_server_name;") {
		t.Fatal("proxy domain missing from SNI passthrough map")
	}
	if !strings.Contains(stream, "server_name app.example.com;") {
		t.Fatal("service domain missing from reverse-proxy SNI route")
	}
	if strings.Contains(stream, ".app.example.com $ssl_preread_server_name;") {
		t.Fatal("service domain leaked into passthrough map")
	}

	http := renderAngieHTTP(testConfig())
	if !strings.Contains(http, "server_name app.example.com;") {
		t.Fatal("service domain missing from HTTP reverse proxy")
	}
	if !strings.Contains(http, "proxy_pass http://127.0.0.1:8080;") {
		t.Fatal("service backend missing")
	}
	if strings.Contains(http, "server_name external.example.com;") {
		t.Fatal("passthrough proxy domain leaked into HTTP reverse proxy")
	}
}

func TestGeoRuleRendering(t *testing.T) {
	rules := []ProxyRule{
		{Type: RuleRootDomain, Value: "root.example"},
		{Type: RuleFull, Value: "full.example"},
		{Type: RulePlain, Value: "keyword.example"},
		{Type: RuleRegex, Value: `^node-[0-9]+\.example\.com$`},
	}
	list, err := renderBlockyProxyList(rules)
	if err != nil {
		t.Fatal(err)
	}
	text := string(list)
	for _, want := range []string{
		"*.root.example",
		"full.example",
		`/keyword\.example/`,
		`/^node-[0-9]+\.example\.com$/`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Blocky list missing %q", want)
		}
	}
	stream := renderAngieStream(&Config{}, rules)
	for _, want := range []string{
		".root.example $ssl_preread_server_name;",
		"full.example $ssl_preread_server_name;",
		angieRegexKey("~*", regexp.QuoteMeta("keyword.example")) + " $ssl_preread_server_name;",
		angieRegexKey("~", `^node-[0-9]+\.example\.com$`) + " $ssl_preread_server_name;",
	} {
		if !strings.Contains(stream, want) {
			t.Fatalf("Angie stream missing %q", want)
		}
	}
}

func TestAngieSkipsExactRuleCoveredByRootDomain(t *testing.T) {
	rules := []ProxyRule{
		{Type: RuleRootDomain, Value: "example.com"},
		{Type: RuleFull, Value: "example.com"},
	}
	stream := renderAngieStream(&Config{}, rules)
	if strings.Count(stream, "example.com $ssl_preread_server_name;") != 1 {
		t.Fatalf("root/full duplicate was not collapsed: %s", stream)
	}
}

func TestAngieRegexKeysAreQuoted(t *testing.T) {
	rules := []ProxyRule{
		{Type: RuleRegex, Value: `^speed\.(coe|open)\.ad\.[a-z]{2,6}\.prod\.hosts\.ooklaserver\.net$`},
		{Type: RuleRegex, Value: `^node-\d+-\S+\.example\.com$`},
	}
	stream := renderAngieStream(&Config{}, rules)
	for _, rule := range rules {
		want := angieRegexKey("~", rule.Value) + " $ssl_preread_server_name;"
		if !strings.Contains(stream, want) {
			t.Fatalf("Angie stream missing quoted regex %q", want)
		}
	}
}

func TestDoHBackendUsesHTTPS(t *testing.T) {
	cfg := &Config{Domains: map[string]Domain{
		"dns.example.com": {Type: "service", IP: "127.0.0.1", Port: 8443},
	}}
	http := renderAngieHTTP(cfg)
	if !strings.Contains(http, "proxy_pass https://127.0.0.1:8443;") {
		t.Fatal("port 8443 must use HTTPS for Blocky DoH")
	}
}

func TestBlockyTLSEnabledAfterDNSCertificateExists(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.com"},
		Domains: map[string]Domain{
			"dns.example.com": {Type: "service", IP: "127.0.0.1", Port: 8443},
		},
	}
	before, err := renderBlocky(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "tls: 853") || strings.Contains(string(before), "https: 8443") {
		t.Fatal("Blocky TLS listeners enabled before ACME certificate exists")
	}

	certDir := rooted("/var/lib/angie/acme/acme_dns_example_com")
	if err := os.MkdirAll(certDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "certificate.pem"), []byte("cert\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "private.key"), []byte("key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := renderBlocky(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !strings.Contains(text, "tls: 853") || !strings.Contains(text, "https: 8443") {
		t.Fatalf("Blocky TLS listeners missing after certificate creation:\n%s", text)
	}
	if !strings.Contains(text, "certificate.pem") || !strings.Contains(text, "private.key") {
		t.Fatal("Blocky certificate paths missing after certificate creation")
	}
}
