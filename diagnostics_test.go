package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type diagnosticFileState struct {
	Mode    fs.FileMode
	ModTime int64
	Data    string
}

func diagnosticTree(t *testing.T, root string) map[string]diagnosticFileState {
	t.Helper()
	tree := make(map[string]diagnosticFileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		state := diagnosticFileState{Mode: stat.Mode(), ModTime: stat.ModTime().UnixNano()}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.Data = string(data)
		}
		tree[path] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func writeDiagnosticDLC(t *testing.T, data []byte) {
	t.Helper()
	path := getPaths().DLC
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckIsReadOnlyIncludingMissingFiles(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		config     bool
		proxyIP    string
		geosites   []string
		dlc        []byte
		wantError  string
		wantOutput string
	}{
		{name: "missing config", args: []string{"tbank.ru"}, wantError: "configuration is missing"},
		{name: "invalid arguments", wantError: "exactly one domain"},
		{name: "invalid domain", args: []string{"https://tbank.ru"}, wantError: "invalid domain"},
		{name: "multiple trailing dots", args: []string{"tbank.ru.."}, wantError: "invalid domain"},
		{name: "proxy", args: []string{"tbank.ru"}, config: true, proxyIP: "203.0.113.10", wantOutput: "DNS: A -> 203.0.113.10"},
		{name: "unset proxy IP", args: []string{"tbank.ru"}, config: true, wantOutput: "proxy_ip is not set"},
		{name: "missing DLC", args: []string{"tbank.ru"}, config: true, geosites: []string{"finance"}, wantError: "local dlc.dat"},
		{name: "invalid DLC", args: []string{"tbank.ru"}, config: true, geosites: []string{"finance"}, dlc: []byte("broken"), wantError: "local dlc.dat"},
		{name: "missing selector", args: []string{"tbank.ru"}, config: true, geosites: []string{"finance"}, dlc: buildTestDLC("OTHER", nil), wantError: "geosite:finance is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("FLOWGATE_ROOT", root)
			if tc.config {
				cfg := &Config{
					Settings: Settings{ProxyIP: tc.proxyIP},
					Domains:  map[string]Domain{"tbank.ru": {Type: "proxy"}},
					GeoSites: tc.geosites,
				}
				if err := saveConfig(cfg); err != nil {
					t.Fatal(err)
				}
			}
			if tc.dlc != nil {
				writeDiagnosticDLC(t, tc.dlc)
			}
			before := diagnosticTree(t, root)
			requests := 0
			transport := http.DefaultTransport
			http.DefaultTransport = diagnosticRoundTripper(func(req *http.Request) (*http.Response, error) {
				requests++
				return nil, fmt.Errorf("unexpected diagnostic network request: %s", req.URL)
			})
			t.Cleanup(func() { http.DefaultTransport = transport })
			var out bytes.Buffer
			err := checkDomain(&out, tc.args)
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if tc.wantOutput != "" && !strings.Contains(out.String(), tc.wantOutput) {
				t.Fatalf("output = %q, want %q", out.String(), tc.wantOutput)
			}
			if !reflect.DeepEqual(before, diagnosticTree(t, root)) {
				t.Fatal("diagnostic changed files, permissions or modification times")
			}
			if requests != 0 {
				t.Fatalf("diagnostic made %d network requests", requests)
			}
		})
	}
	for _, command := range []string{"check", "explain"} {
		if commandNeedsLock(command) {
			t.Fatalf("%s must not create an exclusive process lock", command)
		}
	}
}

