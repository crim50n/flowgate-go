package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	dlcURL    = "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat"
	dlcSumURL = "https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat.sha256sum"
)

func configuredGeoSites() ([]string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.GeoSites, nil
}

func validateRequiredGeoSites(db *GeoSiteDB, selectors []string) error {
	for _, selector := range selectors {
		if _, ok := db.Rules(selector); !ok {
			return fmt.Errorf("configured geosite %q is missing from dlc.dat", selector)
		}
	}
	return nil
}

func ensureDLCFor(required []string) error {
	if len(required) == 0 {
		return nil
	}
	p := getPaths()
	if fileExists(p.DLC) {
		db, err := loadGeoSiteDB(p.DLC)
		if err != nil {
			return fmt.Errorf("invalid dlc.dat: %w; run 'flowgate update'", err)
		}
		return validateRequiredGeoSites(db, required)
	}
	_, _, err := updateDLCFor(required)
	return err
}

func updateDLC() (bool, string, error) {
	required, err := configuredGeoSites()
	if err != nil {
		return false, "", err
	}
	return updateDLCFor(required)
}

func updateDLCFor(required []string) (bool, string, error) {
	client := &http.Client{Timeout: 45 * time.Second}
	return updateDLCFrom(client, dlcURL, dlcSumURL, required)
}

func updateDLCFrom(client *http.Client, dataURL, sumURL string, required []string) (bool, string, error) {
	sumData, err := downloadLimited(client, sumURL, 1<<20)
	if err != nil {
		return false, "", fmt.Errorf("download dlc checksum: %w", err)
	}
	expected, err := parseSHA256(sumData)
	if err != nil {
		return false, "", fmt.Errorf("parse dlc checksum: %w", err)
	}
	data, err := downloadLimited(client, dataURL, 64<<20)
	if err != nil {
		return false, "", fmt.Errorf("download dlc.dat: %w", err)
	}
	actual := sha256.Sum256(data)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != expected {
		return false, "", fmt.Errorf("dlc.dat checksum mismatch: expected %s, got %s", expected, actualHex)
	}
	db, err := parseGeoSiteDB(data)
	if err != nil {
		return false, "", fmt.Errorf("validate dlc.dat: %w", err)
	}
	if err := validateRequiredGeoSites(db, required); err != nil {
		return false, "", err
	}

	p := getPaths()
	if err := os.MkdirAll(p.DataDir, 0750); err != nil {
		return false, "", err
	}
	dataSnap, err := snapshotFile(p.DLC)
	if err != nil {
		return false, "", err
	}
	sumSnap, err := snapshotFile(p.DLCSum)
	if err != nil {
		return false, "", err
	}
	rollback := func() {
		_ = restoreSnapshot(dataSnap)
		_ = restoreSnapshot(sumSnap)
	}

	changed := true
	if current, err := os.ReadFile(p.DLC); err == nil {
		h := sha256.Sum256(current)
		changed = hex.EncodeToString(h[:]) != actualHex
	}
	if changed {
		if err := writeAtomic(p.DLC, data, 0644); err != nil {
			rollback()
			return false, "", err
		}
	}
	normalizedSum := []byte(actualHex + "  dlc.dat\n")
	if err := writeAtomic(p.DLCSum, normalizedSum, 0644); err != nil {
		rollback()
		return false, "", err
	}
	return changed, actualHex, nil
}

func downloadLimited(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	reader := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download exceeds %d bytes", limit)
	}
	return data, nil
}

func parseSHA256(data []byte) (string, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("invalid SHA-256 file")
	}
	hash := strings.ToLower(fields[0])
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("invalid SHA-256: %w", err)
	}
	return hash, nil
}
