package main

import (
	"os"
	"path/filepath"
)

func rooted(path string) string {
	root := os.Getenv("FLOWGATE_ROOT")
	if root == "" || root == "/" {
		return path
	}
	return filepath.Join(root, path)
}

func getPaths() Paths {
	configDir := rooted("/etc/flowgate")
	dataDir := rooted("/var/lib/flowgate")
	return Paths{
		ConfigDir:     configDir,
		DataDir:       dataDir,
		BackupDir:     filepath.Join(dataDir, "backups"),
		ConfigFile:    filepath.Join(configDir, "flowgate.yaml"),
		AppliedConfig: filepath.Join(dataDir, "applied.yaml"),
		Blocky:        rooted("/etc/blocky/config.yml"),
		AngieMain:     rooted("/etc/angie/angie.conf"),
		AngieStream:   rooted("/etc/angie/stream.d/flowgate.conf"),
		AngieHTTP:     rooted("/etc/angie/http.d/flowgate.conf"),
		BlockyList:    rooted("/etc/blocky/flowgate.list"),
		BlockyCertSum: filepath.Join(dataDir, "blocky-cert.sha256"),
		DLC:           filepath.Join(dataDir, "dlc.dat"),
		DLCSum:        filepath.Join(dataDir, "dlc.dat.sha256sum"),
	}
}
