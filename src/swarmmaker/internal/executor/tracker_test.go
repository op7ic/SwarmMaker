// tracker_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for process lifecycle tracker.

package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProcessTrackerLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "processes.json")
	tracker := NewProcessTracker(path)

	// Track a process
	tracker.Track(12345, "claude", "context", "generator")

	// Verify it's running
	running := tracker.Running()
	if len(running) != 1 {
		t.Fatalf("expected 1 running, got %d", len(running))
	}
	if running[0].PID != 12345 {
		t.Fatalf("expected PID 12345, got %d", running[0].PID)
	}
	if running[0].Provider != "claude" {
		t.Fatalf("expected provider claude, got %s", running[0].Provider)
	}

	// Verify file was persisted
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading process file: %v", err)
	}
	var state struct {
		ParentPID int            `json:"parent_pid"`
		Processes []ProcessEntry `json:"processes"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parsing process file: %v", err)
	}
	if state.ParentPID == 0 {
		t.Fatal("parent PID should be non-zero")
	}
	if len(state.Processes) != 1 {
		t.Fatalf("expected 1 process in file, got %d", len(state.Processes))
	}

	// Complete the process
	tracker.Complete(12345, 0, false)

	running = tracker.Running()
	if len(running) != 0 {
		t.Fatalf("expected 0 running after complete, got %d", len(running))
	}

	// Summary should reflect completion
	summary := tracker.Summary()
	if summary == "" {
		t.Fatal("summary should not be empty")
	}

	// Cleanup removes the file
	tracker.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("process file should be removed after cleanup")
	}
}

func TestProcessTrackerMultipleProcesses(t *testing.T) {
	tracker := NewProcessTracker("")

	tracker.Track(100, "claude", "context", "generator")
	tracker.Track(200, "codex", "tasks", "generator")
	tracker.Track(300, "claude", "review", "critic")

	running := tracker.Running()
	if len(running) != 3 {
		t.Fatalf("expected 3 running, got %d", len(running))
	}

	tracker.Complete(200, 0, false)
	tracker.Complete(100, 1, true)

	running = tracker.Running()
	if len(running) != 1 {
		t.Fatalf("expected 1 running, got %d", len(running))
	}
	if running[0].PID != 300 {
		t.Fatalf("expected PID 300 still running, got %d", running[0].PID)
	}
}
