package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func commandNeedsLock(command string) bool {
	switch command {
	case "init", "add", "service", "dns", "remove", "update", "sync":
		return true
	default:
		return false
	}
}

func acquireProcessLock() (*os.File, error) {
	path := filepath.Join(getPaths().DataDir, "flowgate.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return file, nil
}
