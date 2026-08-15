package main

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed flowgate.yaml.default
var defaultConfigYAML []byte

func installDefaultConfig() (*Config, error) {
	p := getPaths()
	if fileExists(p.ConfigFile) {
		return loadConfig()
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(defaultConfigYAML, cfg); err != nil {
		return nil, err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]Domain{}
	}

	proxyIP := os.Getenv("PROXY_IP")
	switch proxyIP {
	case "":
		if cfg.Settings.ProxyIP == "" || cfg.Settings.ProxyIP == "0.0.0.0" {
			ip, err := detectPublicIP()
			if err != nil {
				return nil, err
			}
			cfg.Settings.ProxyIP = ip
		}
	case "auto":
		ip, err := detectPublicIP()
		if err != nil {
			return nil, err
		}
		cfg.Settings.ProxyIP = ip
	default:
		if !validIP(proxyIP) {
			return nil, fmt.Errorf("invalid PROXY_IP: %s", proxyIP)
		}
		cfg.Settings.ProxyIP = proxyIP
	}
	if dns := os.Getenv("DNS_DOMAIN"); dns != "" {
		if !validDomain(dns) {
			return nil, fmt.Errorf("invalid DNS_DOMAIN: %s", dns)
		}
		cfg.Settings.DNSDomain = dns
		if _, ok := cfg.Domains[dns]; !ok {
			cfg.Domains[dns] = Domain{Type: "service", IP: "127.0.0.1", Port: 8443}
		}
	}
	if err := saveConfig(cfg); err != nil {
		return nil, err
	}
	success("Created initial configuration at %s", p.ConfigFile)
	return cfg, nil
}
