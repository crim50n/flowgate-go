package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBlockyCandidateFiles(t *testing.T, c *syncCandidate) {
	t.Helper()
	p := getPaths()
	for _, item := range []struct {
		path string
		data []byte
	}{
		{p.Blocky, c.blockyConfig},
		{p.BlockyList, c.blockyList},
	} {
		if err := writeAtomic(item.path, item.data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceChangeDoesNotChangeBlockyCandidate(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	base := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"example.com": {Type: "proxy"},
		},
	}
	rules, err := resolveProxyRules(base)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := buildSyncCandidate(base, rules)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockyCandidateFiles(t, candidate)

	base.Domains["app.example.com"] = Domain{Type: "service", IP: "127.0.0.1", Port: 8080}
	rules, err = resolveProxyRules(base)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = buildSyncCandidate(base, rules)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := blockyFilesChanged(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("service-only change unexpectedly changes Blocky files")
	}
}

func TestProxyChangeChangesBlockyCandidate(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{},
	}
	rules, err := resolveProxyRules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := buildSyncCandidate(cfg, rules)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockyCandidateFiles(t, candidate)
	cfg.Domains["example.com"] = Domain{Type: "proxy"}
	rules, err = resolveProxyRules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = buildSyncCandidate(cfg, rules)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := blockyFilesChanged(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("proxy change must change Blocky files")
	}
}

func TestBlockyCertificateChangeTriggersRestartState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.com"},
		Domains: map[string]Domain{
			"dns.example.com": {Type: "service", IP: "127.0.0.1", Port: 8443},
		},
	}
	certDir := rooted("/var/lib/angie/acme/acme_dns_example_com")
	if err := os.MkdirAll(certDir, 0755); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(certDir, "certificate.pem")
	key := filepath.Join(certDir, "private.key")
	if err := os.WriteFile(cert, []byte("cert-v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key-v1"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, hash, err := blockyCertificateChanged(cfg)
	if err != nil || !changed || hash == "" {
		t.Fatalf("initial cert state: changed=%v hash=%q err=%v", changed, hash, err)
	}
	if err := saveBlockyCertificateHash(hash); err != nil {
		t.Fatal(err)
	}
	changed, hash, err = blockyCertificateChanged(cfg)
	if err != nil || changed {
		t.Fatalf("unchanged cert state: changed=%v hash=%q err=%v", changed, hash, err)
	}
	if err := os.WriteFile(cert, []byte("cert-v2"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, newHash, err := blockyCertificateChanged(cfg)
	if err != nil || !changed || newHash == hash {
		t.Fatalf("renewed cert state: changed=%v hash=%q old=%q err=%v", changed, newHash, hash, err)
	}
}
