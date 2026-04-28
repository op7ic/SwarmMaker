// executor_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for the LLM CLI executor.
// Uses a compiled Go test harness binary to simulate LLM CLI behavior.
// Covers stdout/stderr capture, non-zero exit, timeout handling, partial
// writes, workspace write validation, managed output files, large prompt
// stdin transport, argument construction for all providers, and cleanup.


package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/op7ic/swarmmaker/internal/discovery"
)

func TestRunStdoutOnly(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdout-only")

	e := &Executor{Timeout: time.Second}
	resp, err := e.run(harnessTool(bin), "prompt", "primary", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	if got := len(resp.Output); got < minOutputLen {
		t.Fatalf("stdout length = %d, want at least %d", got, minOutputLen)
	}
	if resp.FallbackCount != 0 {
		t.Fatalf("fallback count = %d, want 0", resp.FallbackCount)
	}
}

func TestRunCapturesStderrAndNonZeroExit(t *testing.T) {
	bin := buildHarnessBinary(t)
	e := &Executor{Timeout: time.Second}

	t.Run("stderr", func(t *testing.T) {
		t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stderr")
		resp, err := e.run(harnessTool(bin), "prompt", "primary", "")
		if err == nil {
			t.Fatal("expected error for stderr exit")
		}
		if resp == nil || resp.Error == nil {
			t.Fatalf("response error = %#v, want non-nil", resp)
		}
		if !strings.Contains(resp.Output, "stderr harness output") {
			t.Fatalf("output = %q, want stderr content", resp.Output)
		}
	})

	t.Run("non-zero", func(t *testing.T) {
		t.Setenv("SWARMAKER_TEST_BEHAVIOR", "non-zero")
		resp, err := e.run(harnessTool(bin), "prompt", "primary", "")
		if err == nil {
			t.Fatal("expected error for non-zero exit")
		}
		if resp == nil || resp.Error == nil {
			t.Fatalf("response error = %#v, want non-nil", resp)
		}
		if !strings.Contains(resp.Output, "stdout harness output") {
			t.Fatalf("output = %q, want stdout content", resp.Output)
		}
	})
}

func TestRunTimeout(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "timeout")
	t.Setenv("SWARMAKER_TEST_SLEEP_MS", "200")

	e := &Executor{Timeout: 25 * time.Millisecond}
	_, err := e.run(harnessTool(bin), "prompt", "primary", "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout failure", err)
	}
}

func TestClassifyTimeoutCodexInteractiveWait(t *testing.T) {
	err := classifyTimeout(
		"codex",
		25*time.Millisecond,
		"/tmp/out.md",
		"Reading additional input from stdin...\npartial output",
	)
	if err == nil {
		t.Fatal("expected timeout classification error")
	}
	text := err.Error()
	for _, want := range []string{"stdin", "interactive", "Codex CLI/runtime hang"} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeout error missing %q: %s", want, text)
		}
	}
}

func TestClassifyTimeoutReportsMissingOutputContract(t *testing.T) {
	err := classifyTimeout("codex", time.Second, "/tmp/missing-output.md", "")
	if err == nil {
		t.Fatal("expected timeout classification error")
	}
	text := err.Error()
	for _, want := range []string{"output contract", "not created yet", "provider latency or a hung CLI invocation"} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeout error missing %q: %s", want, text)
		}
	}
}

func TestRunToFileRejectsPartialWriteEvenWhenStdoutIsValid(t *testing.T) {
	bin := buildHarnessBinary(t)
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "result.md")

	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "partial-write")
	t.Setenv("SWARMAKER_TEST_STDOUT", strings.Repeat("stdout harness output ", 20))

	e := &Executor{Timeout: time.Second}
	resp, err := e.runToFile(harnessTool(bin), "prompt", outputPath, "primary")
	if err == nil {
		t.Fatal("expected partial file-write contract failure")
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("response = %#v, want error response", resp)
	}
	if !strings.Contains(err.Error(), "file-write failed") {
		t.Fatalf("error = %v, want file-write failure", err)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading partial output file: %v", err)
	}
	if string(written) == resp.Output {
		t.Fatalf("stdout was copied into output file despite file-write contract failure")
	}
}