func TestCheckExplainsAllMatchingSources(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"tbank.ru": {Type: "proxy"}},
		GeoSites: []string{"payments", "category-finance"},
	}
	setupTestRoot(t, cfg)
	data := buildTestDLC("CATEGORY-FINANCE", []ProxyRule{
		{Type: RuleRootDomain, Value: "tbank.ru"},
		{Type: RuleRootDomain, Value: "tbank.ru"},
		{Type: RulePlain, Value: "TBANK"},
	})
	data = append(data, buildTestDLC("PAYMENTS", []ProxyRule{
		{Type: RuleFull, Value: "api.tbank.ru"},
		{Type: RuleRegex, Value: "^api[.]"},
	})...)
	writeDiagnosticDLC(t, data)
	var out bytes.Buffer
	if err := checkDomain(&out, []string{"API.TBANK.RU."}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Configured route for api.tbank.ru",
		"DNS: A -> 203.0.113.10",
		"Angie SNI: TLS passthrough -> api.tbank.ru:443",
		"explicit tbank.ru -> domain:\"tbank.ru\" (DNS + SNI suffix)",
		"geosite:category-finance -> domain:\"tbank.ru\" (DNS + SNI suffix)",
		"geosite:category-finance -> keyword:\"TBANK\" (SNI only; no DNS interception)",
		"geosite:payments -> full:\"api.tbank.ru\" (DNS + SNI suffix)",
		"geosite:payments -> regexp:\"^api[.]\" (SNI only; no DNS interception)",
		"Configuration inspection only",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, out.String())
		}
	}
	if strings.Count(out.String(), "geosite:category-finance -> domain:") != 1 {
		t.Fatal("duplicate database rule was reported more than once")
	}
}

func TestCheckDistinguishesDNSFromSNIOnlyRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule ProxyRule
		sni  string
	}{
		{name: "keyword", rule: ProxyRule{Type: RulePlain, Value: "BANK"}, sni: "TLS passthrough"},
		{name: "regexp", rule: ProxyRule{Type: RuleRegex, Value: "^bank[.]"}, sni: "TLS passthrough"},
		{name: "PCRE-only", rule: ProxyRule{Type: RuleRegex, Value: "(?=bank)bank"}, sni: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestRoot(t, &Config{
				Settings: Settings{ProxyIP: "203.0.113.10"},
				Domains:  map[string]Domain{},
				GeoSites: []string{"test"},
			})
			writeDiagnosticDLC(t, buildTestDLC("TEST", []ProxyRule{tc.rule}))
			var out bytes.Buffer
			if err := checkDomain(&out, []string{"bank.example"}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "DNS: upstream resolution") ||
				!strings.Contains(out.String(), "Angie SNI: "+tc.sni) {
				t.Fatalf("unexpected output:\n%s", out.String())
			}
		})
	}
}

func TestCheckServiceRoutingAndLegacyOverlap(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"app.example.com": {Type: "service", Port: 8080},
		},
	}
	setupTestRoot(t, cfg)
	var out bytes.Buffer
	if err := checkDomain(&out, []string{"app.example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DNS: upstream resolution") ||
		!strings.Contains(out.String(), "exact service -> http://127.0.0.1:8080") {
		t.Fatalf("service was treated as a Smart DNS rule:\n%s", out.String())
	}
	out.Reset()
	if err := checkDomain(&out, []string{"api.app.example.com"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Angie SNI: no configured route.") {
		t.Fatalf("service incorrectly matched a subdomain:\n%s", out.String())
	}
	cfg.Domains["example.com"] = Domain{Type: "proxy"}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := checkDomain(&out, []string{"app.example.com"})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("legacy overlap error = %v", err)
	}
	if !strings.Contains(out.String(), "DNS: A -> 203.0.113.10") ||
		!strings.Contains(out.String(), "exact service -> http://127.0.0.1:8080") ||
		!strings.Contains(out.String(), "explicit example.com") {
		t.Fatalf("legacy overlap was hidden:\n%s", out.String())
	}
}

func TestDiagnosticSuffixBoundaryAndFullSemantics(t *testing.T) {
	for _, kind := range []ProxyRuleType{RuleRootDomain, RuleFull} {
		rule := ProxyRule{Type: kind, Value: "tbank.ru"}
		for _, domain := range []string{"tbank.ru", "api.tbank.ru", "x.api.tbank.ru"} {
			matched, err := diagnosticRuleMatches(rule, domain)
			if err != nil || !matched {
				t.Fatalf("rule %#v did not match %q: %v", rule, domain, err)
			}
		}
		for _, domain := range []string{"not-tbank.ru", "tbank.ru.example"} {
			matched, err := diagnosticRuleMatches(rule, domain)
			if err != nil || matched {
				t.Fatalf("rule %#v falsely matched %q: %v", rule, domain, err)
			}
		}
	}
}
