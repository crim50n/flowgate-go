package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateAngieCandidateDoesNotTouchProduction(t *testing.T) {
	if os.Getenv("FLOWGATE_INTEGRATION") != "1" || !commandExists("angie") {
		t.Skip("requires FLOWGATE_INTEGRATION=1 and Angie")
	}
	p := getPaths()
	if err := ensureAngieConfig(); err != nil {
		t.Fatal(err)
	}
	currentStream := []byte("# current stream\n")
	currentHTTP := []byte("# current http\n")
	if err := writeAtomic(p.AngieStream, currentStream, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(p.AngieHTTP, currentHTTP, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateAngieOnDisk(); err != nil {
		t.Fatal(err)
	}
	beforeDir, err := os.Stat(filepath.Dir(p.AngieStream))
	if err != nil {
		t.Fatal(err)
	}

	candidate := &syncCandidate{
		angieStream: []byte("broken {\n"),
		angieHTTP:   []byte("# candidate http\n"),
	}
	if err := validateAngieCandidate(candidate); err == nil {
		t.Fatal("invalid Angie candidate unexpectedly passed")
	}
	streamData, err := os.ReadFile(p.AngieStream)
	if err != nil {
		t.Fatal(err)
	}
	httpData, err := os.ReadFile(p.AngieHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(streamData, currentStream) || !bytes.Equal(httpData, currentHTTP) {
		t.Fatal("candidate validation modified production Angie files")
	}
	afterDir, err := os.Stat(filepath.Dir(p.AngieStream))
	if err != nil {
		t.Fatal(err)
	}
	if !afterDir.ModTime().Equal(beforeDir.ModTime()) {
		t.Fatalf("production stream directory mtime changed: %v -> %v", beforeDir.ModTime(), afterDir.ModTime())
	}
}

func TestRestoreRuntimeWithNoInitSupervisor(t *testing.T) {
	if os.Getenv("FLOWGATE_INTEGRATION") != "1" || !commandExists("angie") || !commandExists("blocky") {
		t.Skip("requires Flowgate runtime integration container")
	}
	if detectInit() != "none" {
		t.Skip("requires no-init runtime")
	}
	if !isActive("angie") || !isActive("blocky") {
		t.Fatal("Angie and Blocky must be active before runtime rollback test")
	}
	angiePID, err := controlProcessPID("angie")
	if err != nil || angiePID == 0 {
		t.Fatalf("Angie control PID: %d, %v", angiePID, err)
	}
	blockyPID, err := controlProcessPID("blocky")
	if err != nil || blockyPID == 0 {
		t.Fatalf("Blocky control PID: %d, %v", blockyPID, err)
	}

	restoreRuntime(Stack{Angie: true, Blocky: true}, true, true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if isActive("angie") && isActive("blocky") {
			newAngiePID, _ := controlProcessPID("angie")
			newBlockyPID, _ := controlProcessPID("blocky")
			if newAngiePID == angiePID && newBlockyPID != 0 && newBlockyPID != blockyPID {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("runtime rollback did not restore active Angie/Blocky processes")
}