func TestRunToFileRejectsUnexpectedWorkspaceWrites(t *testing.T) {
	bin := buildHarnessBinary(t)
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "result.md")
	extraPath := filepath.Join(outputDir, "unexpected.md")

	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-extra-file")
	t.Setenv("SWARMAKER_TEST_EXTRA_FILE", extraPath)

	e := &Executor{Timeout: time.Second, StagingDir: filepath.Join(outputDir, ".staging")}
	if err := os.MkdirAll(e.StagingDir, 0755); err != nil {
		t.Fatalf("creating staging dir: %v", err)
	}

	resp, err := e.runToFile(harnessTool(bin), "prompt", outputPath, "primary")
	if err == nil {
		t.Fatal("expected workspace write validation failure")
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("response = %#v, want error response", resp)
	}
	if !strings.Contains(err.Error(), "unexpected workspace files") {
		t.Fatalf("error = %v, want unexpected workspace write failure", err)
	}
	if _, statErr := os.Stat(extraPath); statErr != nil {
		t.Fatalf("expected extra file to exist for evidence, got: %v", statErr)
	}
}

func TestRunToFileRejectsWorkspaceDeletions(t *testing.T) {
	bin := buildHarnessBinary(t)
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "result.md")
	victimPath := filepath.Join(outputDir, "victim.md")

	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "delete-file")
	t.Setenv("SWARMAKER_TEST_DELETE_FILE", victimPath)

	if err := os.WriteFile(victimPath, []byte("keep me"), 0644); err != nil {
		t.Fatalf("creating victim file: %v", err)
	}

	e := &Executor{Timeout: time.Second, StagingDir: filepath.Join(outputDir, ".staging")}
	if err := os.MkdirAll(e.StagingDir, 0755); err != nil {
		t.Fatalf("creating staging dir: %v", err)
	}

	resp, err := e.runToFile(harnessTool(bin), "prompt", outputPath, "primary")
	if err == nil {
		t.Fatal("expected workspace deletion validation failure")
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("response = %#v, want error response", resp)
	}
	if !strings.Contains(err.Error(), "unexpected workspace files") {
		t.Fatalf("error = %v, want unexpected workspace write failure", err)
	}
	if _, statErr := os.Stat(victimPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected victim file to be deleted, got: %v", statErr)
	}
}

func TestRunToFileAllowsManagedWorkspaceWrites(t *testing.T) {
	bin := buildHarnessBinary(t)
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "result.md")
	extraPath := filepath.Join(outputDir, "other-task.md")

	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-extra-file")
	t.Setenv("SWARMAKER_TEST_EXTRA_FILE", extraPath)

	e := &Executor{Timeout: time.Second, StagingDir: filepath.Join(outputDir, ".staging")}
	if err := os.MkdirAll(e.StagingDir, 0755); err != nil {
		t.Fatalf("creating staging dir: %v", err)
	}
	e.SetManagedOutputFiles([]string{outputPath, extraPath})
	defer e.ClearManagedOutputFiles()

	resp, err := e.runToFile(harnessTool(bin), "prompt", outputPath, "primary")
	if err != nil {
		t.Fatalf("unexpected error with managed outputs: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("response = %#v, want successful response", resp)
	}
	if _, statErr := os.Stat(extraPath); statErr != nil {
		t.Fatalf("expected managed output to exist, got: %v", statErr)
	}
}

func TestBuildArgsCodexUsesNativeOutputCapture(t *testing.T) {
	args, err := buildArgs("codex", "prompt body", "/tmp/work", "gpt-5.4-mini", "/tmp/out.md", false, true)
	if err != nil {
		t.Fatalf("buildArgs error: %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-o /tmp/out.md") {
		t.Fatalf("args = %q, want -o output path", joined)
	}
	if !strings.Contains(joined, "-C /tmp/work") {
		t.Fatalf("args = %q, want workdir", joined)
	}
	if !strings.Contains(joined, "-s read-only") {
		t.Fatalf("args = %q, want read-only sandbox", joined)
	}
	if strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %q, did not want sandbox bypass", joined)
	}
	if !strings.Contains(joined, "prompt body") {
		t.Fatalf("args = %q, want prompt body", joined)
	}
}

