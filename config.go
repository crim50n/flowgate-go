package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var domainRE = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,}$`)

func validDomain(s string) bool { return len(s) > 0 && len(s) <= 253 && domainRE.MatchString(s) }
func validIP(s string) bool {
	if s == "" {
		return true
	}
	ip := net.ParseIP(s)
	return ip != nil && strings.Contains(s, ".")
}
func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validateConfig(cfg *Config) error {
	if cfg.Settings.ProxyIP != "" && !validIP(cfg.Settings.ProxyIP) {
		return fmt.Errorf("invalid settings.proxy_ip: %s", cfg.Settings.ProxyIP)
	}
	for domain, entry := range cfg.Domains {
		if !validDomain(domain) {
			return fmt.Errorf("invalid domain: %s", domain)
		}
		switch entry.Type {
		case "proxy":
		case "service":
			if !validPort(entry.Port) {
				return fmt.Errorf("invalid service port for %s: %d", domain, entry.Port)
			}
			if !validIP(entry.IP) {
				return fmt.Errorf("invalid service IP for %s: %s", domain, entry.IP)
			}
		default:
			return fmt.Errorf("invalid type for %s: %q", domain, entry.Type)
		}
	}
	if d := cfg.Settings.DNSDomain; d != "" {
		if !validDomain(d) {
			return fmt.Errorf("invalid settings.dns_domain: %s", d)
		}
		entry, ok := cfg.Domains[d]
		if !ok || entry.Type != "service" {
			return fmt.Errorf("settings.dns_domain must reference a service domain: %s", d)
		}
	}
	return nil
}

func detectPublicIP() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, u := range []string{"https://api.ipify.org", "https://ifconfig.me/ip", "https://icanhazip.com"} {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b := make([]byte, 128)
		n, _ := resp.Body.Read(b)
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			v := strings.TrimSpace(string(b[:n]))
			if net.ParseIP(v) != nil {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("failed to detect public IP")
}

func loadConfig() (*Config, error) {
	p := getPaths()
	b, err := os.ReadFile(p.ConfigFile)
	if os.IsNotExist(err) {
		ip, e := detectPublicIP()
		if e != nil {
			return nil, e
		}
		cfg := &Config{Settings: Settings{ProxyIP: ip}, Domains: map[string]Domain{}}
		if e = saveConfig(cfg); e != nil {
			return nil, e
		}
		info("Detected public IP: %s", ip)
		success("Created initial configuration at %s", p.ConfigFile)
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err = yaml.Unmarshal(b, cfg); err != nil {
		return nil, err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]Domain{}
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg *Config) error {
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	p := getPaths()
	if err := os.MkdirAll(p.ConfigDir, 0755); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := p.ConfigFile + ".tmp"
	if err = os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p.ConfigFile)
}

func dnsDomain(cfg *Config) string {
	if cfg.Settings.DNSDomain != "" {
		return cfg.Settings.DNSDomain
	}
	for d := range cfg.Domains {
		if strings.HasPrefix(d, "doh.") || strings.HasPrefix(d, "dns.") {
			return d
		}
	}
	return ""
}

func certPaths(domain string) (string, string) {
	if domain == "" {
		return "", ""
	}
	safe := strings.NewReplacer(".", "_", "-", "_").Replace(domain)
	base := rooted(filepath.Join("/var/lib/angie/acme", "acme_"+safe))
	cert, key := filepath.Join(base, "certificate.pem"), filepath.Join(base, "private.key")
	if nonEmptyFile(cert) && fileExists(key) {
		return cert, key
	}

	old := rooted("/var/lib/angie/acme/letsencrypt")
	cert, key = filepath.Join(old, "certificate.pem"), filepath.Join(old, "private.key")
	if nonEmptyFile(cert) && fileExists(key) {
		return cert, key
	}
	return "", ""
}
