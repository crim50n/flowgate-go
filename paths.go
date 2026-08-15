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
		ConfigDir:   configDir,
		DataDir:     dataDir,
		BackupDir:   filepath.Join(dataDir, "backups"),
		ConfigFile:  filepath.Join(configDir, "flowgate.yaml"),
		Blocky:      rooted("/etc/blocky/config.yml"),
		AngieMain:   rooted("/etc/angie/angie.conf"),
		AngieStream: rooted("/etc/angie/stream.d/ai-proxy.conf"),
		AngieHTTP:   rooted("/etc/angie/http.d/local-services.conf"),
	}
}