func TestBuildArgsCodexStdoutOnlyUsesReadOnlySandbox(t *testing.T) {
	args, err := buildArgs("codex", "APPROVE", "/tmp/work", "gpt-5.4-mini", "", false, true)
	if err != nil {
		t.Fatalf("buildArgs error: %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-s read-only") {
		t.Fatalf("args = %q, want read-only sandbox", joined)
	}
	if strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("args = %q, did not want sandbox bypass", joined)
	}
}

func TestBuildArgsClaudeDoesNotExposeNativeOutputCaptureFlag(t *testing.T) {
	args, err := buildArgs("claude", "prompt body", "/tmp/work", "sonnet", "/tmp/out.md", false, false)
	if err != nil {
		t.Fatalf("buildArgs error: %v", err)
	}

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-o /tmp/out.md") {
		t.Fatalf("args = %q, did not want codex-only output flag", joined)
	}
}

func TestValidateToolRejectsMissingBinaryAndMalformedMetadata(t *testing.T) {
	e := &Executor{}

	missing := discovery.LLMTool{
		Name:         "claude",
		Path:         filepath.Join(t.TempDir(), "missing-binary"),
		Available:    true,
		Capabilities: discovery.CapabilitiesForTool("claude"),
	}
	if err := e.validateTool(missing); err == nil || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("missing binary error = %v, want accessibility failure", err)
	}

	malformed := discovery.LLMTool{
		Name:         "claude",
		Path:         filepath.Join(t.TempDir(), "fake-binary"),
		Available:    true,
		Capabilities: []discovery.Capability{discovery.CapabilityGenerate, discovery.Capability("bogus")},
	}
	if err := os.WriteFile(malformed.Path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("creating fake binary: %v", err)
	}
	if err := e.validateTool(malformed); err == nil || !strings.Contains(err.Error(), "unsupported capability") {
		t.Fatalf("malformed metadata error = %v, want capability validation failure", err)
	}
}

func buildHarnessBinary(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "provider-harness")
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/harness")
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building harness binary: %v\n%s", err, output)
	}
	return binPath
}

func harnessTool(path string) discovery.LLMTool {
	return discovery.LLMTool{
		Name:         "claude",
		Path:         path,
		Available:    true,
		Capabilities: discovery.CapabilitiesForTool("claude"),
	}
}

func TestLargePromptStdinTransport(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdin-echo")

	// Create a prompt larger than the threshold to trigger stdin transport
	largePrompt := strings.Repeat("A", promptSizeThreshold+1000)

	outputDir := t.TempDir()
	e := &Executor{Timeout: 5 * time.Second}
	if err := e.SetStagingDir(outputDir); err != nil {
		t.Fatalf("setting staging dir: %v", err)
	}
	defer e.Cleanup()

	resp, err := e.run(harnessTool(bin), largePrompt, "primary", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	// The harness echoes stdin back, so output should match the prompt
	if len(resp.Output) != len(largePrompt) {
		t.Fatalf("output length = %d, want %d (prompt delivered via stdin)", len(resp.Output), len(largePrompt))
	}
}

func TestSmallPromptDirectArg(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdout-only")

	// Small prompt should use direct arg, not stdin
	smallPrompt := "small prompt"

	e := &Executor{Timeout: time.Second}
	resp, err := e.run(harnessTool(bin), smallPrompt, "primary", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	// stdout-only behavior uses env var for output, not the prompt arg
	if len(resp.Output) < minOutputLen {
		t.Fatalf("output length = %d, want at least %d", len(resp.Output), minOutputLen)
	}
}

func TestLargePromptFileTransportToFile(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdin-to-file")

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "result.md")

	// Create a prompt larger than the threshold
	largePrompt := strings.Repeat("B", promptSizeThreshold+5000)

	e := &Executor{Timeout: 5 * time.Second}
	if err := e.SetStagingDir(outputDir); err != nil {
		t.Fatalf("setting staging dir: %v", err)
	}
	defer e.Cleanup()

	resp, err := e.runToFile(harnessTool(bin), largePrompt, outputPath, "primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	// The harness writes stdin to the output file. runToFileWithModel appends
	// a file-write instruction to the prompt, so the output file is larger
	// than the original prompt. Verify the original prompt is a prefix.
	content, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("reading output file: %v", readErr)
	}
	if len(content) < len(largePrompt) {
		t.Fatalf("output file length = %d, want at least %d", len(content), len(largePrompt))
	}
	if !strings.HasPrefix(string(content), largePrompt) {
		t.Fatal("output file does not start with the original prompt")
	}
}

func TestPromptFileCleanup(t *testing.T) {
	outputDir := t.TempDir()
	e := &Executor{Timeout: time.Second}
	if err := e.SetStagingDir(outputDir); err != nil {
		t.Fatalf("setting staging dir: %v", err)
	}

	// Write a prompt file
	path, err := e.writePromptFile("test prompt content")
	if err != nil {
		t.Fatalf("writing prompt file: %v", err)
	}

	// Verify it exists with correct permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("prompt file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("prompt file permissions = %o, want 0600", perm)
	}

	// Cleanup should remove the staging dir and prompt file
	e.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("prompt file still exists after Cleanup()")
	}
}

