package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

func hasVerbose(args []string) bool {
	for _, arg := range args {
		if arg == "-v" || arg == "--verbose" {
			return true
		}
	}
	return false
}

func cmdInit() error {
	header("Initializing Flowgate Environment")
	if err := ensureEnvironment(); err != nil {
		return err
	}
	if _, err := installDefaultConfig(); err != nil {
		return err
	}
	if err := syncAll(); err != nil {
		return err
	}
	header("Initialization Complete")
	fmt.Println("Next steps:")
	fmt.Println("  flowgate add category-dev")
	fmt.Println("  flowgate add example.com")
	fmt.Println("  flowgate service app.example.com 8080")
	fmt.Println("  flowgate dns dns.example.com")
	fmt.Println("  flowgate status")
	return nil
}

func applyConfig(cfg *Config) error {
	snap, err := snapshotFile(getPaths().ConfigFile)
	if err != nil {
		return err
	}
	if err := saveConfig(cfg); err != nil {
		return err
	}
	if err := syncAll(); err != nil {
		_ = restoreSnapshot(snap)
		return err
	}
	return nil
}

func cmdAdd(targets []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("add requires at least one domain or geosite")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	changed := false
	var db *GeoSiteDB
	for _, target := range targets {
		explicitGeoSite := len(target) >= 8 && strings.EqualFold(target[:8], "geosite:")
		if validDomain(target) && !explicitGeoSite {
			domain := strings.ToLower(target)
			if _, exists := cfg.Domains[domain]; exists {
				warn("Exists: %s", domain)
				continue
			}
			cfg.Domains[domain] = Domain{Type: "proxy"}
			success("Added proxy: %s", domain)
			changed = true
			continue
		}

		selector := normalizeGeoSiteName(target)
		if !validGeoSiteName(selector) {
			warn("Invalid domain or geosite: %s", target)
			continue
		}
		if geoSiteIndex(cfg.GeoSites, selector) >= 0 {
			warn("Exists: %s", selector)
			continue
		}
		if db == nil {
			required := append([]string{}, cfg.GeoSites...)
			required = append(required, selector)
			if err := ensureDLCFor(required); err != nil {
				return err
			}
			db, err = loadGeoSiteDB(getPaths().DLC)
			if err != nil {
				return err
			}
		}
		if _, ok := db.Rules(selector); !ok {
			warn("GeoSite not found: %s", selector)
			continue
		}
		cfg.GeoSites = append(cfg.GeoSites, selector)
		sort.Strings(cfg.GeoSites)
		success("Added GeoSite: %s", selector)
		changed = true
	}
	if !changed {
		return nil
	}
	return applyConfig(cfg)
}

func geoSiteIndex(items []string, name string) int {
	name = normalizeGeoSiteName(name)
	for i, item := range items {
		if normalizeGeoSiteName(item) == name {
			return i
		}
	}
	return -1
}

func cmdService(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("service requires DOMAIN PORT [--ip IP]")
	}
	domain := strings.ToLower(args[0])
	port, err := strconv.Atoi(args[1])
	if err != nil || !validPort(port) {
		return fmt.Errorf("invalid port: %s", args[1])
	}
	if !validDomain(domain) {
		return fmt.Errorf("invalid domain format: %s", domain)
	}
	ip, err := parseServiceIP(args[2:])
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Domains[domain] = Domain{Type: "service", IP: ip, Port: port}
	success("Set service: %s -> %s:%d", domain, ip, port)
	return applyConfig(cfg)
}

func parseServiceIP(args []string) (string, error) {
	ip := "127.0.0.1"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--ip":
			if i+1 >= len(args) {
				return "", fmt.Errorf("--ip requires a value")
			}
			ip = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--ip="):
			ip = strings.TrimPrefix(args[i], "--ip=")
		default:
			return "", fmt.Errorf("unknown service option: %s", args[i])
		}
	}
	if !validIP(ip) {
		return "", fmt.Errorf("invalid IP address: %s", ip)
	}
	return ip, nil
}

func cmdDNS(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("dns requires exactly one domain")
	}
	domain := strings.ToLower(args[0])
	if !validDomain(domain) {
		return fmt.Errorf("invalid domain format: %s", domain)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	entry := Domain{Type: "service", IP: "127.0.0.1", Port: 8443}
	if current, exists := cfg.Domains[domain]; !exists || current != entry {
		info("Configuring reverse-proxy route for %s (Blocky HTTPS port 8443)...", domain)
		cfg.Domains[domain] = entry
	}
	cfg.Settings.DNSDomain = domain
	success("Primary DNS domain set to: %s", domain)
	return applyConfig(cfg)
}

