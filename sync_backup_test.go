package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func newestBackup(t *testing.T, name string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(getPaths().BackupDir, name+"_*.bak"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find %s backups: files=%v err=%v", name, files, err)
	}
	sort.Strings(files)
	return files[len(files)-1]
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestApplyConfigBacksUpPreviousConfiguration(t *testing.T) {
	old := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"old.example.com": {Type: "proxy"}},
	}
	setupTestRoot(t, old)
	if err := syncAll(); err != nil {
		t.Fatal(err)
	}
	p := getPaths()
	before := readTestFile(t, p.ConfigFile)
	streamBefore := readTestFile(t, p.AngieStream)
	blockyBefore := readTestFile(t, p.Blocky)
	if err := os.Chmod(p.ConfigFile, 0600); err != nil {
		t.Fatal(err)
	}

	candidate := &Config{
		Settings: old.Settings,
		Domains:  map[string]Domain{"new.example.com": {Type: "proxy"}},
	}
	if err := applyConfig(candidate); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"flowgate-config", before},
		{"angie-stream", streamBefore},
		{"blocky-config", blockyBefore},
	} {
		if data := readTestFile(t, newestBackup(t, item.name)); !bytes.Equal(data, item.data) {
			t.Fatalf("%s backup is not the pre-command state:\n%s", item.name, data)
		}
	}
	info, err := os.Stat(newestBackup(t, "flowgate-config"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
	got, err := loadConfig()
	if err != nil || got.Domains["new.example.com"].Type != "proxy" {
		t.Fatalf("candidate was not applied: cfg=%#v err=%v", got, err)
	}
}

func TestFailedApplyConfigBacksUpPreviousConfiguration(t *testing.T) {
	old := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"old.example.com": {Type: "proxy"}},
	}
	setupTestRoot(t, old)
	before := readTestFile(t, getPaths().ConfigFile)
	if err := writeAtomic(getPaths().DLC, []byte("invalid protobuf"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := &Config{
		Settings: old.Settings,
		Domains:  map[string]Domain{},
		GeoSites: []string{"category-bad"},
	}
	if err := applyConfig(candidate); err == nil {
		t.Fatal("expected failed sync")
	}
	if data := readTestFile(t, newestBackup(t, "flowgate-config")); !bytes.Equal(data, before) {
		t.Fatalf("failed command backed up its rejected candidate:\n%s", data)
	}
	if data := readTestFile(t, getPaths().ConfigFile); !bytes.Equal(data, before) {
		t.Fatalf("failed command changed its input configuration:\n%s", data)
	}
}

func TestFailedManualSyncRestoresPreparationFiles(t *testing.T) {
	good := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{"old.example.com": {Type: "proxy"}},
	}
	setupTestRoot(t, good)
	p := getPaths()
	if err := saveAppliedConfig(good); err != nil {
		t.Fatal(err)
	}
	originalMain := []byte("events {}\nstream { resolver 9.9.9.9; }\nhttp {}\n")
	examplePath := filepath.Join(filepath.Dir(p.AngieStream), "example.conf")
	originalExample := []byte("# existing example\n")
	if err := writeAtomic(p.AngieMain, originalMain, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(examplePath, originalExample, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(p.DLC, []byte("invalid protobuf"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := &Config{
		Settings: good.Settings,
		Domains:  map[string]Domain{},
		GeoSites: []string{"category-bad"},
	}
	if err := saveConfig(candidate); err != nil {
		t.Fatal(err)
	}
	input := readTestFile(t, p.ConfigFile)
	if err := syncAll(); err == nil {
		t.Fatal("expected failed sync")
	}
	if data := readTestFile(t, p.AngieMain); !bytes.Equal(data, originalMain) {
		t.Fatalf("failed sync left changes to Angie main:\n%s", data)
	}
	info, err := os.Stat(p.AngieMain)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("Angie main permissions were not restored: info=%v err=%v", info, err)
	}
	if data := readTestFile(t, examplePath); !bytes.Equal(data, originalExample) {
		t.Fatalf("failed sync removed the previous example file: %q", data)
	}
	for _, path := range []string{p.Blocky, p.AngieStream, p.AngieHTTP} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed sync left newly generated file %s: %v", path, err)
		}
	}
	if data := readTestFile(t, newestBackup(t, "flowgate-config")); !bytes.Equal(data, input) {
		t.Fatalf("manual sync did not preserve its on-disk input in the backup:\n%s", data)
	}
	got, err := loadConfig()
	if err != nil || got.Domains["old.example.com"].Type != "proxy" || len(got.GeoSites) != 0 {
		t.Fatalf("failed manual sync did not restore applied configuration: %#v, %v", got, err)
	}
}

func TestBackupDoesNotInventPreviousConfig(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	p := getPaths()
	before, err := snapshotFile(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Settings: Settings{ProxyIP: "203.0.113.10"},
		Domains:  map[string]Domain{},
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := backupConfigsWithConfigSnapshot(&before); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(p.BackupDir, "flowgate-config_*.bak"))
	if err != nil || len(files) != 0 {
		t.Fatalf("created backup for nonexistent prior configuration: files=%v err=%v", files, err)
	}
}

func TestApplyCandidateReportsRollbackFailure(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	p := getPaths()
	before := []byte("old\n")
	if err := writeAtomic(p.Blocky, before, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(p.AngieStream, before, 0644); err != nil {
		t.Fatal(err)
	}
	snaps, err := snapshotManagedFiles()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an external change after the pre-sync snapshot that prevents
	// both installation and restoration of one file.
	if err := os.Remove(p.AngieStream); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p.AngieStream, 0755); err != nil {
		t.Fatal(err)
	}
	candidate := &syncCandidate{
		blockyConfig: []byte("new\n"),
		angieStream:  []byte("new\n"),
		angieHTTP:    []byte("new\n"),
	}
	if _, err := applySyncCandidate(Stack{}, candidate, false, snaps); err == nil ||
		!strings.Contains(err.Error(), "rollback configuration") ||
		!strings.Contains(err.Error(), p.AngieStream) {
		t.Fatalf("missing rollback failure details: %v", err)
	}
	if data := readTestFile(t, p.Blocky); !bytes.Equal(data, before) {
		t.Fatalf("rollback did not restore the other file: %q", data)
	}
}
