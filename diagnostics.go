package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

type diagnosticMatch struct {
	Source string
	Rule   ProxyRule
	Reason string
}

func cmdCheck(args []string) error {
	return checkDomain(os.Stdout, args)
}

func checkDomain(out io.Writer, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("check requires exactly one domain (also available as 'flowgate explain DOMAIN')")
	}
	domain := strings.ToLower(strings.TrimSuffix(args[0], "."))
	if !validDomain(domain) {
		return fmt.Errorf("invalid domain format: %s", args[0])
	}
	cfg, err := loadExistingConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("configuration is missing; run 'flowgate init' before checking domains: %w", err)
		}
		return fmt.Errorf("read configuration: %w", err)
	}
	matches, unknown, err := diagnosticMatches(cfg, domain)
	if err != nil {
		return err
	}

	dnsProxy := false
	for _, match := range matches {
		if match.Rule.Type == RuleRootDomain || match.Rule.Type == RuleFull {
			dnsProxy = true
		}
	}
	var service Domain
	hasService := false
	for _, name := range sortedDomains(cfg, "service") {
		if strings.EqualFold(name, domain) {
			service, hasService = cfg.Domains[name], true
			break
		}
	}

	fmt.Fprintf(out, "Configured route for %s\nConfiguration: %s\n", domain, getPaths().ConfigFile)
	if dnsProxy {
		if cfg.Settings.ProxyIP == "" || cfg.Settings.ProxyIP == "0.0.0.0" {
			fmt.Fprintln(out, "DNS: intercepted; proxy_ip is not set and must be resolved during sync.")
		} else {
			fmt.Fprintf(out, "DNS: A -> %s; other types (including AAAA, HTTPS and SVCB) -> NOERROR/NODATA.\n", cfg.Settings.ProxyIP)
		}
	} else {
		fmt.Fprintln(out, "DNS: upstream resolution (no matching Smart DNS suffix rule).")
	}
	switch {
	case hasService:
		ip := service.IP
		if ip == "" {
			ip = "127.0.0.1"
		}
		scheme := "http"
		if service.Port == 8443 {
			scheme = "https"
		}
		fmt.Fprintf(out, "Angie SNI: exact service -> %s://%s:%d (TLS terminates at Angie).\n", scheme, ip, service.Port)
	case len(matches) > 0:
		fmt.Fprintf(out, "Angie SNI: TLS passthrough -> %s:443, if traffic reaches Flowgate.\n", domain)
	case len(unknown) > 0:
		fmt.Fprintln(out, "Angie SNI: unknown; some PCRE rules cannot be evaluated by this diagnostic.")
	default:
		fmt.Fprintln(out, "Angie SNI: no configured route.")
	}
	if hasService {
		fmt.Fprintf(out, "Service source: domains.%s\n", domain)
	}
	if len(matches) > 0 {
		fmt.Fprintln(out, "Matching proxy sources:")
		for _, match := range matches {
			effect := "SNI only; no DNS interception"
			if match.Rule.Type == RuleRootDomain || match.Rule.Type == RuleFull {
				effect = "DNS + SNI suffix"
			}
			fmt.Fprintf(out, "  %s -> %s:%q (%s)\n", match.Source, diagnosticRuleType(match.Rule.Type), match.Rule.Value, effect)
		}
	}
	if len(unknown) > 0 {
		fmt.Fprintf(out, "Unevaluated SNI regex rules (%d):\n", len(unknown))
		for i, match := range unknown {
			if i == 10 {
				fmt.Fprintf(out, "  ... %d more unevaluated rules\n", len(unknown)-i)
				break
			}
			fmt.Fprintf(out, "  %s -> regexp:%q (%s)\n", match.Source, match.Rule.Value, match.Reason)
		}
	}
	fmt.Fprintln(out, "Configuration inspection only: no DNS queries, runtime checks or client/VPN route tests were performed.")
	if dnsProxy && hasService {
		return fmt.Errorf("service %s is covered by a Smart DNS proxy rule; sync will reject this conflict", domain)
	}
	return nil
}

func diagnosticMatches(cfg *Config, domain string) ([]diagnosticMatch, []diagnosticMatch, error) {
	var candidates []diagnosticMatch
	for _, name := range sortedDomains(cfg, "proxy") {
		candidates = append(candidates, diagnosticMatch{
			Source: "explicit " + name,
			Rule:   ProxyRule{Type: RuleRootDomain, Value: name},
		})
	}
	if len(cfg.GeoSites) > 0 {
		db, err := loadGeoSiteDB(getPaths().DLC)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot inspect configured GeoSites from local dlc.dat: %w; run 'flowgate update'", err)
		}
		selectors := append([]string(nil), cfg.GeoSites...)
		sort.Strings(selectors)
		for _, selector := range selectors {
			rules, ok := db.Rules(selector)
			if !ok {
				return nil, nil, fmt.Errorf("configured geosite:%s is missing from dlc.dat; run 'flowgate update'", normalizeGeoSiteName(selector))
			}
			for _, rule := range rules {
				candidates = append(candidates, diagnosticMatch{
					Source: "geosite:" + normalizeGeoSiteName(selector),
					Rule:   rule,
				})
			}
		}
	}
	var matches, unknown []diagnosticMatch
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		key := fmt.Sprintf("%s\x00%d\x00%s", candidate.Source, candidate.Rule.Type, candidate.Rule.Value)
		if seen[key] {
			continue
		}
		seen[key] = true
		matched, err := diagnosticRuleMatches(candidate.Rule, domain)
		if err != nil {
			candidate.Reason = err.Error()
			unknown = append(unknown, candidate)
		} else if matched {
			matches = append(matches, candidate)
		}
	}
	sortDiagnosticMatches(matches)
	sortDiagnosticMatches(unknown)
	return matches, unknown, nil
}

func sortDiagnosticMatches(matches []diagnosticMatch) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Source != matches[j].Source {
			return matches[i].Source < matches[j].Source
		}
		if matches[i].Rule.Type != matches[j].Rule.Type {
			return matches[i].Rule.Type < matches[j].Rule.Type
		}
		return matches[i].Rule.Value < matches[j].Rule.Value
	})
}

func diagnosticRuleMatches(rule ProxyRule, domain string) (bool, error) {
	switch rule.Type {
	case RuleRootDomain, RuleFull:
		suffix := strings.ToLower(rule.Value)
		return domain == suffix || strings.HasSuffix(domain, "."+suffix), nil
	case RulePlain:
		return strings.Contains(domain, strings.ToLower(rule.Value)), nil
	case RuleRegex:
		re, err := regexp.Compile(rule.Value)
		if err != nil {
			return false, fmt.Errorf("unsupported diagnostic regex: %w", err)
		}
		return re.MatchString(domain), nil
	default:
		return false, fmt.Errorf("unknown rule type %d", rule.Type)
	}
}

func diagnosticRuleType(kind ProxyRuleType) string {
	switch kind {
	case RuleRootDomain:
		return "domain"
	case RuleFull:
		return "full"
	case RulePlain:
		return "keyword"
	case RuleRegex:
		return "regexp"
	default:
		return "unknown"
	}
}