func cmdRemove(targets []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("remove requires at least one domain or geosite")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	changed := false
	for _, target := range targets {
		domain := strings.ToLower(target)
		if _, exists := cfg.Domains[domain]; exists {
			delete(cfg.Domains, domain)
			if cfg.Settings.DNSDomain == domain {
				cfg.Settings.DNSDomain = ""
			}
			success("Removed: %s", domain)
			changed = true
			continue
		}
		selector := normalizeGeoSiteName(target)
		if idx := geoSiteIndex(cfg.GeoSites, selector); idx >= 0 {
			cfg.GeoSites = append(cfg.GeoSites[:idx], cfg.GeoSites[idx+1:]...)
			success("Removed GeoSite: %s", selector)
			changed = true
			continue
		}
		warn("Not found: %s", target)
	}
	if !changed {
		return nil
	}
	return applyConfig(cfg)
}

func cmdUpdate() error {
	header("Updating Domain List Community")
	p := getPaths()
	dataSnap, err := snapshotFile(p.DLC)
	if err != nil {
		return err
	}
	sumSnap, err := snapshotFile(p.DLCSum)
	if err != nil {
		return err
	}
	changed, hash, err := updateDLC()
	if err != nil {
		return err
	}
	if changed {
		success("Updated dlc.dat (%s)", hash[:12])
	} else {
		info("dlc.dat is already current (%s)", hash[:12])
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if changed && len(cfg.GeoSites) > 0 {
		if err := syncAll(); err != nil {
			_ = restoreSnapshot(dataSnap)
			_ = restoreSnapshot(sumSnap)
			return err
		}
	}
	return nil
}

func cmdStatus() error {
	header("Service Status")
	printStatus("Angie", isActive("angie"))
	printStatus("Blocky", isActive("blocky"))

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Domains) == 0 && len(cfg.GeoSites) == 0 {
		info("No proxy rules configured.")
		return nil
	}
	if len(cfg.Domains) > 0 {
		header("Configured Domains (%d)", len(cfg.Domains))
		printDomainGroup(cfg, "service", "Reverse Proxy Services")
		printDomainGroup(cfg, "proxy", "Passthrough Domains")
	}
	if len(cfg.GeoSites) > 0 {
		fmt.Printf("\n%sGeoSite Selectors:%s\n", bold, reset)
		for _, selector := range cfg.GeoSites {
			fmt.Printf("  %s\n", selector)
		}
	}
	return nil
}

func printStatus(name string, active bool) {
	if active {
		fmt.Printf("  %-15s %sACTIVE%s\n", name, green, reset)
	} else {
		fmt.Printf("  %-15s %sINACTIVE%s\n", name, red, reset)
	}
}

func printDomainGroup(cfg *Config, kind, title string) {
	var domains []string
	for domain, entry := range cfg.Domains {
		if entry.Type == kind {
			domains = append(domains, domain)
		}
	}
	if len(domains) == 0 {
		return
	}
	sort.Strings(domains)
	fmt.Printf("\n%s%s:%s\n", bold, title, reset)
	for _, domain := range domains {
		entry := cfg.Domains[domain]
		if kind == "service" {
			fmt.Printf("  %-30s -> %s:%d\n", domain, entry.IP, entry.Port)
		} else {
			fmt.Printf("  %s\n", domain)
		}
	}
}

func cmdDoctor(verbose bool) error {
	header("Flowgate System Diagnostics")
	fmt.Printf("\n%sSystem:%s\n", bold, reset)
	fmt.Printf("  Init System: %s\n", detectInit())
	fmt.Printf("\n%sComponents:%s\n", bold, reset)
	showBinary("Angie", "angie", verbose)
	showBinary("Blocky", "blocky", verbose)
	fmt.Printf("\n%sServices:%s\n", bold, reset)
	printStatus("Angie", isActive("angie"))
	printStatus("Blocky", isActive("blocky"))
	if fileExists(getPaths().DLC) {
		fmt.Printf("  %-15s %savailable%s\n", "dlc.dat", green, reset)
	} else {
		fmt.Printf("  %-15s %smissing%s\n", "dlc.dat", yellow, reset)
	}
	if !commandExists("angie") {
		warn("Angie is not installed")
	}
	if !commandExists("blocky") {
		warn("Blocky is not installed")
	}
	return nil
}

func showBinary(label, name string, verbose bool) {
	if !commandExists(name) {
		fmt.Printf("  %-15s %snot found%s\n", label, red, reset)
		return
	}
	resolved := name
	if p, err := os.Stat("/usr/bin/" + name); err == nil && !p.IsDir() {
		resolved = "/usr/bin/" + name
	}
	if verbose {
		fmt.Printf("  %-15s %sinstalled%s (%s)\n", label, green, reset, resolved)
	} else {
		fmt.Printf("  %-15s %sinstalled%s\n", label, green, reset)
	}
}
