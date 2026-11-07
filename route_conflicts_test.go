package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSyncCandidateRejectsProxyRulesCoveringServices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service string
		rule    ProxyRule
	}{
		{"exact", "app.example.com", ProxyRule{RuleRootDomain, "app.example.com"}},
		{"parent suffix", "app.example.com", ProxyRule{RuleRootDomain, "example.com"}},
		{"nested child", "api.app.example.com", ProxyRule{RuleRootDomain, "example.com"}},
		{"full normalized to suffix", "app.example.com", ProxyRule{RuleFull, "example.com"}},
		{"case insensitive", "App.Example.COM", ProxyRule{RuleRootDomain, "EXAMPLE.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Settings: Settings{ProxyIP: "203.0.113.10"},
				Domains: map[string]Domain{
					tc.service: {Type: "service", IP: "127.0.0.1", Port: 8080},
				},
			}
			candidate, err := buildSyncCandidate(cfg, []ProxyRule{tc.rule})
			if err == nil || candidate != nil {
				t.Fatal("service DNS interception must fail before producing a sync candidate")
			}
			for _, want := range []string{tc.service, strings.ToLower(tc.rule.Value)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("conflict error %q does not identify %q", err, want)
				}
			}
		})
	}
}

func TestSyncCandidatePreservesNonconflictingServices(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	for _, tc := range []struct {
		name    string
		service string
		rule    ProxyRule
	}{
		{"sibling", "app.example.com", ProxyRule{RuleRootDomain, "api.example.com"}},
		{"label boundary", "app.example.com", ProxyRule{RuleRootDomain, "ample.com"}},
		{"service parent", "example.com", ProxyRule{RuleRootDomain, "api.example.com"}},
		{"keyword SNI only", "app.example.com", ProxyRule{RulePlain, "example"}},
		{"regex SNI only", "app.example.com", ProxyRule{RuleRegex, "^app[.]example[.]com$"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Settings: Settings{ProxyIP: "203.0.113.10"},
				Domains: map[string]Domain{
					tc.service: {Type: "service", IP: "127.0.0.1", Port: 8080},
				},
			}
			candidate, err := buildSyncCandidate(cfg, []ProxyRule{tc.rule})
			if err != nil {
				t.Fatal(err)
			}
			var blocky struct {
				CustomDNS struct {
					Mapping map[string]string
				} `yaml:"customDNS"`
			}
			if err := yaml.Unmarshal(candidate.blockyConfig, &blocky); err != nil {
				t.Fatal(err)
			}
			if _, exists := blocky.CustomDNS.Mapping[tc.service]; exists {
				t.Fatal("service domain must remain absent from Blocky custom DNS")
			}
			if tc.rule.Type == RuleRootDomain &&
				blocky.CustomDNS.Mapping[tc.rule.Value] != cfg.Settings.ProxyIP {
				t.Fatal("unrelated proxy DNS mapping was lost")
			}
			serviceRoute := "server_name " + tc.service + ";"
			if !strings.Contains(string(candidate.angieStream), serviceRoute) ||
				!strings.Contains(string(candidate.angieHTTP), serviceRoute) {
				t.Fatal("explicit reverse-proxy service route was lost")
			}
		})
	}
}

func TestSyncCandidateRejectsServiceCoveredByGeoSite(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	data := buildTestDLC("CATEGORY-TEST", []ProxyRule{
		{Type: RuleRootDomain, Value: "example.com"},
	})
	if err := os.WriteFile(p.DLC, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"app.example.com": {Type: "service", IP: "127.0.0.1", Port: 8080},
		},
		GeoSites: []string{"category-test"},
	}
	rules, err := resolveProxyRules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if candidate, err := buildSyncCandidate(cfg, rules); err == nil || candidate != nil {
		t.Fatal("expanded GeoSite suffix must not silently rewrite a service domain")
	}
}

func TestServiceOnlyCandidateHasNoDNSMappings(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"app.example.com": {Type: "service", IP: "127.0.0.1", Port: 8080},
		},
	}
	candidate, err := buildSyncCandidate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var blocky struct {
		CustomDNS struct {
			Mapping map[string]string
		} `yaml:"customDNS"`
	}
	if err := yaml.Unmarshal(candidate.blockyConfig, &blocky); err != nil {
		t.Fatal(err)
	}
	if len(blocky.CustomDNS.Mapping) != 0 {
		t.Fatalf("service-only configuration rewrites DNS: %v", blocky.CustomDNS.Mapping)
	}
}
