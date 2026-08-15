package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testConfig() *Config {
	return &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains: map[string]Domain{
			"openai.com":      {Type: "proxy"},
			"app.example.com": {Type: "service", IP: "127.0.0.1", Port: 8080},
		},
	}
}

func TestBlockyContainsOnlyProxyDomains(t *testing.T) {
	data, err := renderBlocky(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		CustomDNS struct {
			Mapping map[string]string `yaml:"mapping"`
		} `yaml:"customDNS"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.CustomDNS.Mapping["openai.com"]; got != "203.0.113.10" {
		t.Fatalf("proxy mapping = %q", got)
	}
	if _, ok := cfg.CustomDNS.Mapping["app.example.com"]; ok {
		t.Fatal("service domain must not be written to Blocky")
	}
}

func TestAngieSeparatesPassthroughAndReverseProxy(t *testing.T) {
	stream := renderAngieStream(testConfig())
	if !strings.Contains(stream, ".openai.com $ssl_preread_server_name;") {
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
	if strings.Contains(http, "server_name openai.com;") {
		t.Fatal("passthrough proxy domain leaked into HTTP reverse proxy")
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