func TestBuildArgsStdinMode(t *testing.T) {
	t.Run("claude_stdin", func(t *testing.T) {
		args, err := buildArgs("claude", "big prompt", "", "", "", true, false)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-p -") {
			t.Fatalf("args = %q, want -p - for stdin mode", joined)
		}
		if strings.Contains(joined, "big prompt") {
			t.Fatalf("args = %q, should not contain prompt in stdin mode", joined)
		}
	})

	t.Run("codex_stdin", func(t *testing.T) {
		args, err := buildArgs("codex", "big prompt", "/tmp/work", "", "", true, false)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "big prompt") {
			t.Fatalf("args = %q, should not contain prompt in stdin mode", joined)
		}
	})

	t.Run("gemini_stdin", func(t *testing.T) {
		args, err := buildArgs("gemini", "big prompt", "", "", "", true, false)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-p -") {
			t.Fatalf("args = %q, want -p - for stdin mode", joined)
		}
		if strings.Contains(joined, "big prompt") {
			t.Fatalf("args = %q, should not contain prompt in stdin mode", joined)
		}
	})

	t.Run("claude_direct", func(t *testing.T) {
		args, err := buildArgs("claude", "small prompt", "", "", "", false, false)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "small prompt") {
			t.Fatalf("args = %q, want prompt in direct mode", joined)
		}
	})
}

// --- RunCritic tests ---

func TestRunCriticSuccess(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdout-only")

	e := &Executor{
		Critic:  harnessTool(bin),
		Timeout: 5 * time.Second,
	}
	resp, err := e.RunCritic("Review this output for quality.")
	if err != nil {
		t.Fatalf("RunCritic failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	if len(resp.Output) < minOutputLen {
		t.Fatalf("output length = %d, want at least %d", len(resp.Output), minOutputLen)
	}
	if resp.Tool != "claude" {
		t.Errorf("Tool = %q, want %q", resp.Tool, "claude")
	}
}

func TestRunCriticRejectsShortOutput(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdout-only")
	t.Setenv("SWARMAKER_TEST_STDOUT", "too short")

	e := &Executor{
		Critic:  harnessTool(bin),
		Timeout: 5 * time.Second,
	}
	_, err := e.RunCritic("prompt")
	if err == nil {
		t.Fatal("expected error for short output")
	}
	if !strings.Contains(err.Error(), "suspiciously short") {
		t.Fatalf("error = %v, want short-output failure", err)
	}
}

func TestRunCriticRejectsUnavailableTool(t *testing.T) {
	e := &Executor{
		Critic: discovery.LLMTool{
			Name:         "claude",
			Path:         "/nonexistent",
			Available:    false,
			Capabilities: discovery.CapabilitiesForTool("claude"),
		},
		Timeout: time.Second,
	}
	_, err := e.RunCritic("prompt")
	if err == nil {
		t.Fatal("expected error for unavailable tool")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v, want unavailable failure", err)
	}
}

// --- RunToolToFile tests ---

func TestRunToolToFileSuccess(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.md")

	tool := harnessTool(bin)
	e := &Executor{Timeout: 5 * time.Second}
	resp, err := e.RunToolToFile(tool, "", "Generate content", outputPath)
	if err != nil {
		t.Fatalf("RunToolToFile failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	if resp.OutputFile != outputPath {
		t.Errorf("OutputFile = %q, want %q", resp.OutputFile, outputPath)
	}
	content, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("reading output file: %v", readErr)
	}
	if len(content) < minOutputLen {
		t.Fatalf("output file length = %d, want at least %d", len(content), minOutputLen)
	}
	if resp.Output != strings.TrimSpace(string(content)) {
		t.Errorf("resp.Output does not match file content")
	}
}

func TestRunToolToFileWithModelOverride(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "write-file")

	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.md")

	tool := harnessTool(bin)
	e := &Executor{Timeout: 5 * time.Second}
	resp, err := e.RunToolToFile(tool, "haiku", "Generate content", outputPath)
	if err != nil {
		t.Fatalf("RunToolToFile with model override failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v, want nil", resp.Error)
	}
	if resp.OutputFile != outputPath {
		t.Errorf("OutputFile = %q, want %q", resp.OutputFile, outputPath)
	}
}

