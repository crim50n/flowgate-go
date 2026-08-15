package main

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestUpdateDLCVerifiesAndStores(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	data := buildTestDLC("CATEGORY-TEST", []ProxyRule{{Type: RuleRootDomain, Value: "example.com"}})
	hash := sha256.Sum256(data)
	server := httptest.NewServer(httpHandler(map[string][]byte{
		"/dlc.dat":           data,
		"/dlc.dat.sha256sum": []byte(fmt.Sprintf("%x  dlc.dat\n", hash)),
	}))
	defer server.Close()

	changed, gotHash, err := updateDLCFrom(server.Client(), server.URL+"/dlc.dat", server.URL+"/dlc.dat.sha256sum", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || gotHash != fmt.Sprintf("%x", hash) {
		t.Fatalf("changed=%v hash=%s", changed, gotHash)
	}
	stored, err := os.ReadFile(getPaths().DLC)
	if err != nil || string(stored) != string(data) {
		t.Fatalf("stored dlc mismatch: %v", err)
	}
	changed, _, err = updateDLCFrom(server.Client(), server.URL+"/dlc.dat", server.URL+"/dlc.dat.sha256sum", nil)
	if err != nil || changed {
		t.Fatalf("second update changed=%v err=%v", changed, err)
	}
}

func TestUpdateDLCRejectsBadChecksum(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	data := buildTestDLC("CATEGORY-TEST", []ProxyRule{{Type: RuleFull, Value: "example.com"}})
	server := httptest.NewServer(httpHandler(map[string][]byte{
		"/dlc.dat":           data,
		"/dlc.dat.sha256sum": []byte(strings.Repeat("0", 64) + "  dlc.dat\n"),
	}))
	defer server.Close()
	if _, _, err := updateDLCFrom(server.Client(), server.URL+"/dlc.dat", server.URL+"/dlc.dat.sha256sum", nil); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if fileExists(getPaths().DLC) {
		t.Fatal("invalid dlc.dat must not be installed")
	}
}

func TestUpdateDLCRejectsDatabaseMissingConfiguredGeoSite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		t.Fatal(err)
	}
	oldData := buildTestDLC("CATEGORY-REQUIRED", []ProxyRule{{Type: RuleRootDomain, Value: "old.example"}})
	if err := os.WriteFile(p.DLC, oldData, 0644); err != nil {
		t.Fatal(err)
	}
	newData := buildTestDLC("CATEGORY-OTHER", []ProxyRule{{Type: RuleRootDomain, Value: "new.example"}})
	hash := sha256.Sum256(newData)
	server := httptest.NewServer(httpHandler(map[string][]byte{
		"/dlc.dat":           newData,
		"/dlc.dat.sha256sum": []byte(fmt.Sprintf("%x  dlc.dat\n", hash)),
	}))
	defer server.Close()

	if _, _, err := updateDLCFrom(server.Client(), server.URL+"/dlc.dat", server.URL+"/dlc.dat.sha256sum", []string{"category-required"}); err == nil {
		t.Fatal("expected missing configured geosite error")
	}
	got, err := os.ReadFile(p.DLC)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(oldData) {
		t.Fatal("working dlc.dat was replaced")
	}
}
