package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("FLOWGATE_PROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func TestControlProcessPIDUsesExactExecutable(t *testing.T) {
	name := fmt.Sprintf("flowgate-proc-%d", os.Getpid())
	testExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := copyExecutable(testExe, path); err != nil {
		t.Fatal(err)
	}

	realProcess := exec.Command(path, "-test.run=TestProcessHelper")
	realProcess.Env = append(os.Environ(), "FLOWGATE_PROCESS_HELPER=1")
	if err := realProcess.Start(); err != nil {
		t.Fatal(err)
	}
	defer realProcess.Process.Kill()

	decoy := exec.Command(testExe, "-test.run=TestProcessHelper")
	decoy.Env = append(os.Environ(), "FLOWGATE_PROCESS_HELPER=1")
	decoy.Args[0] = name
	if err := decoy.Start(); err != nil {
		t.Fatal(err)
	}
	defer decoy.Process.Kill()
	deadline := time.Now().Add(2 * time.Second)
	for {
		pid, err := controlProcessPID(name)
		if err == nil && pid == realProcess.Process.Pid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exact process not selected: pid=%d err=%v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	pids := exactProcessPIDs(name)
	if len(pids) != 1 || pids[0] != realProcess.Process.Pid {
		t.Fatalf("exactProcessPIDs=%v, real=%d decoy=%d", pids, realProcess.Process.Pid, decoy.Process.Pid)
	}
	if err := signalProcess(name, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := realProcess.Wait(); err == nil {
		t.Fatal("expected signal termination")
	}
	if err := syscall.Kill(decoy.Process.Pid, 0); err != nil {
		t.Fatalf("decoy was affected: %v", err)
	}
}
