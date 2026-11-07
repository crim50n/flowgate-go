package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ambiguousDNSConfig() *Config {
	return &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"dns.one.example": {Type: "service", IP: "127.0.0.1", Port: 8443},
			"doh.two.example": {Type: "service", Port: 8443},
		},
	}
}

func writeDNSAmbiguityCertificate(t *testing.T, domain string) {
	t.Helper()
	dir := rooted(filepath.Join("/var/lib/angie/acme", "acme_"+acmeName(domain)))
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"certificate.pem", "private.key"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAmbiguousLegacyDNSRejectsSyncCandidate(t *testing.T) {
	cfg := ambiguousDNSConfig()
	setupTestRoot(t, cfg)
	for domain := range cfg.Domains {
		writeDNSAmbiguityCertificate(t, domain)
	}
	loaded, err := loadConfig()
	if err != nil {
		t.Fatalf("ambiguous legacy configuration must remain loadable for repair: %v", err)
	}
	candidate, err := buildSyncCandidate(loaded, nil)
	if err == nil || candidate != nil {
		t.Fatal("ambiguous legacy DNS must not produce a candidate that silently disables TLS")
	}
	for _, want := range []string{"dns.one.example", "doh.two.example", "flowgate dns set"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ambiguity error %q does not identify %q", err, want)
		}
	}
}

func TestExplicitDNSResolvesLegacyAmbiguity(t *testing.T) {
	for _, domain := range []string{"dns.one.example", "doh.two.example"} {
		t.Run(domain, func(t *testing.T) {
			t.Setenv("FLOWGATE_ROOT", t.TempDir())
			cfg := ambiguousDNSConfig()
			cfg.Settings.DNSDomain = domain
			writeDNSAmbiguityCertificate(t, domain)
			candidate, err := buildSyncCandidate(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"https: 8443", "tls: 853", "acme_" + acmeName(domain) + "/certificate.pem"} {
				if !strings.Contains(string(candidate.blockyConfig), want) {
					t.Fatalf("explicit DNS candidate lacks %q:\n%s", want, candidate.blockyConfig)
				}
			}
		})
	}
}

func TestDNSCommandRepairsAmbiguityWithoutRemovingLegacyAliases(t *testing.T) {
	cfg := ambiguousDNSConfig()
	setupTestRoot(t, cfg)
	writeDNSAmbiguityCertificate(t, "dns.one.example")
	if err := cmdDNS([]string{"set", "dns.one.example"}); err != nil {
		t.Fatalf("cannot select an explicit primary from ambiguous legacy aliases: %v", err)
	}
	got, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings.DNSDomain != "dns.one.example" {
		t.Fatalf("primary DNS = %q", got.Settings.DNSDomain)
	}
	if len(got.Domains) != len(cfg.Domains) {
		t.Fatalf("legacy aliases changed: %#v", got.Domains)
	}
	for domain, entry := range cfg.Domains {
		if got.Domains[domain] != entry {
			t.Fatalf("legacy alias %s was removed or changed: %#v", domain, got.Domains[domain])
		}
	}
	blocky, err := os.ReadFile(getPaths().Blocky)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blocky), "https: 8443") || !strings.Contains(string(blocky), "tls: 853") {
		t.Fatalf("repair failed to retain DNS TLS listeners:\n%s", blocky)
	}
}
