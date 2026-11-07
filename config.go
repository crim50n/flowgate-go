package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
}
func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validateConfig(cfg *Config) error {
	if cfg.Settings.ProxyIP != "" && !validIP(cfg.Settings.ProxyIP) {
		return fmt.Errorf("invalid settings.proxy_ip: %s", cfg.Settings.ProxyIP)
	}
	seenGeoSites := make(map[string]struct{}, len(cfg.GeoSites))
	for _, selector := range cfg.GeoSites {
		name := normalizeGeoSiteName(selector)
		if !validGeoSiteName(name) {
			return fmt.Errorf("invalid geosite selector: %q", selector)
		}
		if _, exists := seenGeoSites[name]; exists {
			return fmt.Errorf("duplicate geosite selector: %s", name)
		}
		seenGeoSites[name] = struct{}{}
	}
	seenDomains := make(map[string]string, len(cfg.Domains))
	seenACMENames := make(map[string]string)
	for _, domain := range sortedDomainNames(cfg) {
		entry := cfg.Domains[domain]
		canonical := strings.ToLower(domain)
		if previous, exists := seenDomains[canonical]; exists {
			return fmt.Errorf("duplicate domain names differing only by case: %s and %s", previous, domain)
		}
		seenDomains[canonical] = domain
		if !validDomain(domain) {
			return fmt.Errorf("invalid domain: %s", domain)
		}
		switch entry.Type {
		case "proxy":
		case "service":
			name := acmeName(canonical)
			if previous, exists := seenACMENames[name]; exists {
				return fmt.Errorf("service domains %s and %s share ACME client acme_%s; choose noncolliding service hostnames", previous, domain, name)
			}
			seenACMENames[name] = domain
			if !validPort(entry.Port) {
				return fmt.Errorf("invalid service port for %s: %d", domain, entry.Port)
			}
			if !validIP(entry.IP) {
				return fmt.Errorf("invalid service IP for %s: %s", domain, entry.IP)
			}
		default:
			return fmt.Errorf("invalid type for %s: %q", domain, entry.Type)
		}
		if entry.ManagedBy != "" && (entry.ManagedBy != "dns" || !isBlockyService(entry)) {
			return fmt.Errorf("invalid managed_by for %s: only a local Blocky service may be managed by dns", domain)
		}
	}
	if d := cfg.Settings.DNSDomain; d != "" {
		if !validDomain(d) {
			return fmt.Errorf("invalid settings.dns_domain: %s", d)
		}
		entry, ok := cfg.Domains[seenDomains[strings.ToLower(d)]]
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
			if v != "" && validIP(v) {
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
		cfg := &Config{Settings: Settings{ProxyIP: ip}, Domains: map[string]Domain{}, GeoSites: []string{}}
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
	return decodeConfig(b)
}

// loadExistingConfig never initializes files or probes the network.
func loadExistingConfig() (*Config, error) {
	path := getPaths().ConfigFile
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]Domain{}
	}
	if cfg.GeoSites == nil {
		cfg.GeoSites = []string{}
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	canonical := make(map[string]Domain, len(cfg.Domains))
	for domain, entry := range cfg.Domains {
		canonical[strings.ToLower(domain)] = entry
	}
	cfg.Domains = canonical
	cfg.Settings.DNSDomain = strings.ToLower(cfg.Settings.DNSDomain)
	return cfg, nil
}

func sortedDomainNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Domains))
	for name := range cfg.Domains {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isBlockyService(entry Domain) bool {
	return entry.Type == "service" && entry.Port == 8443 && (entry.IP == "" || entry.IP == "127.0.0.1")
}

func saveConfig(cfg *Config) error {
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	p := getPaths()
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(p.ConfigFile, b, 0644)
}

func saveAppliedConfig(cfg *Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(getPaths().AppliedConfig, data, 0644)
}

func restoreAppliedConfig() error {
	p := getPaths()
	data, err := os.ReadFile(p.AppliedConfig)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Domains == nil {
		cfg.Domains = map[string]Domain{}
	}
	if err := validateConfig(&cfg); err != nil {
		return err
	}
	return writeAtomic(p.ConfigFile, data, 0644)
}

func inferredDNSDomains(cfg *Config) []string {
	var candidates []string
	for _, domain := range sortedDomainNames(cfg) {
		name := strings.ToLower(domain)
		if isBlockyService(cfg.Domains[domain]) && (strings.HasPrefix(name, "doh.") || strings.HasPrefix(name, "dns.")) {
			candidates = append(candidates, name)
		}
	}
	return candidates
}

func dnsDomain(cfg *Config) string {
	if cfg.Settings.DNSDomain != "" {
		return strings.ToLower(cfg.Settings.DNSDomain)
	}
	// Preserve legacy inference only when one actual Blocky endpoint exists.
	if candidates := inferredDNSDomains(cfg); len(candidates) == 1 {
		return candidates[0]
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