func TestRunToolToFileRejectsUnavailableTool(t *testing.T) {
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "output.md")

	tool := discovery.LLMTool{
		Name:         "claude",
		Path:         "/nonexistent",
		Available:    false,
		Capabilities: discovery.CapabilitiesForTool("claude"),
	}
	e := &Executor{Timeout: time.Second}
	_, err := e.RunToolToFile(tool, "", "prompt", outputPath)
	if err == nil {
		t.Fatal("expected error for unavailable tool")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v, want unavailable failure", err)
	}
}

// --- Cleanup tests ---

func TestCleanupRemovesStagingDir(t *testing.T) {
	baseDir := t.TempDir()
	e := &Executor{Timeout: time.Second}
	if err := e.SetStagingDir(baseDir); err != nil {
		t.Fatalf("SetStagingDir failed: %v", err)
	}
	stagingDir := e.StagingDir

	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("staging dir should exist: %v", err)
	}

	contextPath, err := e.WriteContextFile("test.txt", "test content")
	if err != nil {
		t.Fatalf("writing context file: %v", err)
	}
	if _, err := os.Stat(contextPath); err != nil {
		t.Fatalf("context file should exist: %v", err)
	}

	e.Cleanup()

	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir should be removed after Cleanup, got: %v", err)
	}
	if _, err := os.Stat(contextPath); !os.IsNotExist(err) {
		t.Fatalf("context file should be removed after Cleanup, got: %v", err)
	}
}

func TestCleanupSafeWithEmptyStagingDir(t *testing.T) {
	e := &Executor{StagingDir: ""}
	e.Cleanup() // should not panic
}

func TestCleanupSafeWithDotStagingDir(t *testing.T) {
	e := &Executor{StagingDir: "."}
	e.Cleanup() // should not remove cwd
	if _, err := os.Stat("."); err != nil {
		t.Fatal("current directory should still exist")
	}
}

// --- validateOutput tests ---

func TestValidateOutputAcceptsValidContent(t *testing.T) {
	content := strings.Repeat("This is a valid output with enough content. ", 10)
	if err := validateOutput(content, "claude"); err != nil {
		t.Fatalf("validateOutput rejected valid content: %v", err)
	}
}

func TestValidateOutputRejectsTooShort(t *testing.T) {
	err := validateOutput("short", "claude")
	if err == nil {
		t.Fatal("expected error for short output")
	}
	if !strings.Contains(err.Error(), "suspiciously short") {
		t.Fatalf("error = %v, want short-output error", err)
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("error should mention tool name, got: %v", err)
	}
}

func TestValidateOutputRejectsEmptyString(t *testing.T) {
	err := validateOutput("", "codex")
	if err == nil {
		t.Fatal("expected error for empty output")
	}
	if !strings.Contains(err.Error(), "suspiciously short") {
		t.Fatalf("error = %v, want short-output error", err)
	}
}

func TestValidateOutputRejectsRefusalInShortOutput(t *testing.T) {
	// Pad to exactly minOutputLen to pass length check but stay under 500 for refusal detection
	refusal := "I cannot help with that request."
	padded := refusal + strings.Repeat("x", minOutputLen-len(refusal))
	err := validateOutput(padded, "gemini")
	if err == nil {
		t.Fatal("expected error for refusal in short output")
	}
	if !strings.Contains(err.Error(), "refusal") {
		t.Fatalf("error = %v, want refusal error", err)
	}
}

