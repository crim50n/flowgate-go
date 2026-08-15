package main

import (
	"os"
	"testing"
)

func testProtoVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func testProtoBytesField(field int, value []byte) []byte {
	out := testProtoVarint(uint64(field<<3 | 2))
	out = append(out, testProtoVarint(uint64(len(value)))...)
	return append(out, value...)
}

func testProtoVarintField(field int, value uint64) []byte {
	out := testProtoVarint(uint64(field << 3))
	return append(out, testProtoVarint(value)...)
}

func buildTestDLC(name string, rules []ProxyRule) []byte {
	var site []byte
	site = append(site, testProtoBytesField(1, []byte(name))...)
	for _, rule := range rules {
		var domain []byte
		domain = append(domain, testProtoVarintField(1, uint64(rule.Type))...)
		domain = append(domain, testProtoBytesField(2, []byte(rule.Value))...)
		site = append(site, testProtoBytesField(2, domain)...)
	}
	return testProtoBytesField(1, site)
}

func TestParseGeoSiteDB(t *testing.T) {
	want := []ProxyRule{
		{Type: RuleRootDomain, Value: "example.com"},
		{Type: RuleFull, Value: "full.example.com"},
		{Type: RulePlain, Value: "keyword"},
		{Type: RuleRegex, Value: `^node-[0-9]+\.example$`},
	}
	db, err := parseGeoSiteDB(buildTestDLC("CATEGORY-TEST", want))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := db.Rules("category-test")
	if !ok || len(got) != len(want) {
		t.Fatalf("rules = %#v, ok=%v", got, ok)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestResolveProxyRulesWithGeoSite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	data := buildTestDLC("CATEGORY-TEST", []ProxyRule{{Type: RuleFull, Value: "full.example.com"}})
	if err := os.WriteFile(p.DLC, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Domains:  map[string]Domain{"manual.example.com": {Type: "proxy"}},
		GeoSites: []string{"category-test"},
	}
	rules, err := resolveProxyRules(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
}
