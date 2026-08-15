package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type syncCandidate struct {
	blockyConfig []byte
	blockyList   []byte
	angieStream  []byte
	angieHTTP    []byte
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

func buildSyncCandidate(cfg *Config, rules []ProxyRule) (*syncCandidate, error) {
	listData, err := renderBlockyProxyList(rules)
	if err != nil {
		return nil, err
	}
	configData, err := renderBlocky(cfg, rules)
	if err != nil {
		return nil, err
	}
	return &syncCandidate{
		blockyConfig: configData,
		blockyList:   listData,
		angieStream:  []byte(renderAngieStream(cfg, rules)),
		angieHTTP:    []byte(renderAngieHTTP(cfg)),
	}, nil
}
func validateBlockyCandidate(cfg *Config, rules []ProxyRule, listData []byte) error {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return nil
	}
	if !commandExists("blocky") {
		return nil
	}
	dir, err := os.MkdirTemp("", "flowgate-blocky-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	listPath := filepath.Join(dir, "flowgate.list")
	configPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(listPath, listData, 0644); err != nil {
		return err
	}
	configData, err := renderBlockyWithListPath(cfg, rules, listPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return err
	}
	stdout, stderr, code, runErr := run("blocky", "--config", configPath, "validate")
	if code != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		return fmt.Errorf("blocky validate: %v: %s", runErr, msg)
	}
	return nil
}
func stageAngieDirectory(sourceDir, stageDir, managedPath string, override []byte) error {
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(sourceDir, "*.conf"))
	if err != nil {
		return err
	}
	managedPath = filepath.Clean(managedPath)
	for _, source := range matches {
		if filepath.Clean(source) == managedPath {
			continue
		}
		if err := os.Symlink(source, filepath.Join(stageDir, filepath.Base(source))); err != nil {
			return err
		}
	}
	dest := filepath.Join(stageDir, filepath.Base(managedPath))
	if override != nil {
		return writeAtomic(dest, override, 0644)
	}
	if fileExists(managedPath) {
		return os.Symlink(managedPath, dest)
	}
	return nil
}

func validateAngieConfigCandidate(mainData, streamData, httpData []byte) error {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return nil
	}
	if !commandExists("angie") {
		return nil
	}
	p := getPaths()
	if mainData == nil {
		var err error
		mainData, err = os.ReadFile(p.AngieMain)
		if err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "flowgate-angie-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	streamDir := filepath.Join(dir, "stream.d")
	httpDir := filepath.Join(dir, "http.d")
	if err := stageAngieDirectory(filepath.Dir(p.AngieStream), streamDir, p.AngieStream, streamData); err != nil {
		return err
	}
	if err := stageAngieDirectory(filepath.Dir(p.AngieHTTP), httpDir, p.AngieHTTP, httpData); err != nil {
		return err
	}
	text := string(mainData)
	text = strings.ReplaceAll(text, filepath.Join(filepath.Dir(p.AngieStream), "*.conf"), filepath.Join(streamDir, "*.conf"))
	text = strings.ReplaceAll(text, filepath.Join(filepath.Dir(p.AngieHTTP), "*.conf"), filepath.Join(httpDir, "*.conf"))
	mainPath := filepath.Join(dir, "angie.conf")
	if err := os.WriteFile(mainPath, []byte(text), 0644); err != nil {
		return err
	}
	_, stderr, code, testErr := run("angie", "-t", "-c", mainPath)
	if code != 0 {
		return fmt.Errorf("angie -t: %v: %s", testErr, strings.TrimSpace(stderr))
	}
	return nil
}

func validateAngieCandidate(c *syncCandidate) error {
	return validateAngieConfigCandidate(nil, c.angieStream, c.angieHTTP)
}

