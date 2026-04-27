// e2e_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// End-to-end integration tests for the full swarm-me pipeline.
// Builds the swarm-me binary and an E2E test harness that masquerades as a
// real LLM CLI, then runs the complete pipeline against realistic fixture
// inputs. Tests cover: full pipeline success, no-provider error handling,
// malformed LLM output, multi-target rendering, and pre-screen finding
// enforcement when the adversarial reviewer approves despite concrete flags.


package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildE2EBinaries compiles both the swarm-me CLI and the E2E harness.
// The harness is placed in a directory as "codex" so discovery finds it.
func buildE2EBinaries(t *testing.T) (swarmMeBin string, harnessDir string) {
	t.Helper()

	binDir := t.TempDir()

	modRoot := moduleRoot(t)

	// Build swarm-me CLI
	swarmMeBin = filepath.Join(binDir, "swarm-me")
	if runtime.GOOS == "windows" {
		swarmMeBin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", swarmMeBin, "./cmd/swarm-me")
	cmd.Dir = modRoot
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building swarm-me: %v\n%s", err, out)
	}

	// Build E2E harness as "codex" -- the user tests with codex mini
	harnessDir = t.TempDir()
	harnessName := "codex"
	if runtime.GOOS == "windows" {
		harnessName = "codex.exe"
	}
	harnessBin := filepath.Join(harnessDir, harnessName)
	cmd = exec.Command("go", "build", "-o", harnessBin, "./internal/cli/testdata/e2e_harness")
	cmd.Dir = modRoot
	cmd.Env = os.Environ()
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building e2e harness: %v\n%s", err, out)
	}

	return swarmMeBin, harnessDir
}

// moduleRoot returns the directory containing go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find module root (go.mod)")
		}
		dir = parent
	}
}

func runSwarmMeE2E(t *testing.T, swarmMeBin, harnessDir, inputDir, outputDir string, extraArgs ...string) (string, string, error) {
	t.Helper()

	args := []string{
		"--input", inputDir,
		"--output-folder", outputDir,
		"--model", "codex",
		"--critique", "codex",
		"--output-swarm", "codex",
		"--force",
		"--name", "SupportOps",
	}
	args = append(args, extraArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, swarmMeBin, args...)
	// Set PATH to harness dir + basic system paths. Do NOT append the full
	// system PATH to avoid discovering real LLM CLI binaries (which could
	// hang waiting for auth/network).
	env := filterEnv(os.Environ())
	env = append(env, "PATH="+harnessDir+":/usr/bin:/bin")
	env = append(env, "SWARMAKER_E2E_INPUT_ROOT="+inputDir)

	cmd.Env = env
	cmd.Dir = outputDir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func filterEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		// Remove existing PATH to avoid picking up real LLM CLIs
		if strings.HasPrefix(e, "PATH=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata/e2e_notes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture dir not found: %v", err)
	}
	return dir
}

func TestE2EFullPipelineSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	swarmMeBin, harnessDir := buildE2EBinaries(t)
	inputDir := fixtureDir(t)
	outputDir := t.TempDir()

	stdoutStr, stderrStr, err := runSwarmMeE2E(t, swarmMeBin, harnessDir, inputDir, outputDir)
	if err != nil {
		t.Fatalf("swarm-me failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdoutStr, stderrStr)
	}

	// Verify .tasks/ ledger files exist
	for _, rel := range ledgerFiles {
		path := filepath.Join(outputDir, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected ledger file %s: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("ledger file %s is empty", rel)
		}
	}

	// Verify evidence.json exists and is valid JSON
	evidencePath := filepath.Join(outputDir, ".tasks", "evidence.json")
	assertValidJSON(t, evidencePath, "evidence.json")

	// Verify manifest.json exists and is valid JSON
	manifestPath := filepath.Join(outputDir, ".tasks", "manifest.json")
	assertValidJSON(t, manifestPath, "manifest.json")

	// Verify .tasks/ir/ has artifact files
	irDir := filepath.Join(outputDir, ".tasks", "ir")
	irEntries, err := os.ReadDir(irDir)
	if err != nil {
		t.Fatalf("reading IR dir: %v", err)
	}
	if len(irEntries) < 7 {
		names := make([]string, 0, len(irEntries))
		for _, e := range irEntries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected at least 7 IR artifact files, got %d: %v", len(irEntries), names)
	}

	// Verify validation-report.md exists
	reportPath := filepath.Join(outputDir, ".tasks", "validation-report.md")
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("expected validation-report.md: %v", err)
	}
	reportContent, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("reading validation-report.md: %v", err)
	}
	if !strings.Contains(string(reportContent), "Decision") {
		t.Fatalf("validation-report.md missing Decision section:\n%s", reportContent)
	}

	// Verify output tree exists (.codex/)
	codexDir := filepath.Join(outputDir, ".codex")
	if _, err := os.Stat(codexDir); err != nil {
		t.Fatalf("expected .codex/ output tree: %v", err)
	}

	// Verify README.md and install.sh exist in output root
	for _, rel := range []string{"README.md", "install.sh"} {
		if _, err := os.Stat(filepath.Join(outputDir, rel)); err != nil {
			t.Fatalf("expected %s in output root: %v", rel, err)
		}
	}
}

