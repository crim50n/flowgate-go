package main

import "testing"

func TestValidateConfigRejectsInvalidEntries(t *testing.T) {
	tests := []Config{
		{Settings: Settings{ProxyIP: "not-an-ip"}, Domains: map[string]Domain{}},
		{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
			"bad domain": {Type: "proxy"},
		}},
		{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
			"example.com": {Type: "whatever"},
		}},
		{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
			"app.example.com": {Type: "service", IP: "127.0.0.1", Port: 0},
		}},
		{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
			"app.example.com": {Type: "service", IP: "invalid", Port: 8080},
		}},
	}
	for i := range tests {
		if err := validateConfig(&tests[i]); err == nil {
			t.Fatalf("case %d unexpectedly valid", i)
		}
	}
}
func TestValidateConfigRejectsDanglingDNSDomain(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.com"},
		Domains: map[string]Domain{
			"openai.com": {Type: "proxy"},
		},
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("dangling dns_domain unexpectedly valid")
	}
}

func TestValidateConfigAcceptsServiceDNSDomain(t *testing.T) {
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10", DNSDomain: "dns.example.com"},
		Domains: map[string]Domain{
			"dns.example.com": {Type: "service", IP: "127.0.0.1", Port: 8443},
		},
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
