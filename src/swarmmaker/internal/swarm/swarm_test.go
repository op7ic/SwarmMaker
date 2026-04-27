// swarm_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for the concurrent execution engine.
// Covers task success/failure, round-robin provider assignment, same-provider
// serialization, managed output file creation, and minimum length validation.
// Uses a compiled test harness binary for real subprocess execution.


package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/executor"
	"github.com/op7ic/swarmmaker/prompts"
)

func TestSuccessCount(t *testing.T) {
	results := []Result{
		{Error: nil},
		{Error: nil},
		{Error: fmt.Errorf("fail")},
		{Error: nil},
	}
	if SuccessCount(results) != 3 {
		t.Errorf("SuccessCount = %d, want 3", SuccessCount(results))
	}
}

func TestSuccessCountAllFail(t *testing.T) {
	results := []Result{
		{Error: fmt.Errorf("fail")},
		{Error: fmt.Errorf("fail")},
	}
	if SuccessCount(results) != 0 {
		t.Errorf("SuccessCount = %d, want 0", SuccessCount(results))
	}
}

func TestSuccessCountEmpty(t *testing.T) {
	if SuccessCount(nil) != 0 {
		t.Error("SuccessCount(nil) should be 0")
	}
}

func TestFailures(t *testing.T) {
	results := []Result{
		{Task: Task{Name: "a"}, Error: nil},
		{Task: Task{Name: "b"}, Error: fmt.Errorf("fail")},
		{Task: Task{Name: "c"}, Error: nil},
		{Task: Task{Name: "d"}, Error: fmt.Errorf("fail2")},
	}
	failed := Failures(results)
	if len(failed) != 2 {
		t.Errorf("Failures() returned %d, want 2", len(failed))
	}
	if failed[0].Task.Name != "b" || failed[1].Task.Name != "d" {
		t.Errorf("Failures() returned wrong tasks: %v", failed)
	}
}

func TestFailuresAllSuccess(t *testing.T) {
	results := []Result{
		{Error: nil},
		{Error: nil},
	}
	failed := Failures(results)
	if len(failed) != 0 {
		t.Errorf("Failures() should return empty for all-success, got %d", len(failed))
	}
}

// --- BuildTasks tests ---
// The stage-1 .tasks ledger has 9 decomposition artifacts.

func TestBuildTasksReturnsExpectedCount(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	if len(tasks) != 9 {
		t.Errorf("BuildTasks() returned %d tasks, want 9", len(tasks))
	}
}

func TestBuildTasksIncludesLedgerArtifacts(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	required := map[string]bool{
		".tasks/context.md":            false,
		".tasks/tasks.md":              false,
		".tasks/prompts/product.md":    false,
		".tasks/prompts/technical.md":  false,
		".tasks/prompts/tools.md":      false,
		".tasks/prompts/deployment.md": false,
		".tasks/todo.md":               false,
		".tasks/skills.md":             false,
		".tasks/agents.md":             false,
	}
	for _, task := range tasks {
		if _, ok := required[task.OutputFile]; ok {
			required[task.OutputFile] = true
		}
		if !strings.HasPrefix(task.OutputFile, ".tasks/") {
			t.Errorf("task %q outputs outside .tasks/: %s", task.Name, task.OutputFile)
		}
	}
	for file, found := range required {
		if !found {
			t.Errorf("BuildTasks missing required ledger artifact %q", file)
		}
	}
}

func TestBuildTasksAllHaveRequiredFields(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	for _, task := range tasks {
		if task.Name == "" {
			t.Error("task has empty Name")
		}
		if task.OutputFile == "" {
			t.Error("task has empty OutputFile")
		}
		if task.Prompt == "" {
			t.Errorf("task %q has empty Prompt", task.Name)
		}
		if task.MinLen <= 0 {
			t.Errorf("task %q has non-positive MinLen: %d", task.Name, task.MinLen)
		}
	}
}

func TestBuildTasksUniqueNames(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	seen := make(map[string]bool)
	for _, task := range tasks {
		if seen[task.Name] {
			t.Errorf("duplicate task name: %q", task.Name)
		}
		seen[task.Name] = true
	}
}

func TestBuildTasksUniqueOutputFiles(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	seen := make(map[string]bool)
	for _, task := range tasks {
		if seen[task.OutputFile] {
			t.Errorf("duplicate output file: %q", task.OutputFile)
		}
		seen[task.OutputFile] = true
	}
}

