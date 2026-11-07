package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDNSSwitchRemovesOwnedRouteAndPreservesUserServices(t *testing.T) {
	original := Domain{Type: "service", IP: "127.0.0.1", Port: 8443}
	app := Domain{Type: "service", IP: "127.0.0.1", Port: 8080}
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.net"},
		Domains:  map[string]Domain{"dns.example.net": original, "app.example.net": app},
	}
	setupTestRoot(t, cfg)
	if err := cmdDNS([]string{"set", "temporary.example.org"}); err != nil {
		t.Fatal(err)
	}
	got, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.Domains["temporary.example.org"].ManagedBy != "dns" {
		t.Fatal("new route has no ownership marker")
	}
	if got.Domains["dns.example.net"] != original {
		t.Fatal("legacy user service was altered")
	}
	if err := cmdDNS([]string{"set", "dns.example.net"}); err != nil {
		t.Fatal(err)
	}
	got, err = loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got.Domains["temporary.example.org"]; exists {
		t.Fatal("temporary DNS route survived switching back")
	}
	if got.Domains["dns.example.net"] != original || got.Domains["app.example.net"] != app {
		t.Fatal("user service changed")
	}
	if got.Settings.DNSDomain != "dns.example.net" {
		t.Fatal("primary DNS was not restored")
	}
	for _, path := range []string{getPaths().AngieStream, getPaths().AngieHTTP, getPaths().Blocky} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("temporary.example.org")) {
			t.Fatalf("stale DNS route in %s", path)
		}
	}
	blocky, err := os.ReadFile(getPaths().Blocky)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blocky, []byte("example.net")) {
		t.Fatal("user services leaked into Blocky")
	}
}

func TestDNSSwitchReplacesOwnedPrimaryAndIsIdempotent(t *testing.T) {
	setupTestRoot(t, &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{}})
	if err := cmdDNS([]string{"set", "dns.one.example"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDNS([]string{"set", "dns.two.example"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Domains) != 1 || cfg.Domains["dns.two.example"].ManagedBy != "dns" {
		t.Fatalf("unexpected routes: %#v", cfg.Domains)
	}
	paths := []string{getPaths().ConfigFile, getPaths().Blocky, getPaths().AngieStream, getPaths().AngieHTTP}
	before := make(map[string][]byte)
	for _, path := range paths {
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := cmdDNS([]string{"set", "dns.two.example"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before[path], after) {
			t.Fatalf("repeat changed %s", path)
		}
	}
}

func TestDNSRefusesToOverwriteExistingRoute(t *testing.T) {
	for _, entry := range []Domain{{Type: "proxy"}, {Type: "service", IP: "127.0.0.1", Port: 8080}} {
		t.Run(entry.Type, func(t *testing.T) {
			cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{"app.example.com": entry}}
			setupTestRoot(t, cfg)
			before, err := os.ReadFile(getPaths().ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmdDNS([]string{"set", "app.example.com"}); err == nil || !strings.Contains(err.Error(), "--replace") {
				t.Fatalf("missing actionable conflict: %v", err)
			}
			after, err := os.ReadFile(getPaths().ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected command changed config")
			}
			if fileExists(getPaths().AppliedConfig) || fileExists(getPaths().AngieHTTP) {
				t.Fatal("rejected command started synchronization")
			}
		})
	}
}

func TestDNSAdoptsExistingBlockyServiceWithoutTakingOwnership(t *testing.T) {
	entry := Domain{Type: "service", Port: 8443}
	setupTestRoot(t, &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{"alias.example.net": entry}})
	if err := cmdDNS([]string{"set", "alias.example.net"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdDNS([]string{"set", "dns.example.org"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Domains["alias.example.net"] != entry {
		t.Fatal("existing user-managed alias was removed or changed")
	}
}

func TestServiceCannotRepurposePrimaryDNS(t *testing.T) {
	cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.net"}, Domains: map[string]Domain{
		"dns.example.net": {Type: "service", IP: "127.0.0.1", Port: 8443, ManagedBy: "dns"},
	}}
	setupTestRoot(t, cfg)
	before, err := os.ReadFile(getPaths().ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdService([]string{"dns.example.net", "8080"}); err == nil {
		t.Fatal("primary DNS backend silently replaced")
	}
	after, err := os.ReadFile(getPaths().ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected service command changed config")
	}
}

func TestDNSFailureRestoresPreviousOwnedRoute(t *testing.T) {
	cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.old.example"}, Domains: map[string]Domain{
		"dns.old.example": {Type: "service", IP: "127.0.0.1", Port: 8443, ManagedBy: "dns"},
	}, GeoSites: []string{"broken"}}
	setupTestRoot(t, cfg)
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DLC, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdDNS([]string{"set", "dns.new.example"}); err == nil {
		t.Fatal("expected failed sync")
	}
	after, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed DNS switch did not restore route and ownership")
	}
}

func TestDNSLegacySyntaxRemainsCompatible(t *testing.T) {
	setupTestRoot(t, &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{}})
	if err := cmdDNS([]string{"dns.example.net"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.DNSDomain != "dns.example.net" {
		t.Fatal("legacy syntax no longer sets DNS")
	}
}

func TestServiceCannotRepurposeLegacyInferredDNS(t *testing.T) {
	cfg := &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
		"dns.example.net": {Type: "service", IP: "127.0.0.1", Port: 8443},
	}}
	setupTestRoot(t, cfg)
	if err := cmdService([]string{"dns.example.net", "8080"}); err == nil {
		t.Fatal("legacy primary DNS backend silently replaced")
	}
	got, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if dnsDomain(got) != "dns.example.net" || got.Domains["dns.example.net"].Port != 8443 {
		t.Fatal("legacy DNS endpoint changed")
	}
}
