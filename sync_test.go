package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowgate.conf")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := restoreSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old\n" {
		t.Fatalf("restored content = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("restored mode = %o", info.Mode().Perm())
	}
}

func TestSnapshotRestoreRemovesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.conf")
	snap, err := snapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := restoreSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("new file still exists: %v", err)
	}
}

func TestRestoreManagedFilesRestoresAllSnapshots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FLOWGATE_ROOT", root)
	p := getPaths()
	paths := []string{p.Blocky, p.AngieStream, p.AngieHTTP}
	for _, path := range paths {
		if err := writeAtomic(path, []byte("old\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := snapshotManagedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if err := writeAtomic(path, []byte("new\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreManagedFiles(snaps); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "old\n" {
			t.Fatalf("%s not restored: %q", path, data)
		}
	}
}