func TestBuildTasksIncludesSourceHints(t *testing.T) {
	hints := "SOURCE MATERIAL ANALYSIS: 20 dimensions\n"
	tasks, err := BuildTasks(testPromptIR(), hints)
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	for _, task := range tasks {
		if !strings.Contains(task.Prompt, hints) {
			t.Errorf("task %q prompt does not contain source hints", task.Name)
		}
	}
}

func TestBuildTasksEmptyHintsStillWorks(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	for _, task := range tasks {
		if task.Prompt == "" {
			t.Errorf("task %q has empty prompt even with no hints", task.Name)
		}
	}
}

func TestBuildTasksCriticalFilesPresent(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	criticalOutputs := map[string]bool{
		".tasks/context.md":            false,
		".tasks/tasks.md":              false,
		".tasks/todo.md":               false,
		".tasks/skills.md":             false,
		".tasks/agents.md":             false,
		".tasks/prompts/product.md":    false,
		".tasks/prompts/technical.md":  false,
		".tasks/prompts/tools.md":      false,
		".tasks/prompts/deployment.md": false,
	}
	for _, task := range tasks {
		if _, ok := criticalOutputs[task.OutputFile]; ok {
			criticalOutputs[task.OutputFile] = true
		}
	}
	for file, found := range criticalOutputs {
		if !found {
			t.Errorf("critical file %q not in BuildTasks output", file)
		}
	}
}

func TestBuildTasksRejectsInvalidPromptIR(t *testing.T) {
	ir := testPromptIR()
	ir.TargetFormats = nil
	if _, err := BuildTasks(ir, ""); err == nil || !strings.Contains(err.Error(), "target_formats") {
		t.Fatalf("expected prompt IR validation failure, got %v", err)
	}
}

func TestBuildTasksPromptsIncludeTargetFormatAndEvidence(t *testing.T) {
	tasks, err := BuildTasks(testPromptIR(), "")
	if err != nil {
		t.Fatalf("BuildTasks failed: %v", err)
	}
	for _, task := range tasks {
		for _, want := range []string{"target_output_formats: gemini", "evidence_manifest: .tasks/evidence.json", "ir_manifest: .tasks/manifest.json"} {
			if !strings.Contains(task.Prompt, want) {
				t.Fatalf("task %s prompt missing %q", task.Name, want)
			}
		}
	}
}

func TestNewUsesSingleConcurrencyForSameProvider(t *testing.T) {
	exec := executor.New(
		discovery.LLMTool{Name: "codex", Available: true},
		discovery.LLMTool{Name: "codex", Available: true},
		false,
	)
	swarm := New(exec, t.TempDir(), false)
	if swarm.Concurrency != 1 {
		t.Fatalf("Concurrency = %d, want 1 for same-provider runs", swarm.Concurrency)
	}
}

func TestNewKeepsParallelismForMixedProviders(t *testing.T) {
	exec := executor.New(
		discovery.LLMTool{Name: "codex", Available: true},
		discovery.LLMTool{Name: "gemini", Available: true},
		false,
	)
	swarm := New(exec, t.TempDir(), false)
	if swarm.Concurrency != 2 {
		t.Fatalf("Concurrency = %d, want 2 for mixed-provider runs", swarm.Concurrency)
	}
}

// --- Swarm.Run tests ---

func buildSwarmHarness(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "swarm-harness")
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/harness")
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building swarm harness binary: %v\n%s", err, output)
	}
	return binPath
}

func swarmTool(name, path string) discovery.LLMTool {
	return discovery.LLMTool{
		Name:         name,
		Path:         path,
		Available:    true,
		Capabilities: discovery.CapabilitiesForTool(name),
	}
}

