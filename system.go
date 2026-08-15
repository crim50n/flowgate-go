package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func fileExists(p string) bool { st, e := os.Stat(p); return e == nil && !st.IsDir() }
func nonEmptyFile(p string) bool {
	st, e := os.Stat(p)
	return e == nil && !st.IsDir() && st.Size() > 0
}
func dirExists(p string) bool        { st, e := os.Stat(p); return e == nil && st.IsDir() }
func commandExists(name string) bool { _, e := exec.LookPath(name); return e == nil }

func detectInit() string {
	if dirExists(rooted("/run/systemd/system")) {
		return "systemd"
	}
	if dirExists(rooted("/run/openrc")) {
		return "openrc"
	}
	if dirExists(rooted("/etc/init.d")) && commandExists("service") {
		return "sysvinit"
	}
	if commandExists("sv") {
		return "runit"
	}
	if commandExists("s6-svc") {
		return "s6"
	}
	return "none"
}
func run(name string, args ...string) (string, string, int, error) {
	cmd := exec.Command(name, args...)
	var out, er bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &er
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return out.String(), er.String(), code, err
}

func serviceControl(name, action string) error {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return nil
	}
	init := detectInit()
	var cmd []string
	switch init {
	case "systemd":
		cmd = []string{"systemctl", action, name}
	case "openrc":
		cmd = []string{"rc-service", name, action}
	case "sysvinit":
		cmd = []string{"service", name, action}
	case "runit":
		m := map[string]string{"start": "up", "stop": "down", "restart": "restart", "reload": "hup", "status": "status"}
		cmd = []string{"sv", m[action], name}
	case "s6":
		m := map[string]string{"start": "-u", "stop": "-d", "restart": "-r", "reload": "-h"}
		if action == "status" {
			cmd = []string{"s6-svstat", "/run/service/" + name}
		} else {
			cmd = []string{"s6-svc", m[action], "/run/service/" + name}
		}
	default:
		return signalProcess(name, action)
	}
	if os.Geteuid() != 0 {
		cmd = append([]string{"sudo"}, cmd...)
	}
	info("Running: %s", strings.Join(cmd, " "))
	_, stderr, code, err := run(cmd[0], cmd[1:]...)
	if code != 0 {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func signalProcess(name, action string) error {
	if action == "start" {
		return nil
	}
	if action != "reload" && action != "restart" && action != "stop" {
		return nil
	}
	out, _, code, _ := run("pgrep", "-f", name)
	if code != 0 || strings.TrimSpace(out) == "" {
		if action == "reload" {
			return fmt.Errorf("process %s not found", name)
		}
		return nil
	}
	pid, err := strconv.Atoi(strings.Fields(out)[0])
	if err != nil {
		return err
	}
	sig := syscall.SIGHUP
	if action == "restart" || action == "stop" {
		sig = syscall.SIGTERM
	}
	return syscall.Kill(pid, sig)
}

func isActive(name string) bool {
	var out string
	var code int
	switch detectInit() {
	case "systemd":
		_, _, code, _ = run("systemctl", "is-active", name)
		return code == 0
	case "openrc":
		out, _, code, _ = run("rc-service", name, "status")
		return code == 0 && strings.Contains(strings.ToLower(out), "started")
	case "sysvinit":
		_, _, code, _ = run("service", name, "status")
		return code == 0
	case "runit":
		out, _, _, _ = run("sv", "status", name)
		return strings.Contains(out, "run:")
	case "s6":
		out, _, _, _ = run("s6-svstat", "/run/service/"+name)
		return strings.Contains(strings.ToLower(out), "up")
	}
	_, _, code, _ = run("pgrep", "-f", name)
	return code == 0
}
func detectStack() Stack {
	if root := os.Getenv("FLOWGATE_ROOT"); root != "" && root != "/" {
		return Stack{Angie: true, Blocky: true}
	}
	return Stack{
		Angie:  isActive("angie") || commandExists("angie"),
		Blocky: isActive("blocky") || commandExists("blocky"),
	}
}

func backupConfigs() error {
	p := getPaths()
	if err := os.MkdirAll(p.BackupDir, 0750); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102_150405")
	for _, src := range []string{p.Blocky, p.AngieMain, p.AngieStream, p.AngieHTTP, p.ConfigFile} {
		if !fileExists(src) {
			continue
		}
		base := filepath.Base(src)
		dst := filepath.Join(p.BackupDir, base+"_"+stamp+".bak")
		if err := copyFile(src, dst); err != nil {
			return err
		}
		matches, _ := filepath.Glob(filepath.Join(p.BackupDir, base+"_*.bak"))
		sort.Strings(matches)
		const keep = 3
		if len(matches) > keep {
			for _, old := range matches[:len(matches)-keep] {
				_ = os.Remove(old)
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