func TestValidateOutputAllowsRefusalPatternInLongOutput(t *testing.T) {
	content := strings.Repeat("Documentation about handling error: cases in production systems. ", 20)
	if err := validateOutput(content, "claude"); err != nil {
		t.Fatalf("validateOutput rejected long output with incidental refusal pattern: %v", err)
	}
}

func TestValidateOutputRejectsRateLimitShortOutput(t *testing.T) {
	// Must be >= minOutputLen to pass length check, and < 500 to trigger refusal detection
	prefix := "Rate limit exceeded. Please wait. "
	pad := minOutputLen - len(prefix)
	if pad < 0 {
		pad = 0
	}
	msg := prefix + strings.Repeat("x", pad)
	err := validateOutput(msg, "codex")
	if err == nil {
		t.Fatal("expected error for rate limit message")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("error = %v, want rate limit detection", err)
	}
}

// --- SetStagingDir / WriteContextFile tests ---

func TestSetStagingDirAndWriteContextFile(t *testing.T) {
	baseDir := t.TempDir()
	e := &Executor{}
	if err := e.SetStagingDir(baseDir); err != nil {
		t.Fatalf("SetStagingDir failed: %v", err)
	}

	expectedDir := filepath.Join(baseDir, ".staging")
	if e.StagingDir != expectedDir {
		t.Errorf("StagingDir = %q, want %q", e.StagingDir, expectedDir)
	}

	path, err := e.WriteContextFile("context.md", "# Context\nProject info here.\n")
	if err != nil {
		t.Fatalf("WriteContextFile failed: %v", err)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading context file: %v", readErr)
	}
	if string(content) != "# Context\nProject info here.\n" {
		t.Errorf("context file content = %q, want exact content", string(content))
	}
}

// --- SetManagedOutputFiles tests ---

func TestSetManagedOutputFilesNormalizesAndDeduplicates(t *testing.T) {
	e := &Executor{}
	e.SetManagedOutputFiles([]string{
		"/tmp/output/a.md",
		" /tmp/output/b.md ",
		"/tmp/output/a.md", // duplicate
		"",                 // empty - should be skipped
		"  ",               // whitespace - should be skipped
	})
	defer e.ClearManagedOutputFiles()

	snap := e.managedOutputSnapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 managed files, got %d: %v", len(snap), snap)
	}
	if _, ok := snap[filepath.Clean("/tmp/output/a.md")]; !ok {
		t.Error("expected a.md in managed set")
	}
	if _, ok := snap[filepath.Clean("/tmp/output/b.md")]; !ok {
		t.Error("expected b.md in managed set")
	}
}

func TestClearManagedOutputFiles(t *testing.T) {
	e := &Executor{}
	e.SetManagedOutputFiles([]string{"/tmp/a.md"})
	e.ClearManagedOutputFiles()

	snap := e.managedOutputSnapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty managed set after clear, got %d", len(snap))
	}
}

func TestSandboxFallbackDetection(t *testing.T) {
	e := &Executor{}
	// bwrap is unlikely to be available in CI/test environments
	available := e.CodexSandboxAvailable()

	// Verify caching: calling again returns the same result
	if got := e.CodexSandboxAvailable(); got != available {
		t.Fatalf("CodexSandboxAvailable() not cached: first=%v, second=%v", available, got)
	}

	// Verify buildArgs uses the correct flag based on sandbox availability
	t.Run("sandbox_available", func(t *testing.T) {
		args, err := buildArgs("codex", "prompt", "/tmp", "", "", false, true)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-s read-only") {
			t.Fatalf("args = %q, want -s read-only when sandbox available", joined)
		}
		if strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("args = %q, should not bypass when sandbox available", joined)
		}
	})

	t.Run("sandbox_unavailable", func(t *testing.T) {
		args, err := buildArgs("codex", "prompt", "/tmp", "", "", false, false)
		if err != nil {
			t.Fatalf("buildArgs error: %v", err)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "-s read-only") {
			t.Fatalf("args = %q, should not use sandbox when unavailable", joined)
		}
		if !strings.Contains(joined, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatalf("args = %q, want bypass when sandbox unavailable", joined)
		}
	})
}