func TestSwarmRunTwoTasksOneProviderSuccess(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	tool := swarmTool("claude", bin)
	exec := executor.New(tool, tool, false)
	exec.Timeout = 10 * time.Second

	sw := &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: 1, // same provider means serialized
	}

	tasks := []Task{
		{Name: "task-a", OutputFile: "a.md", Prompt: "Generate A", MinLen: 10},
		{Name: "task-b", OutputFile: "b.md", Prompt: "Generate B", MinLen: 10},
	}

	results := sw.Run(tasks)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("task %d (%s) failed: %v", i, r.Task.Name, r.Error)
		}
		if r.Content == "" {
			t.Errorf("task %d (%s) has empty content", i, r.Task.Name)
		}
		if r.Tool == "" {
			t.Errorf("task %d (%s) has empty Tool", i, r.Task.Name)
		}
		if r.Duration == 0 {
			t.Errorf("task %d (%s) has zero Duration", i, r.Task.Name)
		}
	}
	if SuccessCount(results) != 2 {
		t.Errorf("SuccessCount = %d, want 2", SuccessCount(results))
	}

	// Verify output files were actually created
	for _, task := range tasks {
		path := filepath.Join(outputDir, task.OutputFile)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("output file %s not created: %v", path, err)
		}
	}
}

func TestSwarmRunRoundRobinTwoProviders(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	primary := swarmTool("claude", bin)
	critic := swarmTool("gemini", bin)
	exec := executor.New(primary, critic, false)
	exec.Timeout = 10 * time.Second

	sw := &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: 2,
	}

	tasks := []Task{
		{Name: "task-0", OutputFile: "t0.md", Prompt: "Prompt 0", MinLen: 10},
		{Name: "task-1", OutputFile: "t1.md", Prompt: "Prompt 1", MinLen: 10},
		{Name: "task-2", OutputFile: "t2.md", Prompt: "Prompt 2", MinLen: 10},
		{Name: "task-3", OutputFile: "t3.md", Prompt: "Prompt 3", MinLen: 10},
	}

	results := sw.Run(tasks)
	if SuccessCount(results) != 4 {
		for _, r := range results {
			if r.Error != nil {
				t.Logf("task %s failed: %v", r.Task.Name, r.Error)
			}
		}
		t.Fatalf("SuccessCount = %d, want 4", SuccessCount(results))
	}

	// Verify round-robin assignment: even tasks -> primary (claude), odd tasks -> critic (gemini)
	if results[0].Tool != "claude" {
		t.Errorf("task 0 Tool = %q, want claude (primary)", results[0].Tool)
	}
	if results[1].Tool != "gemini" {
		t.Errorf("task 1 Tool = %q, want gemini (critic)", results[1].Tool)
	}
	if results[2].Tool != "claude" {
		t.Errorf("task 2 Tool = %q, want claude (primary)", results[2].Tool)
	}
	if results[3].Tool != "gemini" {
		t.Errorf("task 3 Tool = %q, want gemini (critic)", results[3].Tool)
	}
}

func TestSwarmRunTaskFailureHandling(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "fail")

	outputDir := t.TempDir()
	tool := swarmTool("claude", bin)
	exec := executor.New(tool, tool, false)
	exec.Timeout = 10 * time.Second

	sw := &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: 1,
	}

	tasks := []Task{
		{Name: "will-fail", OutputFile: "fail.md", Prompt: "Generate", MinLen: 10},
	}

	results := sw.Run(tasks)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("expected task to fail")
	}
	if SuccessCount(results) != 0 {
		t.Errorf("SuccessCount = %d, want 0", SuccessCount(results))
	}
	failed := Failures(results)
	if len(failed) != 1 {
		t.Errorf("Failures returned %d, want 1", len(failed))
	}
	if failed[0].Task.Name != "will-fail" {
		t.Errorf("failed task Name = %q, want %q", failed[0].Task.Name, "will-fail")
	}
}

func TestSwarmRunSameProviderSerialization(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	tool := swarmTool("claude", bin)
	exec := executor.New(tool, tool, false)
	exec.Timeout = 10 * time.Second

	// New() should set concurrency=1 for same-provider
	sw := New(exec, outputDir, false)
	if sw.Concurrency != 1 {
		t.Fatalf("Concurrency = %d, want 1 for same-provider", sw.Concurrency)
	}

	tasks := []Task{
		{Name: "serial-a", OutputFile: "sa.md", Prompt: "Prompt A", MinLen: 10},
		{Name: "serial-b", OutputFile: "sb.md", Prompt: "Prompt B", MinLen: 10},
	}

	results := sw.Run(tasks)
	if SuccessCount(results) != 2 {
		t.Fatalf("SuccessCount = %d, want 2", SuccessCount(results))
	}
}

