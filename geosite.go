package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type ProxyRuleType uint8

const (
	RulePlain ProxyRuleType = iota
	RuleRegex
	RuleRootDomain
	RuleFull
)

type ProxyRule struct {
	Type  ProxyRuleType
	Value string
}

type GeoSiteDB struct {
	sites map[string][]ProxyRule
}

func normalizeGeoSiteName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) >= 8 && strings.EqualFold(name[:8], "geosite:") {
		name = name[8:]
	}
	return strings.ToLower(name)
}

func validGeoSiteName(name string) bool {
	if name == "" || len(name) > 253 || strings.TrimSpace(name) != name {
		return false
	}
	return !strings.ContainsAny(name, "\\/\r\n\t ")
}

func loadGeoSiteDB(path string) (*GeoSiteDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseGeoSiteDB(data)
}

func parseGeoSiteDB(data []byte) (*GeoSiteDB, error) {
	db := &GeoSiteDB{sites: make(map[string][]ProxyRule)}
	pos := 0
	for pos < len(data) {
		key, err := protoVarint(data, &pos)
		if err != nil {
			return nil, err
		}
		field, wire := int(key>>3), int(key&7)
		if field == 1 && wire == 2 {
			msg, err := protoBytes(data, &pos)
			if err != nil {
				return nil, err
			}
			name, rules, err := parseGeoSite(msg)
			if err != nil {
				return nil, err
			}
			if name != "" {
				key := normalizeGeoSiteName(name)
				db.sites[key] = append(db.sites[key], rules...)
			}
			continue
		}
		if err := skipProto(data, &pos, wire); err != nil {
			return nil, err
		}
	}
	if len(db.sites) == 0 {
		return nil, fmt.Errorf("dlc.dat contains no geosite entries")
	}
	return db, nil
}

func parseGeoSite(data []byte) (string, []ProxyRule, error) {
	var name string
	var rules []ProxyRule
	pos := 0
	for pos < len(data) {
		key, err := protoVarint(data, &pos)
		if err != nil {
			return "", nil, err
		}
		field, wire := int(key>>3), int(key&7)
		switch {
		case field == 1 && wire == 2:
			b, err := protoBytes(data, &pos)
			if err != nil {
				return "", nil, err
			}
			name = string(b)
		case field == 2 && wire == 2:
			b, err := protoBytes(data, &pos)
			if err != nil {
				return "", nil, err
			}
			rule, err := parseGeoDomain(b)
			if err != nil {
				return "", nil, err
			}
			rules = append(rules, rule)
		default:
			if err := skipProto(data, &pos, wire); err != nil {
				return "", nil, err
			}
		}
	}
	return name, rules, nil
}

func parseGeoDomain(data []byte) (ProxyRule, error) {
	rule := ProxyRule{Type: RulePlain}
	pos := 0
	for pos < len(data) {
		key, err := protoVarint(data, &pos)
		if err != nil {
			return rule, err
		}
		field, wire := int(key>>3), int(key&7)
		switch {
		case field == 1 && wire == 0:
			v, err := protoVarint(data, &pos)
			if err != nil {
				return rule, err
			}
			if v > uint64(RuleFull) {
				return rule, fmt.Errorf("unsupported geosite domain type: %d", v)
			}
			rule.Type = ProxyRuleType(v)
		case field == 2 && wire == 2:
			b, err := protoBytes(data, &pos)
			if err != nil {
				return rule, err
			}
			rule.Value = string(b)
		default:
			if err := skipProto(data, &pos, wire); err != nil {
				return rule, err
			}
		}
	}
	if rule.Value == "" || strings.ContainsAny(rule.Value, "\r\n\x00") {
		return rule, fmt.Errorf("invalid empty or unsafe geosite rule")
	}
	return rule, nil
}

func protoVarint(data []byte, pos *int) (uint64, error) {
	var out uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *pos >= len(data) {
			return 0, fmt.Errorf("truncated protobuf varint")
		}
		b := data[*pos]
		*pos++
		out |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return out, nil
		}
	}
	return 0, fmt.Errorf("protobuf varint overflow")
}

func protoBytes(data []byte, pos *int) ([]byte, error) {
	n, err := protoVarint(data, pos)
	if err != nil {
		return nil, err
	}
	if n > uint64(len(data)-*pos) {
		return nil, fmt.Errorf("truncated protobuf field")
	}
	end := *pos + int(n)
	out := data[*pos:end]
	*pos = end
	return out, nil
}

func skipProto(data []byte, pos *int, wire int) error {
	switch wire {
	case 0:
		_, err := protoVarint(data, pos)
		return err
	case 1:
		if *pos+8 > len(data) {
			return fmt.Errorf("truncated protobuf fixed64")
		}
		*pos += 8
		return nil
	case 2:
		_, err := protoBytes(data, pos)
		return err
	case 5:
		if *pos+4 > len(data) {
			return fmt.Errorf("truncated protobuf fixed32")
		}
		*pos += 4
		return nil
	default:
		return fmt.Errorf("unsupported protobuf wire type: %d", wire)
	}
}

func (db *GeoSiteDB) Rules(name string) ([]ProxyRule, bool) {
	rules, ok := db.sites[normalizeGeoSiteName(name)]
	if !ok {
		return nil, false
	}
	out := make([]ProxyRule, len(rules))
	copy(out, rules)
	return out, true
}

func resolveProxyRules(cfg *Config) ([]ProxyRule, error) {
	rules := make([]ProxyRule, 0, len(cfg.Domains))
	for domain, entry := range cfg.Domains {
		if entry.Type == "proxy" {
			rules = append(rules, ProxyRule{Type: RuleRootDomain, Value: strings.ToLower(domain)})
		}
	}
	if len(cfg.GeoSites) > 0 {
		p := getPaths()
		db, err := loadGeoSiteDB(p.DLC)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("dlc.dat is missing; run 'flowgate update'")
			}
			return nil, fmt.Errorf("load dlc.dat: %w", err)
		}
		for _, selector := range cfg.GeoSites {
			geoRules, ok := db.Rules(selector)
			if !ok {
				return nil, fmt.Errorf("geosite %q not found in dlc.dat; run 'flowgate update'", selector)
			}
			rules = append(rules, geoRules...)
		}
	}

	seen := make(map[string]struct{}, len(rules))
	out := rules[:0]
	for _, rule := range rules {
		key := fmt.Sprintf("%d\x00%s", rule.Type, rule.Value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}
