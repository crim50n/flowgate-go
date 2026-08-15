package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestProcessLockSerializes(t *testing.T) {
	t.Setenv("FLOWGATE_ROOT", t.TempDir())
	first, err := acquireProcessLock()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	acquired := make(chan *os.File, 1)
	errs := make(chan error, 1)
	go func() {
		lock, err := acquireProcessLock()
		if err != nil {
			errs <- err
			return
		}
		acquired <- lock
	}()
	select {
	case lock := <-acquired:
		lock.Close()
		t.Fatal("second lock acquired before first was released")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case lock := <-acquired:
		lock.Close()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not acquire after release")
	}
}

func TestWriteAtomicConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	values := [][]byte{[]byte("alpha\n"), []byte("beta\n"), []byte("gamma\n")}
	var wg sync.WaitGroup
	errs := make(chan error, len(values))
	for _, value := range values {
		value := value
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- writeAtomic(path, value, 0644)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	valid := false
	for _, value := range values {
		if string(got) == string(value) {
			valid = true
			break
		}
	}
	if !valid {
		t.Fatalf("partial concurrent write: %q", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config.yml.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}
