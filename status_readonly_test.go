package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestStatusBeforeInitPreservesStartupSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	t.Setenv("PROXY_IP", "203.0.113.10")
	t.Setenv("DNS_DOMAIN", "dns.startup-test.invalid")
	t.Setenv("PATH", t.TempDir())
	requests := 0
	transport := http.DefaultTransport
	http.DefaultTransport = diagnosticRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("198.51.100.77")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })

	before := diagnosticTree(t, root)
	if err := cmdStatus(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("status before initialization = %v, want missing configuration", err)
	}
	if requests != 0 {
		t.Errorf("early status made %d public-IP requests", requests)
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, root)) {
		t.Error("early status created or changed files")
	}

	// This is the startup order when a readiness poll runs before init saves
	// the configuration. Status must not choose the address on init's behalf.
	if err := cmdInit(); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadExistingConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.ProxyIP != "203.0.113.10" {
		t.Errorf("init ignored PROXY_IP after early status: got %s", cfg.Settings.ProxyIP)
	}
	if cfg.Settings.DNSDomain != "dns.startup-test.invalid" {
		t.Errorf("init ignored DNS_DOMAIN after early status: got %q", cfg.Settings.DNSDomain)
	}
	if !isBlockyService(cfg.Domains["dns.startup-test.invalid"]) {
		t.Error("init did not create the configured DNS service")
	}
	if requests != 0 {
		t.Fatalf("explicit PROXY_IP startup made %d network requests", requests)
	}
}

func TestStatusLeavesExistingConfigurationUnchanged(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"example.com": {Type: "proxy"}},
	}
	root := setupTestRoot(t, cfg)
	t.Setenv("PATH", t.TempDir())
	before := diagnosticTree(t, root)
	requests := 0
	transport := http.DefaultTransport
	http.DefaultTransport = diagnosticRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("status must not contact the network")
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	if err := cmdStatus(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, root)) {
		t.Fatal("status changed existing files")
	}
	if requests != 0 {
		t.Fatalf("status made %d network requests", requests)
	}
}
