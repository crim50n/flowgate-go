package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestConfigRejectsAmbiguousDomainAndACMENames(t *testing.T) {
	cases := []map[string]Domain{
		{"APP.example.com": {Type: "service", Port: 8080}, "app.example.com": {Type: "proxy"}},
		{"app-example.com": {Type: "service", Port: 8080}, "app.example.com": {Type: "service", Port: 8081}},
	}
	for _, domains := range cases {
		cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: domains}
		if err := validateConfig(cfg); err == nil {
			t.Fatalf("ambiguous names accepted: %#v", domains)
		}
	}
}

func TestIPv4ValidationRejectsIPv6Notation(t *testing.T) {
	for _, ip := range []string{"::ffff:127.0.0.1", "::127.0.0.1", "2001:db8::1", "300.1.2.3"} {
		if validIP(ip) {
			t.Fatalf("invalid IPv4 backend accepted: %s", ip)
		}
		cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{"app.example.com": {Type: "service", Port: 8080, IP: ip}}}
		if err := validateConfig(cfg); err == nil {
			t.Fatalf("invalid backend config accepted: %s", ip)
		}
	}
	for _, ip := range []string{"", "127.0.0.1", "203.0.113.10"} {
		if !validIP(ip) {
			t.Fatalf("valid IPv4/default rejected: %q", ip)
		}
	}
}

func TestLoadExistingConfigCanonicalizesWithoutWriting(t *testing.T) {
	cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "DNS.Example.COM"}, Domains: map[string]Domain{
		"dns.example.com": {Type: "service", Port: 8443}, "APP.Example.NET": {Type: "service", Port: 8080},
	}}
	setupTestRoot(t, cfg)
	before, err := os.ReadFile(getPaths().ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.DNSDomain != "dns.example.com" || got.Domains["app.example.net"].Type != "service" {
		t.Fatalf("noncanonical config: %#v", got)
	}
	after, err := os.ReadFile(getPaths().ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only loader rewrote file")
	}
}

func TestDNSFallbackUsesOnlyUnambiguousBlockyService(t *testing.T) {
	cfg := &Config{Domains: map[string]Domain{
		"dns.proxy.example":  {Type: "proxy"},
		"dns.app.example":    {Type: "service", Port: 8080},
		"dns.remote.example": {Type: "service", Port: 8443, IP: "192.0.2.1"},
	}}
	if got := dnsDomain(cfg); got != "" {
		t.Fatalf("unrelated domain selected: %s", got)
	}
	cfg.Domains["dns.one.example"] = Domain{Type: "service", Port: 8443, IP: "127.0.0.1"}
	if got := dnsDomain(cfg); got != "dns.one.example" {
		t.Fatalf("valid fallback not selected: %s", got)
	}
	cfg.Domains["doh.two.example"] = Domain{Type: "service", Port: 8443}
	for i := 0; i < 100; i++ {
		if got := dnsDomain(cfg); got != "" {
			t.Fatalf("ambiguous fallback selected: %s", got)
		}
	}
	cfg.Settings.DNSDomain = "dns.one.example"
	if got := dnsDomain(cfg); got != "dns.one.example" {
		t.Fatalf("explicit primary ignored: %s", got)
	}
}

func TestConfigRejectsInvalidDNSOwnership(t *testing.T) {
	for _, entry := range []Domain{
		{Type: "proxy", ManagedBy: "dns"},
		{Type: "service", Port: 8080, ManagedBy: "dns"},
		{Type: "service", Port: 8443, ManagedBy: "unknown"},
	} {
		cfg := &Config{Domains: map[string]Domain{"dns.example.com": entry}}
		if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "managed_by") {
			t.Fatalf("invalid ownership accepted: %#v, %v", entry, err)
		}
	}
}