func TestBuildArgsOllama(t *testing.T) {
	args, err := buildArgs("ollama", "test prompt", "/tmp/work", "llama3.1", "", false, false)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if args[0] != "run" {
		t.Errorf("expected 'run', got %q", args[0])
	}
	if args[1] != "llama3.1" {
		t.Errorf("expected model 'llama3.1', got %q", args[1])
	}
	if args[2] != "--nowordwrap" {
		t.Errorf("expected '--nowordwrap', got %q", args[2])
	}
}

func TestBuildArgsOllamaDefaultModel(t *testing.T) {
	args, err := buildArgs("ollama", "test prompt", "/tmp/work", "", "", false, false)
	if err != nil {
		t.Fatalf("buildArgs failed: %v", err)
	}
	if args[1] != "llama3.1" {
		t.Errorf("expected default model 'llama3.1', got %q", args[1])
	}
}

func TestBuildArgsReturnsErrorForUnknownProvider(t *testing.T) {
	_, err := buildArgs("unknown-provider", "prompt", "", "", "", false, false)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("error = %q, want 'unsupported provider'", err.Error())
	}
}

func TestExecutorErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		wantCode  string
		wantRetry bool
	}{
		{
			name:      "short output",
			output:    "short",
			wantCode:  "SHORT_OUTPUT",
			wantRetry: true,
		},
		{
			name:      "rate limit",
			output:    "Rate limit exceeded. Please wait." + strings.Repeat("x", minOutputLen-len("Rate limit exceeded. Please wait.")),
			wantCode:  "RATE_LIMIT",
			wantRetry: true,
		},
		{
			name:      "refusal",
			output:    "I cannot help with that request." + strings.Repeat("x", minOutputLen-len("I cannot help with that request.")),
			wantCode:  "REFUSAL",
			wantRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutput(tt.output, "claude")
			if err == nil {
				t.Fatal("expected error")
			}
			execErr, ok := err.(*ExecutorError)
			if !ok {
				t.Fatalf("expected *ExecutorError, got %T", err)
			}
			if execErr.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", execErr.Code, tt.wantCode)
			}
			if execErr.Retryable != tt.wantRetry {
				t.Errorf("Retryable = %v, want %v", execErr.Retryable, tt.wantRetry)
			}
			if execErr.Guidance == "" {
				t.Error("Guidance should not be empty")
			}
		})
	}
}

func TestExecutorErrorTimeout(t *testing.T) {
	err := classifyTimeout("claude", 10*time.Minute, "/tmp/output.md", "some output")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Code != "TIMEOUT" {
		t.Errorf("Code = %q, want TIMEOUT", err.Code)
	}
	if !err.Retryable {
		t.Error("timeout should be retryable")
	}
	if err.Guidance == "" {
		t.Error("Guidance should not be empty")
	}
}

func TestResponseTokenEstimation(t *testing.T) {
	resp := &Response{
		Tool:   "claude",
		Prompt: strings.Repeat("x", 400), // 400 chars = ~100 tokens
		Output: strings.Repeat("y", 200), // 200 chars = ~50 tokens
	}
	resp.InputTokens = len(resp.Prompt) / 4
	resp.OutputTokens = len(resp.Output) / 4

	if resp.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.InputTokens)
	}
	if resp.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", resp.OutputTokens)
	}
}

func TestPromptSizeLimit(t *testing.T) {
	bin := buildHarnessBinary(t)
	t.Setenv("SWARMAKER_TEST_BEHAVIOR", "stdout-only")

	e := &Executor{Timeout: time.Second}

	// Prompt under limit should work
	normalPrompt := strings.Repeat("x", maxPromptChars-1)
	resp, err := e.run(harnessTool(bin), normalPrompt, "primary", "")
	if err != nil {
		t.Fatalf("normal prompt should succeed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("response error = %v for normal prompt", resp.Error)
	}

	// Prompt over limit should fail immediately
	hugePrompt := strings.Repeat("x", maxPromptChars+1)
	resp, err = e.run(harnessTool(bin), hugePrompt, "primary", "")
	if err == nil {
		t.Fatal("expected error for oversized prompt")
	}
	if !strings.Contains(err.Error(), "prompt exceeds maximum size") {
		t.Fatalf("error = %v, want prompt size limit error", err)
	}
}