func reloadOrStartAngie() error {
	if isActive("angie") {
		return serviceControl("angie", "reload")
	}
	return serviceControl("angie", "start")
}
func snapshotManagedFiles() ([]fileSnapshot, error) {
	p := getPaths()
	paths := []string{p.Blocky, p.BlockyList, p.AngieStream, p.AngieHTTP}
	out := make([]fileSnapshot, 0, len(paths))
	for _, path := range paths {
		snap, err := snapshotFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func restoreManagedFiles(snaps []fileSnapshot) error {
	var first error
	for _, snap := range snaps {
		if err := restoreSnapshot(snap); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func restoreRuntime(stack Stack, angieWasActive, blockyWasActive bool) {
	if stack.Angie {
		if angieWasActive {
			_ = serviceControl("angie", "reload")
		} else if isActive("angie") {
			_ = serviceControl("angie", "stop")
		}
	}
	if stack.Blocky {
		if blockyWasActive {
			_ = serviceControl("blocky", "restart")
		} else if isActive("blocky") {
			_ = serviceControl("blocky", "stop")
		}
	}
}
func applySyncCandidate(stack Stack, c *syncCandidate) error {
	p := getPaths()
	snaps, err := snapshotManagedFiles()
	if err != nil {
		return err
	}
	angieWasActive := isActive("angie")
	blockyWasActive := isActive("blocky")
	rollback := func() {
		_ = restoreManagedFiles(snaps)
		restoreRuntime(stack, angieWasActive, blockyWasActive)
	}

	writes := []struct {
		path string
		data []byte
	}{
		{p.BlockyList, c.blockyList},
		{p.Blocky, c.blockyConfig},
		{p.AngieStream, c.angieStream},
		{p.AngieHTTP, c.angieHTTP},
	}
	for _, item := range writes {
		if err := writeAtomic(item.path, item.data, 0644); err != nil {
			rollback()
			return err
		}
	}

	if stack.Angie {
		if err := reloadOrStartAngie(); err != nil {
			rollback()
			return fmt.Errorf("apply angie: %w", err)
		}
	}
	if stack.Blocky {
		if err := serviceControl("blocky", "restart"); err != nil {
			rollback()
			return fmt.Errorf("apply blocky: %w", err)
		}
	}
	return nil
}
func syncFailure(err error) error {
	if restoreErr := restoreAppliedConfig(); restoreErr != nil {
		return fmt.Errorf("%w; restore applied config: %v", err, restoreErr)
	}
	return err
}

func syncAll() error {
	stack := detectStack()
	header("Syncing Configuration (Proxy: ANGIE, DNS: BLOCKY)")
	if !stack.Angie && !stack.Blocky {
		warn("Angie and Blocky are not installed or running. Nothing to sync.")
		return nil
	}
	if err := backupConfigs(); err != nil {
		return syncFailure(fmt.Errorf("backup: %w", err))
	}
	if err := ensureEnvironment(); err != nil {
		return syncFailure(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		return syncFailure(err)
	}
	if err := ensureDLCFor(cfg.GeoSites); err != nil {
		return syncFailure(err)
	}
	rules, err := resolveProxyRules(cfg)
	if err != nil {
		return syncFailure(err)
	}
	candidate, err := buildSyncCandidate(cfg, rules)
	if err != nil {
		return syncFailure(err)
	}
	if err := saveConfig(cfg); err != nil {
		return syncFailure(err)
	}
	if stack.Blocky {
		if err := validateBlockyCandidate(cfg, rules, candidate.blockyList); err != nil {
			return syncFailure(err)
		}
	}
	if stack.Angie {
		if err := validateAngieCandidate(candidate); err != nil {
			return syncFailure(err)
		}
	}
	if stack.Blocky {
		if err := fixAngieCertPermissions(); err != nil {
			return syncFailure(fmt.Errorf("prepare certificate access: %w", err))
		}
	}
	if err := applySyncCandidate(stack, candidate); err != nil {
		return syncFailure(err)
	}
	if stack.Angie {
		success("Angie configuration applied")
	}
	if stack.Blocky {
		success("Blocky configuration applied")
	}
	if err := saveAppliedConfig(cfg); err != nil {
		warn("Could not save applied configuration snapshot: %v", err)
	}
	if err := fixAngieCertPermissions(); err != nil {
		warn("Could not refresh certificate permissions: %v", err)
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

func fixAngieCertPermissions() error {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return nil
	}
	base := "/var/lib/angie/acme"
	if !dirExists(base) {
		return nil
	}
	if _, _, code, _ := run("id", "-u", "blocky"); code == 0 {
		groups, _, _, _ := run("id", "-nG", "blocky")
		if !containsField(groups, "angie") {
			switch {
			case commandExists("usermod"):
				if _, stderr, code, err := run("usermod", "-a", "-G", "angie", "blocky"); code != 0 {
					return fmt.Errorf("add blocky to angie group: %v: %s", err, strings.TrimSpace(stderr))
				}
			case commandExists("addgroup"):
				if _, stderr, code, err := run("addgroup", "blocky", "angie"); code != 0 {
					return fmt.Errorf("add blocky to angie group: %v: %s", err, strings.TrimSpace(stderr))
				}
			default:
				return fmt.Errorf("cannot add blocky to angie group")
			}
		}
	}
	if commandExists("chgrp") {
		if _, stderr, code, err := run("chgrp", "-R", "angie", base); code != 0 {
			return fmt.Errorf("chgrp Angie ACME directory: %v: %s", err, strings.TrimSpace(stderr))
		}
	}
	if err := os.Chmod(base, 0750); err != nil {
		return err
	}
	entries, err := filepath.Glob(filepath.Join(base, "acme_*"))
	if err != nil {
		return err
	}
	for _, dir := range entries {
		if err := os.Chmod(dir, 0750); err != nil {
			return err
		}
		for _, name := range []string{"certificate.pem", "private.key"} {
			path := filepath.Join(dir, name)
			if fileExists(path) {
				if err := os.Chmod(path, 0640); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func containsField(text, value string) bool {
	for _, field := range strings.Fields(text) {
		if field == value {
			return true
		}
	}
	return false
}