func TestE2ENoProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	swarmMeBin, _ := buildE2EBinaries(t)
	inputDir := fixtureDir(t)
	outputDir := t.TempDir()

	// Run with PATH pointing only to an empty directory so no LLM CLI is
	// discoverable. We must NOT append the system PATH, otherwise real
	// codex/claude/gemini binaries could be found and cause hangs.
	emptyDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, swarmMeBin,
		"--input", inputDir,
		"--output-folder", outputDir,
		"--model", "codex",
		"--critique", "codex",
		"--output-swarm", "codex",
		"--force",
		"--name", "SupportOps",
	)
	env := filterEnv(os.Environ())
	// Only include the empty dir and basic system paths (no LLM CLIs).
	// git is needed for repo detection; it lives in /usr/bin on most systems.
	env = append(env, "PATH="+emptyDir+":/usr/bin:/bin")
	cmd.Env = env
	cmd.Dir = outputDir

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error when no LLM provider is available")
	}

	// Verify no partial output was created
	for _, rel := range ledgerFiles {
		path := filepath.Join(outputDir, rel)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("expected no partial output, but found %s", rel)
		}
	}
}

func TestE2EMalformedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	swarmMeBin, harnessDir := buildE2EBinaries(t)
	inputDir := fixtureDir(t)
	outputDir := t.TempDir()

	// Set behavior to return malformed output
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, swarmMeBin,
		"--input", inputDir,
		"--output-folder", outputDir,
		"--model", "codex",
		"--critique", "codex",
		"--output-swarm", "codex",
		"--force",
		"--name", "SupportOps",
	)
	env := filterEnv(os.Environ())
	env = append(env, "PATH="+harnessDir+":/usr/bin:/bin")
	env = append(env, "SWARMAKER_E2E_BEHAVIOR=malformed")
	env = append(env, "SWARMAKER_E2E_INPUT_ROOT="+inputDir)
	cmd.Env = env
	cmd.Dir = outputDir

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected failure with malformed LLM output")
	}
}

func TestE2EMultiTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	swarmMeBin, harnessDir := buildE2EBinaries(t)
	inputDir := fixtureDir(t)
	outputDir := t.TempDir()

	_, _, err := runSwarmMeE2E(t, swarmMeBin, harnessDir, inputDir, outputDir, "--output-swarm", "all")
	if err != nil {
		t.Fatalf("swarm-me --output-swarm all failed: %v", err)
	}

	// Verify all three output trees exist
	for _, dir := range []string{".claude", ".codex", ".gemini"} {
		path := filepath.Join(outputDir, dir)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s output tree: %v", dir, err)
		}
	}
}

func TestE2EApproveWithFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	swarmMeBin, harnessDir := buildE2EBinaries(t)
	inputDir := fixtureDir(t)
	outputDir := t.TempDir()

	// Use short-files behavior: harness returns valid but short content for
	// skills.md and agents.md (triggers pre-screen "suspiciously short for
	// deep source" flags), while the adversarial review returns APPROVE.
	// The pipeline should fail because concrete pre-screen findings override
	// a bare APPROVE verdict (B2 safety gate).
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, swarmMeBin,
		"--input", inputDir,
		"--output-folder", outputDir,
		"--model", "codex",
		"--critique", "codex",
		"--output-swarm", "codex",
		"--force",
		"--name", "SupportOps",
	)
	env := filterEnv(os.Environ())
	env = append(env, "PATH="+harnessDir+":/usr/bin:/bin")
	env = append(env, "SWARMAKER_E2E_BEHAVIOR=short-files")
	env = append(env, "SWARMAKER_E2E_INPUT_ROOT="+inputDir)
	cmd.Env = env
	cmd.Dir = outputDir

	var stderr strings.Builder
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected failure when pre-screen finds concrete issues despite APPROVE")
	}
	if !strings.Contains(stderr.String(), "concrete findings") {
		t.Fatalf("expected 'concrete findings' in error, got:\n%s", stderr.String())
	}
}

func assertValidJSON(t *testing.T, path, name string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s not found: %v", name, err)
	}
	if !json.Valid(data) {
		t.Fatalf("%s is not valid JSON:\n%.200s", name, data)
	}
}
