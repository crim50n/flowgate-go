package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ensureBlockyConfig() error {
	p := getPaths()
	if fileExists(p.Blocky) {
		return nil
	}
	const initial = `upstreams:
  groups:
    default:
      - https://8.8.8.8/dns-query
      - https://1.1.1.1/dns-query
ports:
  dns: 53
  http: 4000
log:
  level: info
`
	if err := writeAtomic(p.Blocky, []byte(initial), 0644); err != nil {
		return err
	}
	success("Created initial Blocky config: %s", p.Blocky)
	return nil
}
func ensureAngieConfig() error {
	p := getPaths()
	for _, d := range []string{filepath.Dir(p.AngieStream), filepath.Dir(p.AngieHTTP), rooted("/run/angie"), rooted("/var/log/angie")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(filepath.Dir(p.AngieStream), "example.conf"))

	if !fileExists(p.AngieMain) {
		base := `user angie;
worker_processes auto;
pid /run/angie.pid;
error_log /var/log/angie/error.log info;
events { worker_connections 1024; }
stream { include /etc/angie/stream.d/*.conf; }
http {
    include /etc/angie/mime.types;
    default_type application/octet-stream;
    resolver 8.8.8.8 1.1.1.1 ipv6=off;
    variables_hash_bucket_size 512;
    sendfile on;
    keepalive_timeout 65;
    include /etc/angie/http.d/*.conf;
}
`
		if err := writeAngieMain(p.AngieMain, []byte(base)); err != nil {
			return err
		}
		success("Created initial Angie config: %s", p.AngieMain)
		return nil
	}

	b, err := os.ReadFile(p.AngieMain)
	if err != nil {
		return err
	}
	text := string(b)
	text = enableStreamBlock(text)
	text = ensureHTTPSettings(text)
	return writeAngieMain(p.AngieMain, []byte(text))
}

func writeAngieMain(path string, data []byte) error {
	snap, err := snapshotFile(path)
	if err != nil {
		return err
	}
	if err := writeAtomic(path, data, 0644); err != nil {
		return err
	}
	if err := validateAngieOnDisk(); err != nil {
		_ = restoreSnapshot(snap)
		return err
	}
	return nil
}

func validateAngieOnDisk() error {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return nil
	}
	if !commandExists("angie") {
		return nil
	}
	_, stderr, code, err := run("angie", "-t")
	if code != 0 {
		return fmt.Errorf("angie -t: %v: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}
func contextRange(text, name string) (int, int, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*\{`)
	loc := re.FindStringIndex(text)
	if loc == nil {
		return 0, 0, false
	}
	open := loc[0] + strings.Index(text[loc[0]:loc[1]], "{")
	depth := 0
	var quote byte
	comment := false
	escaped := false
	for i := open; i < len(text); i++ {
		c := text[i]
		if comment {
			if c == '\n' {
				comment = false
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '#':
			comment = true
		case '\'', '"':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return open, i + 1, true
			}
		}
	}
	return 0, 0, false
}

func contextHasTopLevelDirective(text, name, directive string) bool {
	open, end, ok := contextRange(text, name)
	if !ok {
		return false
	}
	body := text[open+1 : end-1]
	depth := 0
	var quote byte
	comment := false
	statementStart := true
	for i := 0; i < len(body); {
		c := body[i]
		if comment {
			i++
			if c == '\n' {
				comment = false
				statementStart = true
			}
			continue
		}
		if quote != 0 {
			i++
			if c == '\\' && i < len(body) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '#' {
			comment = true
			i++
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			i++
			continue
		}
		if c == '{' {
			depth++
			statementStart = true
			i++
			continue
		}
		if c == '}' {
			if depth > 0 {
				depth--
			}
			statementStart = true
			i++
			continue
		}
		if c == ';' {
			statementStart = true
			i++
			continue
		}
		if c == '\n' {
			statementStart = true
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		if depth == 0 && statementStart {
			j := i
			for j < len(body) && body[j] != ' ' && body[j] != '\t' && body[j] != '\r' && body[j] != '\n' && body[j] != ';' && body[j] != '{' {
				j++
			}
			if body[i:j] == directive {
				return true
			}
			statementStart = false
			i = j
			continue
		}
		statementStart = false
		i++
	}
	return false
}

func addToContext(text, name, directive string) (string, bool) {
	open, _, ok := contextRange(text, name)
	if !ok {
		return text, false
	}
	if directiveName := strings.Fields(directive); len(directiveName) > 0 && contextHasTopLevelDirective(text, name, directiveName[0]) {
		if directiveName[0] != "include" || strings.Contains(text[open:], directive) {
			return text, true
		}
	}
	insert := "\n    " + directive
	return text[:open+1] + insert + text[open+1:], true
}

func enableStreamBlock(text string) string {
	const include = "include /etc/angie/stream.d/*.conf;"
	const resolver = "resolver 1.1.1.1 8.8.8.8 ipv6=off;"
	if _, _, ok := contextRange(text, "stream"); !ok {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + "\nstream {\n    " + resolver + "\n    " + include + "\n}\n"
	}
	text, _ = addToContext(text, "stream", include)
	if !contextHasTopLevelDirective(text, "stream", "resolver") {
		text, _ = addToContext(text, "stream", resolver)
	}
	return text
}

func ensureHTTPSettings(text string) string {
	const include = "include /etc/angie/http.d/*.conf;"
	if _, _, ok := contextRange(text, "http"); !ok {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + "\nhttp {\n    resolver 8.8.8.8 1.1.1.1 ipv6=off;\n    variables_hash_bucket_size 512;\n    " + include + "\n}\n"
	}
	text, _ = addToContext(text, "http", include)
	if !contextHasTopLevelDirective(text, "http", "resolver") {
		text, _ = addToContext(text, "http", "resolver 8.8.8.8 1.1.1.1 ipv6=off;")
	}
	if !contextHasTopLevelDirective(text, "http", "variables_hash_bucket_size") {
		text, _ = addToContext(text, "http", "variables_hash_bucket_size 512;")
	}
	return text
}

func ensureSnakeoilCert() error {
	certPath := rooted("/etc/ssl/certs/ssl-cert-snakeoil.pem")
	keyPath := rooted("/etc/ssl/private/ssl-cert-snakeoil.key")
	if fileExists(certPath) && fileExists(keyPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost", Organization: []string{"Flowgate"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := writeAtomic(certPath, certPEM, 0644); err != nil {
		return err
	}
	if err := writeAtomic(keyPath, keyPEM, 0600); err != nil {
		return err
	}
	success("Generated snakeoil SSL certificate")
	return nil
}

func ensureEnvironment() error {
	p := getPaths()
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{
		{p.ConfigDir, 0755}, {p.DataDir, 0750}, {p.BackupDir, 0750},
	} {
		if err := os.MkdirAll(item.path, item.mode); err != nil {
			return err
		}
		_ = os.Chmod(item.path, item.mode)
	}
	if err := ensureBlockyConfig(); err != nil {
		return fmt.Errorf("blocky: %w", err)
	}
	if err := ensureAngieConfig(); err != nil {
		return fmt.Errorf("angie: %w", err)
	}
	return ensureSnakeoilCert()
}
