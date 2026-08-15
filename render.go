package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func sortedDomains(cfg *Config, kind string) []string {
	var out []string
	for d, v := range cfg.Domains {
		if v.Type == kind {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func ensureProxyIP(cfg *Config) error {
	if cfg.Settings.ProxyIP != "" && cfg.Settings.ProxyIP != "0.0.0.0" {
		return nil
	}
	ip, err := detectPublicIP()
	if err != nil {
		return err
	}
	cfg.Settings.ProxyIP = ip
	if err = saveConfig(cfg); err != nil {
		return err
	}
	info("Updated proxy_ip to: %s", ip)
	return nil
}
func renderBlocky(cfg *Config) ([]byte, error) {
	if err := ensureProxyIP(cfg); err != nil {
		return nil, err
	}

	mapping := map[string]string{}
	for _, domain := range sortedDomains(cfg, "proxy") {
		mapping[domain] = cfg.Settings.ProxyIP
	}

	out := map[string]interface{}{
		"upstreams": map[string]interface{}{
			"groups": map[string]interface{}{
				"default": []string{
					"https://8.8.8.8/dns-query",
					"https://1.1.1.1/dns-query",
				},
			},
		},
		"customDNS": map[string]interface{}{
			"mapping": mapping,
		},
		"ports": map[string]interface{}{
			"dns":  53,
			"http": 4000,
		},
		"log": map[string]interface{}{"level": "info"},
	}
	domain := dnsDomain(cfg)
	if cert, key := certPaths(domain); cert != "" && key != "" {
		ports := out["ports"].(map[string]interface{})
		ports["tls"] = 853
		ports["https"] = 8443
		out["certFile"] = certRefPath(cert)
		out["keyFile"] = certRefPath(key)
	}

	return yaml.Marshal(out)
}

func renderAngieStream(cfg *Config) string {
	var b strings.Builder
	b.WriteString("map $ssl_preread_server_name $flowgate_origin {\n")
	b.WriteString("    hostnames;\n")
	b.WriteString("    default \"\";\n")
	for _, domain := range sortedDomains(cfg, "proxy") {
		fmt.Fprintf(&b, "    .%s $ssl_preread_server_name;\n", domain)
	}
	b.WriteString("}\n\n")

	services := sortedDomains(cfg, "service")
	if len(services) > 0 {
		b.WriteString("server {\n    listen 443;\n    listen [::]:443;\n")
		fmt.Fprintf(&b, "    server_name %s;\n", strings.Join(services, " "))
		b.WriteString("    proxy_pass 127.0.0.1:44301;\n    ssl_preread on;\n}\n\n")
	}
	b.WriteString("server {\n")
	b.WriteString("    listen 443;\n    listen [::]:443;\n")
	b.WriteString("    proxy_pass $flowgate_origin:443;\n")
	b.WriteString("    ssl_preread on;\n")
	b.WriteString("}\n")
	return b.String()
}

func certRefPath(path string) string {
	root := os.Getenv("FLOWGATE_ROOT")
	if root != "" && root != "/" {
		if rel, err := filepath.Rel(root, path); err == nil {
			return "/" + rel
		}
	}
	return path
}

func renderAngieHTTP(cfg *Config) string {
	var b strings.Builder
	services := sortedDomains(cfg, "service")
	for _, domain := range services {
		name := acmeName(domain)
		fmt.Fprintf(&b, "acme_client acme_%s https://acme-v02.api.letsencrypt.org/directory;\n", name)
	}
	if len(services) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("# HTTP listener for ACME and HTTPS redirects\n")
	b.WriteString("server {\n    listen 80;\n    server_name _;\n")
	b.WriteString("    location /.well-known/acme-challenge/ { }\n")
	b.WriteString("    location / { return 301 https://$host$request_uri; }\n}\n\n")
	for _, domain := range services {
		entry := cfg.Domains[domain]
		ip := entry.IP
		if ip == "" {
			ip = "127.0.0.1"
		}
		name := acmeName(domain)

		cert := rooted(filepath.Join("/var/lib/angie/acme", "acme_"+name, "certificate.pem"))
		key := rooted(filepath.Join("/var/lib/angie/acme", "acme_"+name, "private.key"))
		if !nonEmptyFile(cert) || !fileExists(key) {
			cert = rooted("/etc/ssl/certs/ssl-cert-snakeoil.pem")
			key = rooted("/etc/ssl/private/ssl-cert-snakeoil.key")
		}

		scheme := "http"
		if entry.Port == 8443 {
			scheme = "https"
		}
		fmt.Fprintf(&b, "# Service: %s\n", domain)
		fmt.Fprintf(&b, "server {\n    listen 44301 ssl;\n    server_name %s;\n", domain)
		fmt.Fprintf(&b, "    acme acme_%s;\n", name)
		fmt.Fprintf(&b, "    ssl_certificate %s;\n", certRefPath(cert))
		fmt.Fprintf(&b, "    ssl_certificate_key %s;\n", certRefPath(key))
		b.WriteString("    location / {\n")
		fmt.Fprintf(&b, "        proxy_pass %s://%s:%d;\n", scheme, ip, entry.Port)
		b.WriteString("        proxy_set_header Host $host;\n")
		b.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
		b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
		b.WriteString("        proxy_set_header X-Forwarded-Proto https;\n")
		b.WriteString("    }\n}\n\n")
	}
	return b.String()
}

func acmeName(domain string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(domain)
}
