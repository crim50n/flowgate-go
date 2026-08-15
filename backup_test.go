package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupConfigsKeepsAngieContextsSeparate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	if err := os.MkdirAll(filepath.Dir(p.AngieStream), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.AngieHTTP), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.AngieStream, []byte("stream\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.AngieHTTP, []byte("http\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := backupConfigs(); err != nil {
		t.Fatal(err)
	}
	stream, _ := filepath.Glob(filepath.Join(p.BackupDir, "angie-stream_*.bak"))
	http, _ := filepath.Glob(filepath.Join(p.BackupDir, "angie-http_*.bak"))
	if len(stream) != 1 || len(http) != 1 {
		t.Fatalf("stream backups=%d http backups=%d", len(stream), len(http))
	}
	streamData, err := os.ReadFile(stream[0])
	if err != nil {
		t.Fatal(err)
	}
	httpData, err := os.ReadFile(http[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(streamData) != "stream\n" || string(httpData) != "http\n" {
		t.Fatalf("backup contents collided: stream=%q http=%q", streamData, httpData)
	}
}

func TestBackupRetentionKeepsThreeGenerations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	if err := os.MkdirAll(filepath.Dir(p.AngieStream), 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(p.AngieStream, []byte("version\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := backupConfigs(); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(p.BackupDir, "angie-stream_*.bak"))
	if len(matches) != 3 {
		t.Fatalf("retained %d backups, want 3", len(matches))
	}
}
