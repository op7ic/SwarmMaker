// tracker.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Process tracker for LLM subprocess lifecycle management.
// Records every spawned child process with PID, provider, task name,
// start time, and status. Writes state to a JSON file so running
// processes can be inspected externally. Only processes tracked here
// should ever be terminated by SwarmMaker.

package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ProcessEntry records a single subprocess spawned by the executor.
type ProcessEntry struct {
	PID       int       `json:"pid"`
	Provider  string    `json:"provider"`
	Task      string    `json:"task"`
	Role      string    `json:"role"` // "generator", "critic", "preflight"
	StartedAt time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Status    string    `json:"status"` // "running", "completed", "failed", "killed"
	ExitCode  *int      `json:"exit_code,omitempty"`
}

// ProcessTracker maintains a thread-safe registry of all child processes
// spawned by this SwarmMaker run. It persists state to a JSON file so
// external tools (or a future `swarm-maker ps` command) can inspect
// what's running.
type ProcessTracker struct {
	mu        sync.Mutex
	entries   []ProcessEntry
	filePath  string // path to write process state (e.g., .tasks/processes.json)
	parentPID int    // PID of the swarm-maker process itself
}

// NewProcessTracker creates a tracker that writes state to the given file path.
func NewProcessTracker(filePath string) *ProcessTracker {
	return &ProcessTracker{
		filePath:  filePath,
		parentPID: os.Getpid(),
		entries:   make([]ProcessEntry, 0),
	}
}

// Track records a newly started process. Call immediately after cmd.Start() succeeds.
func (pt *ProcessTracker) Track(pid int, provider, task, role string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.entries = append(pt.entries, ProcessEntry{
		PID:       pid,
		Provider:  provider,
		Task:      task,
		Role:      role,
		StartedAt: time.Now(),
		Status:    "running",
	})
	pt.persist()
}

// Complete marks a tracked process as finished.
func (pt *ProcessTracker) Complete(pid int, exitCode int, failed bool) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	for i := range pt.entries {
		if pt.entries[i].PID == pid && pt.entries[i].Status == "running" {
			now := time.Now()
			pt.entries[i].EndedAt = &now
			pt.entries[i].ExitCode = &exitCode
			if failed {
				pt.entries[i].Status = "failed"
			} else {
				pt.entries[i].Status = "completed"
			}
			break
		}
	}
	pt.persist()
}

// Running returns all currently running processes.
func (pt *ProcessTracker) Running() []ProcessEntry {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	var running []ProcessEntry
	for _, e := range pt.entries {
		if e.Status == "running" {
			running = append(running, e)
		}
	}
	return running
}

// Summary returns a human-readable summary of tracked processes.
func (pt *ProcessTracker) Summary() string {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	running := 0
	completed := 0
	failed := 0
	for _, e := range pt.entries {
		switch e.Status {
		case "running":
			running++
		case "completed":
			completed++
		case "failed", "killed":
			failed++
		}
	}
	return fmt.Sprintf("processes: %d total (%d running, %d completed, %d failed), parent PID: %d",
		len(pt.entries), running, completed, failed, pt.parentPID)
}

// persist writes the current state to the JSON file.
func (pt *ProcessTracker) persist() {
	if pt.filePath == "" {
		return
	}

	state := struct {
		ParentPID int            `json:"parent_pid"`
		UpdatedAt time.Time      `json:"updated_at"`
		Processes []ProcessEntry `json:"processes"`
	}{
		ParentPID: pt.parentPID,
		UpdatedAt: time.Now(),
		Processes: pt.entries,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return // best-effort persistence
	}
	_ = os.WriteFile(pt.filePath, data, 0644)
}

// Cleanup removes the process file. Call when the run completes.
func (pt *ProcessTracker) Cleanup() {
	if pt.filePath != "" {
		_ = os.Remove(pt.filePath)
	}
}
