package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestRoot(t *testing.T, cfg *Config) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDNSConvertsExistingProxyToService(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"dns.example.com": {Type: "proxy"},
		},
	}
	root := setupTestRoot(t, cfg)
	if err := cmdDNS([]string{"dns.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := Domain{Type: "service", IP: "127.0.0.1", Port: 8443}
	if got.Domains["dns.example.com"] != want {
		t.Fatalf("dns domain = %#v, want %#v", got.Domains["dns.example.com"], want)
	}
	if got.Settings.DNSDomain != "dns.example.com" {
		t.Fatalf("dns_domain = %q", got.Settings.DNSDomain)
	}
	blocky, err := os.ReadFile(filepath.Join(root, "etc/blocky/config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blocky), "dns.example.com") {
		t.Fatal("DNS service domain leaked into Blocky mapping")
	}
	stream, err := os.ReadFile(filepath.Join(root, "etc/angie/stream.d/flowgate.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stream), "server_name dns.example.com;") {
		t.Fatal("DNS service domain missing from Angie service route")
	}
}

func TestRemovePrimaryDNSClearsSetting(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.com"},
		Domains: map[string]Domain{
			"dns.example.com": {Type: "service", IP: "127.0.0.1", Port: 8443},
		},
	}
	setupTestRoot(t, cfg)
	if err := cmdRemove([]string{"dns.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.DNSDomain != "" {
		t.Fatalf("dns_domain not cleared: %q", got.Settings.DNSDomain)
	}
}

func TestAddGeoSiteCompilesRules(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{},
		GeoSites: []string{},
	}
	root := setupTestRoot(t, cfg)
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	data := buildTestDLC("CATEGORY-TEST", []ProxyRule{
		{Type: RuleRootDomain, Value: "example.com"},
		{Type: RuleFull, Value: "full.example.net"},
	})
	if err := os.WriteFile(p.DLC, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := cmdAdd([]string{"category-test"}); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GeoSites) != 1 || got.GeoSites[0] != "category-test" {
		t.Fatalf("geosites = %#v", got.GeoSites)
	}
	blocky, err := os.ReadFile(filepath.Join(root, "etc/blocky/config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blocky), "example.com: 203.0.113.10") || !strings.Contains(string(blocky), "full.example.net: 203.0.113.10") {
		t.Fatal("GeoSite domains missing from Blocky customDNS mapping")
	}
	stream, err := os.ReadFile(filepath.Join(root, "etc/angie/stream.d/flowgate.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stream), ".example.com $ssl_preread_server_name;") {
		t.Fatal("GeoSite rule missing from Angie")
	}
}
