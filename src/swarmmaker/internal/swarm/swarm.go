// swarm.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Core concurrent task execution engine.
// Runs multiple LLM tasks in parallel (or serial for same-provider) with
// configurable concurrency, round-robin provider assignment, managed output
// file registration, and minimum output length validation. This is what
// drives the 9-task Stage 1 generation phase.


package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"

	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/executor"
)

// Task defines one file to produce via LLM.
type Task struct {
	Name       string // human name: "product", "technical", etc.
	OutputFile string // relative path from output dir: ".tasks/tasks.md"
	Prompt     string // complete prompt for this task
	MinLen     int    // minimum acceptable output length (chars)
}

// Result captures the outcome of one task.
type Result struct {
	Task     Task
	Content  string
	Duration time.Duration
	Tool     string // which LLM tool was used
	Error    error
}

// Swarm runs LLM tasks in parallel with a concurrency limit.
type Swarm struct {
	Exec        *executor.Executor
	OutputDir   string
	Concurrency int // max parallel LLM calls
	Verbose     bool
}

// New creates a Swarm runner with the initial concurrency limit used by the CLI.
func New(exec *executor.Executor, outputDir string, verbose bool) *Swarm {
	return &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: defaultConcurrency(exec),
		Verbose:     verbose,
	}
}

func defaultConcurrency(exec *executor.Executor) int {
	if exec != nil && exec.Primary.Name != "" && exec.Primary.Name == exec.Critic.Name {
		// Serialize same-provider runs to avoid overlapping CLI sessions racing in
		// one workspace. Mixed-provider runs can still overlap safely.
		return 1
	}
	return 2
}

// Run executes all tasks with the configured concurrency limit.
// Tasks are distributed round-robin across the primary and critic tools.
// Returns results for all tasks (check each Result.Error).
func (s *Swarm) Run(tasks []Task) []Result {
	results := make([]Result, len(tasks))
	sem := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // protects printing

	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)

	tools := []discovery.LLMTool{s.Exec.Primary, s.Exec.Critic}
	managedOutputs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		managedOutputs = append(managedOutputs, filepath.Join(s.OutputDir, task.OutputFile))
	}
	s.Exec.SetManagedOutputFiles(managedOutputs)
	defer s.Exec.ClearManagedOutputFiles()

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t Task) {
			defer wg.Done()

			sem <- struct{}{}        // acquire slot
			defer func() { <-sem }() // release slot

			// Round-robin tool assignment
			tool := tools[idx%len(tools)]

			absOutputPath := filepath.Join(s.OutputDir, t.OutputFile)

			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(absOutputPath), 0755); err != nil {
				results[idx] = Result{Task: t, Error: fmt.Errorf("mkdir: %w", err), Tool: tool.Name}
				return
			}

			start := time.Now()

			if s.Verbose {
				mu.Lock()
				yellow.Printf("    [%s] Starting %s...\n", tool.Name, t.Name)
				mu.Unlock()
			}

			// Determine model override
			model := ""
			if s.Exec.Models != nil {
				model = s.Exec.Models[tool.Name]
			}

			resp, err := s.Exec.RunToolToFile(tool, model, t.Prompt, absOutputPath)
			duration := time.Since(start)

			if err != nil {
				results[idx] = Result{Task: t, Duration: duration, Error: err, Tool: tool.Name}
				mu.Lock()
				red.Printf("    x %s failed (%s, %v)\n", t.OutputFile, tool.Name, duration.Round(time.Second))
				mu.Unlock()
				return
			}

			content := resp.Output
			// Validate minimum length
			if len(strings.TrimSpace(content)) < t.MinLen {
				results[idx] = Result{
					Task: t, Content: content, Duration: duration, Tool: tool.Name,
					Error: fmt.Errorf("%s output too short (%d chars, need %d)", t.Name, len(content), t.MinLen),
				}
				mu.Lock()
				red.Printf("    x %s too short (%d chars, %s, %v)\n", t.OutputFile, len(content), tool.Name, duration.Round(time.Second))
				mu.Unlock()
				return
			}

			results[idx] = Result{Task: t, Content: content, Duration: duration, Tool: tool.Name}

			mu.Lock()
			green.Printf("    + %s (%d chars, %s, %v)\n", t.OutputFile, len(content), tool.Name, duration.Round(time.Second))
			mu.Unlock()
		}(i, task)
	}

	wg.Wait()
	return results
}

// SuccessCount returns the number of tasks that completed without error.
func SuccessCount(results []Result) int {
	n := 0
	for _, r := range results {
		if r.Error == nil {
			n++
		}
	}
	return n
}

// Failures returns only the failed results.
func Failures(results []Result) []Result {
	var failed []Result
	for _, r := range results {
		if r.Error != nil {
			failed = append(failed, r)
		}
	}
	return failed
}