func TestSwarmRunManagedOutputFileCreation(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	tool := swarmTool("claude", bin)
	exec := executor.New(tool, tool, false)
	exec.Timeout = 10 * time.Second

	sw := &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: 1,
	}

	// Use nested subdirectory in OutputFile to test MkdirAll
	tasks := []Task{
		{Name: "nested", OutputFile: "sub/dir/output.md", Prompt: "Generate nested", MinLen: 10},
	}

	results := sw.Run(tasks)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("nested task failed: %v", results[0].Error)
	}

	nestedPath := filepath.Join(outputDir, "sub", "dir", "output.md")
	info, err := os.Stat(nestedPath)
	if err != nil {
		t.Fatalf("nested output file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("nested output file is empty")
	}
}

func TestSwarmRunMinLenValidation(t *testing.T) {
	bin := buildSwarmHarness(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")
	// Set a very short file content that is above minOutputLen but below task MinLen
	t.Setenv("SWARMAKER_TEST_FILE_CONTENT", strings.Repeat("x", 250))

	outputDir := t.TempDir()
	tool := swarmTool("claude", bin)
	exec := executor.New(tool, tool, false)
	exec.Timeout = 10 * time.Second

	sw := &Swarm{
		Exec:        exec,
		OutputDir:   outputDir,
		Concurrency: 1,
	}

	tasks := []Task{
		{Name: "short-output", OutputFile: "short.md", Prompt: "Generate", MinLen: 5000},
	}

	results := sw.Run(tasks)
	if results[0].Error == nil {
		t.Fatal("expected MinLen validation to fail")
	}
	if !strings.Contains(results[0].Error.Error(), "too short") {
		t.Fatalf("error = %v, want 'too short' failure", results[0].Error)
	}
}

func testPromptIR() prompts.PromptIR {
	return prompts.PromptIR{
		ProjectName:          "TestProject",
		SourceMaterial:       "# Notes\nBuild a swarm.\n",
		TargetFormats:        []string{"gemini"},
		GeneratorProvider:    "codex",
		CriticProvider:       "gemini",
		OutputRenderers:      []string{"gemini"},
		EvidenceManifestPath: ".tasks/evidence.json",
		IRManifestPath:       ".tasks/manifest.json",
		PromptPackName:       "swarmmaker-default",
		PromptPackSource:     "embedded:prompts/default_pack.json",
		PromptPackDigest:     "sha256:test",
		InputFileCount:       1,
		BinaryFileCount:      0,
		EvidenceEventCount:   0,
		ToolLanguages:        []string{"go"},
	}
}

// --- Two-phase generation tests ---

func TestTwoPhaseGeneration(t *testing.T) {
	ir := testPromptIR()
	pack, err := prompts.DefaultPack()
	if err != nil {
		t.Fatalf("DefaultPack failed: %v", err)
	}

	phaseA, err := BuildPhaseATasks(ir, "", pack)
	if err != nil {
		t.Fatalf("BuildPhaseATasks failed: %v", err)
	}
	if len(phaseA) != 2 {
		t.Fatalf("BuildPhaseATasks returned %d tasks, want 2", len(phaseA))
	}

	// Verify Phase A contains context and tasks
	names := map[string]bool{}
	for _, task := range phaseA {
		names[task.Name] = true
	}
	if !names["context"] {
		t.Error("Phase A missing 'context' task")
	}
	if !names["tasks"] {
		t.Error("Phase A missing 'tasks' task")
	}

	phaseB, err := BuildPhaseBTasks(ir, "", pack, "LEDGER CONTEXT: test\n")
	if err != nil {
		t.Fatalf("BuildPhaseBTasks failed: %v", err)
	}
	if len(phaseB) != 7 {
		t.Fatalf("BuildPhaseBTasks returned %d tasks, want 7", len(phaseB))
	}

	// Verify Phase B prompts include ledger context
	for _, task := range phaseB {
		if !strings.Contains(task.Prompt, "LEDGER CONTEXT: test") {
			t.Errorf("Phase B task %q missing ledger context prefix", task.Name)
		}
	}

	// Verify combined count equals original BuildTasksWithPack
	allTasks, err := BuildTasksWithPack(ir, "", pack)
	if err != nil {
		t.Fatalf("BuildTasksWithPack failed: %v", err)
	}
	if len(phaseA)+len(phaseB) != len(allTasks) {
		t.Errorf("phase A (%d) + phase B (%d) != total (%d)", len(phaseA), len(phaseB), len(allTasks))
	}
}
