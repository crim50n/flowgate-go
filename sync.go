package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func syncAll() error {
	stack := detectStack()
	header("Syncing Configuration (Proxy: ANGIE, DNS: BLOCKY)")
	if !stack.Angie && !stack.Blocky {
		warn("Angie and Blocky are not installed or running. Nothing to sync.")
		return nil
	}

	if err := backupConfigs(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := ensureEnvironment(); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if stack.Blocky {
		if err := syncBlocky(cfg); err != nil {
			return err
		}
	}
	if stack.Angie {
		if err := syncAngie(cfg); err != nil {
			return err
		}
	}

	if os.Getenv("FLOWGATE_ROOT") == "" && detectInit() != "none" {
		if stack.Angie && !isActive("angie") {
			warn("Angie is not running after sync")
		}
		if stack.Blocky && !isActive("blocky") {
			warn("Blocky is not running after sync")
		}
	}
	header("Sync Completed Successfully")
	return nil
}

func syncBlocky(cfg *Config) error {
	info("Syncing Blocky...")
	data, err := renderBlocky(cfg)
	if err != nil {
		return err
	}
	p := getPaths()
	if err = writeAtomic(p.Blocky, data, 0644); err != nil {
		return err
	}
	if err = serviceControl("blocky", "restart"); err != nil {
		return fmt.Errorf("restart blocky: %w", err)
	}
	success("Blocky restarted")
	return nil
}

type fileSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotFile(path string) (fileSnapshot, error) {
	s := fileSnapshot{path: path, mode: 0644}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	s.data, s.mode, s.exists = data, info.Mode().Perm(), true
	return s, nil
}

func restoreSnapshot(s fileSnapshot) error {
	if !s.exists {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAtomic(s.path, s.data, s.mode)
}

func syncAngie(cfg *Config) error {
	info("Syncing Angie...")
	p := getPaths()
	streamSnap, err := snapshotFile(p.AngieStream)
	if err != nil {
		return err
	}
	httpSnap, err := snapshotFile(p.AngieHTTP)
	if err != nil {
		return err
	}
	rollback := func() {
		_ = restoreSnapshot(streamSnap)
		_ = restoreSnapshot(httpSnap)
	}

	if err := writeAtomic(p.AngieStream, []byte(renderAngieStream(cfg)), 0644); err != nil {
		rollback()
		return err
	}
	if err := writeAtomic(p.AngieHTTP, []byte(renderAngieHTTP(cfg)), 0644); err != nil {
		rollback()
		return err
	}

	if root := os.Getenv("FLOWGATE_ROOT"); root == "" || root == "/" {
		if commandExists("angie") {
			_, stderr, code, testErr := run("angie", "-t")
			if code != 0 {
				rollback()
				return fmt.Errorf("angie -t: %v: %s", testErr, strings.TrimSpace(stderr))
			}
		}
	}

	if isActive("angie") {
		if err := serviceControl("angie", "reload"); err != nil {
			rollback()
			_ = serviceControl("angie", "reload")
			return fmt.Errorf("reload angie: %w", err)
		}
		success("Angie reloaded")
	} else {
		if err := serviceControl("angie", "start"); err != nil {
			rollback()
			return fmt.Errorf("start angie: %w", err)
		}
		success("Angie started")
	}
	fixAngieCertPermissions()
	return nil
}

func fixAngieCertPermissions() {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return
	}
	base := "/var/lib/angie/acme"
	entries, err := filepath.Glob(filepath.Join(base, "acme_*"))
	if err != nil {
		return
	}

	if _, _, code, _ := run("id", "-u", "blocky"); code == 0 {
		if commandExists("usermod") {
			_, _, _, _ = run("usermod", "-a", "-G", "angie", "blocky")
		} else if commandExists("addgroup") {
			_, _, _, _ = run("addgroup", "blocky", "angie")
		}
	}
	for _, dir := range entries {
		_ = os.Chmod(dir, 0750)
		if commandExists("chgrp") {
			_, _, _, _ = run("chgrp", "-R", "angie", dir)
		}
		for _, name := range []string{"certificate.pem", "private.key"} {
			path := filepath.Join(dir, name)
			if fileExists(path) {
				_ = os.Chmod(path, 0640)
			}
		}
	}
}
