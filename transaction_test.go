package main

import (
	"os"
	"testing"
)

func TestInitWithoutGeoSitesDoesNotRequireDLC(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	t.Setenv("PROXY_IP", "203.0.113.10")
	if err := cmdInit(); err != nil {
		t.Fatal(err)
	}
	if fileExists(getPaths().DLC) {
		t.Fatal("init downloaded dlc.dat without configured GeoSites")
	}
}

func TestApplyConfigRestoresConfigOnFailedSync(t *testing.T) {
	old := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"existing.example.com": {Type: "proxy"}},
	}
	setupTestRoot(t, old)
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DLC, []byte("not-a-protobuf"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{},
		GeoSites: []string{"category-bad"},
	}
	if err := applyConfig(candidate); err == nil {
		t.Fatal("expected failed sync")
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GeoSites) != 0 {
		t.Fatalf("failed config persisted GeoSite: %#v", got.GeoSites)
	}
	if _, ok := got.Domains["existing.example.com"]; !ok {
		t.Fatal("previous config was not restored")
	}
}

func TestFailedManualSyncRestoresAppliedConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	good := &Config{Settings: Settings{ProxyIP: "203.0.113.10"}, Domains: map[string]Domain{
		"existing.example.com": {Type: "proxy"},
	}}
	if err := saveConfig(good); err != nil {
		t.Fatal(err)
	}
	if err := saveAppliedConfig(good); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.DLC, []byte("not-a-protobuf"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{},
		GeoSites: []string{"category-bad"},
	}
	if err := saveConfig(candidate); err != nil {
		t.Fatal(err)
	}
	if err := syncAll(); err == nil {
		t.Fatal("expected failed sync")
	}
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GeoSites) != 0 || got.Domains["existing.example.com"].Type != "proxy" {
		t.Fatalf("applied config was not restored: %#v", got)
	}
}
