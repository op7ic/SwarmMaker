// executor.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// LLM CLI subprocess executor.
// Manages the lifecycle of LLM CLI invocations: argument construction for
// each provider (claude/codex/gemini), stdin-based prompt transport for large
// prompts (>100KB), retry with exponential backoff, timeout classification,
// workspace write validation, managed output file tracking, and bubblewrap
// sandbox detection with graceful fallback. This is the boundary between
// SwarmMaker and the external LLM CLI processes.


package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/op7ic/swarmmaker/internal/discovery"
)

const (
	defaultTimeout          = 10 * time.Minute
	defaultProgressAfter    = 20 * time.Second
	defaultProgressInterval = 30 * time.Second
	maxRetries              = 3
	minOutputLen            = 200 // minimum acceptable response length
	promptSizeThreshold     = 100_000 // bytes; Linux MAX_ARG_STRLEN is 131072
	maxPromptChars          = 400_000 // ~100K tokens; conservative limit for all providers
)

// ModelOverrides allows callers to specify lighter/faster models per tool.
// Keys are tool names ("claude", "codex", "gemini"), values are model identifiers.
type ModelOverrides map[string]string

// Executor shells out to LLM CLI tools and captures their output.
// Prompts are kept small (instructions only). Context data is written to
// a staging directory that the LLM CLI tools can read directly.
type Executor struct {
	Primary          discovery.LLMTool
	Critic           discovery.LLMTool
	Verbose          bool
	Timeout          time.Duration
	ProgressAfter    time.Duration
	ProgressInterval time.Duration
	StagingDir       string          // directory where context files are written for LLM access
	Models           ModelOverrides  // optional model overrides per tool (e.g. "claude" -> "haiku")
	Ctx              context.Context // parent context for cancellation (signal handling)
	managedMu        sync.RWMutex
	managedOut       map[string]struct{}

	sandboxOnce      sync.Once
	sandboxAvailable bool
}

// ExecutorError classifies LLM invocation failures with a retryable flag
// so the retry loop can make informed decisions instead of retrying blindly.
type ExecutorError struct {
	Code      string // TIMEOUT, REFUSAL, RATE_LIMIT, SHORT_OUTPUT, UNKNOWN
	Message   string
	Retryable bool
	Guidance  string // what the caller should do differently
}

func (e *ExecutorError) Error() string {
	return e.Message
}

// Response holds the output from an LLM invocation.
type Response struct {
	Tool            string
	Prompt          string
	Output          string
	Duration        time.Duration
	Retries         int
	FallbackCount   int
	FallbackReasons []string
	Error           error
	OutputFile      string // if the LLM wrote to a file, this is its path
	InputTokens     int    // estimated from prompt length
	OutputTokens    int    // estimated from output length
}

type fileSnapshot struct {
	ModTime time.Time
	Size    int64
}

// New creates a new Executor. Call SetStagingDir before use to place staging
// files where LLM CLIs can read them (e.g. inside the output directory).
func New(primary, critic discovery.LLMTool, verbose bool) *Executor {
	return &Executor{
		Primary: primary,
		Critic:  critic,
		Verbose: verbose,
		Timeout: defaultTimeout,
	}
}

// CodexSandboxAvailable returns whether bubblewrap (bwrap) is available for
// sandboxed codex execution. The result is cached per Executor instance.
func (e *Executor) CodexSandboxAvailable() bool {
	e.sandboxOnce.Do(func() {
		cmd := exec.Command("bwrap", "--version")
		err := cmd.Run()
		e.sandboxAvailable = err == nil
	})
	return e.sandboxAvailable
}

func (e *Executor) progressAfter() time.Duration {
	if e.ProgressAfter > 0 {
		return e.ProgressAfter
	}
	return defaultProgressAfter
}

func (e *Executor) progressInterval() time.Duration {
	if e.ProgressInterval > 0 {
		return e.ProgressInterval
	}
	return defaultProgressInterval
}

// SetStagingDir creates a .staging/ subdirectory inside baseDir for context files.
// LLM CLIs need filesystem access to read these, so baseDir should be a project
// directory the CLIs are allowed to access (not /tmp/).
func (e *Executor) SetStagingDir(baseDir string) error {
	staging := filepath.Join(baseDir, ".staging")
	if err := os.MkdirAll(staging, 0755); err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}
	e.StagingDir = staging
	return nil
}

// Cleanup removes the staging directory.
func (e *Executor) Cleanup() {
	if e.StagingDir != "" && e.StagingDir != "." {
		_ = os.RemoveAll(e.StagingDir)
	}
}

// SetManagedOutputFiles records the output files a concurrent phase is expected
// to produce so per-task workspace guards do not treat sibling task outputs as
// unexpected writes.
func (e *Executor) SetManagedOutputFiles(paths []string) {
	e.managedMu.Lock()
	defer e.managedMu.Unlock()
	if len(paths) == 0 {
		e.managedOut = nil
		return
	}
	e.managedOut = make(map[string]struct{}, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(strings.TrimSpace(path))
		if clean == "." || clean == "" {
			continue
		}
		e.managedOut[clean] = struct{}{}
	}
}

func (e *Executor) ClearManagedOutputFiles() {
	e.SetManagedOutputFiles(nil)
}

// WriteContextFile writes content to the staging directory and returns the path.
// LLM CLIs will be instructed to read this file rather than receiving it inline.
func (e *Executor) WriteContextFile(name, content string) (string, error) {
	path := filepath.Join(e.StagingDir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing context file %s: %w", name, err)
	}
	return path, nil
}

// writePromptFile writes a large prompt to a temp file in StagingDir with
// restrictive permissions. Returns the file path. The file is cleaned up
// by Cleanup() along with the rest of the staging directory.
func (e *Executor) writePromptFile(prompt string) (string, error) {
	if e.StagingDir == "" {
		return "", fmt.Errorf("staging dir not set; cannot write prompt file")
	}
	f, err := os.CreateTemp(e.StagingDir, "prompt-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating prompt temp file: %w", err)
	}
	path := f.Name()
	if err := f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("setting prompt file permissions: %w", err)
	}
	if _, err := f.WriteString(prompt); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("writing prompt to temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("closing prompt temp file: %w", err)
	}
	return path, nil
}

// RunCritic sends a prompt to the critic LLM with retry.
func (e *Executor) RunCritic(prompt string) (*Response, error) {
	return e.runWithRetry(e.Critic, prompt, "critic")
}

// RunPreFlight sends a short prompt to the primary LLM with a 30-second
// timeout and no minimum output length validation. Used for lightweight
// pre-flight checks (e.g., input quality gate) where the expected response
// is a single verdict line.
func (e *Executor) RunPreFlight(prompt string) (*Response, error) {
	if err := e.validateTool(e.Primary); err != nil {
		return &Response{Tool: e.Primary.Name, Prompt: prompt, Error: err}, err
	}
	origTimeout := e.Timeout
	e.Timeout = 30 * time.Second
	defer func() { e.Timeout = origTimeout }()

	resp, err := e.run(e.Primary, prompt, "preflight", "")
	if err != nil {
		return resp, err
	}
	// Skip validateOutput -- pre-flight responses are intentionally short.
	return resp, nil
}

// RunPrimary sends a prompt to the primary LLM and returns the response text.
// Unlike RunPrimaryToFile, this does not instruct the LLM to write to a file.
func (e *Executor) RunPrimary(prompt string) (*Response, error) {
	return e.runWithRetry(e.Primary, prompt, "primary")
}

// RunPrimaryToFile runs the primary LLM, instructing it to write output to outputFile.
// After execution, reads the file. Falls back to stdout if file wasn't created.
func (e *Executor) RunPrimaryToFile(prompt, outputFile string) (*Response, error) {
	return e.runToFile(e.Primary, prompt, outputFile, "primary")
}

// RunToolToFile runs a specific tool with an optional model override.
// Used by the swarm to distribute work across available tools.
func (e *Executor) RunToolToFile(tool discovery.LLMTool, model, prompt, outputFile string) (*Response, error) {
	return e.runToFileWithModel(tool, prompt, outputFile, tool.Name, model)
}

func (e *Executor) runToFile(tool discovery.LLMTool, prompt, outputFile, role string) (*Response, error) {
	return e.runToFileWithModel(tool, prompt, outputFile, role, "")
}

func (e *Executor) runToFileWithModel(tool discovery.LLMTool, prompt, outputFile, role, modelOverride string) (*Response, error) {
	if err := e.validateTool(tool); err != nil {
		return &Response{Tool: tool.Name, Prompt: prompt, Error: err}, err
	}

	filePrompt := prompt
	if !supportsNativeOutputCapture(tool.Name) {
		filePrompt += fmt.Sprintf("\n\n---\nIMPORTANT: Write your COMPLETE output to this file: %s\n"+
			"Create the file and write the full content there. This is mandatory.\n"+
			"Do NOT just print to stdout — the file must exist after you finish.\n", outputFile)
	}

	var lastErr error
	var lastResp *Response
	backoff := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if e.Verbose {
				fmt.Printf("  [%s] Retry %d/%d after %v...\n", tool.Name, attempt, maxRetries, backoff)
			}
			time.Sleep(backoff)
			backoff *= 2
		}

		if err := os.Remove(outputFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			lastErr = fmt.Errorf("remove stale output file %q: %w", outputFile, err)
			continue
		}

		workspaceRoot := e.workspaceRoot()
		before, err := snapshotFiles(workspaceRoot)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := e.runWithModel(tool, filePrompt, role, outputFile, modelOverride)
		lastResp = resp
		if err != nil {
			lastErr = err
			continue
		}

		if err := e.validateWorkspaceWrites(workspaceRoot, before, outputFile); err != nil {
			resp.Error = err
			if e.Verbose {
				fmt.Printf("  [%s] Workspace write validation failed: %v\n", tool.Name, err)
			}
			return resp, err
		}

		// Check if the LLM wrote the file (primary success signal)
		if content, readErr := os.ReadFile(outputFile); readErr == nil && len(content) > minOutputLen {
			resp.Output = strings.TrimSpace(string(content))
			resp.OutputFile = outputFile
			resp.Retries = attempt
			resp.InputTokens = len(filePrompt) / 4
			resp.OutputTokens = len(resp.Output) / 4
			return resp, nil
		}

		reason := "output file missing or too short"
		if content, readErr := os.ReadFile(outputFile); readErr == nil {
			reason = fmt.Sprintf("output file too short (%d chars)", len(content))
		}
		resp.FallbackCount++
		resp.FallbackReasons = append(resp.FallbackReasons, reason)
		resp.Error = fmt.Errorf("required output file %q was not produced: %s", outputFile, reason)
		lastErr = resp.Error
		if e.Verbose {
			fmt.Printf("  [%s] Required output file invalid: %s\n", tool.Name, reason)
		}
	}

	if lastResp != nil && lastResp.Error == nil {
		lastResp.Error = lastErr
	}
	return lastResp, fmt.Errorf("%s file-write failed after %d retries: %w", tool.Name, maxRetries, lastErr)
}

func (e *Executor) workspaceRoot() string {
	if e.StagingDir == "" {
		return ""
	}
	return filepath.Dir(e.StagingDir)
}

func (e *Executor) validateWorkspaceWrites(workspaceRoot string, before map[string]fileSnapshot, allowedOutputFile string) error {
	if workspaceRoot == "" {
		return nil
	}
	after, err := snapshotFiles(workspaceRoot)
	if err != nil {
		return err
	}
	allowed := e.managedOutputSnapshot()
	if strings.TrimSpace(allowedOutputFile) != "" {
		allowed[filepath.Clean(allowedOutputFile)] = struct{}{}
	}
	var unexpected []string
	for path, afterState := range after {
		beforeState, existed := before[path]
		if existed && beforeState.ModTime.Equal(afterState.ModTime) && beforeState.Size == afterState.Size {
			continue
		}
		if _, ok := allowed[path]; ok {
			continue
		}
		unexpected = append(unexpected, path)
	}
	for path := range before {
		if _, ok := after[path]; ok {
			continue
		}
		if _, ok := allowed[path]; ok {
			continue
		}
		unexpected = append(unexpected, path)
	}
	if len(unexpected) == 0 {
		return nil
	}
	return fmt.Errorf("provider wrote unexpected workspace files outside target %q: %s", allowedOutputFile, strings.Join(unexpected, ", "))
}

func (e *Executor) managedOutputSnapshot() map[string]struct{} {
	e.managedMu.RLock()
	defer e.managedMu.RUnlock()
	out := make(map[string]struct{}, len(e.managedOut))
	for path := range e.managedOut {
		out[path] = struct{}{}
	}
	return out
}

func snapshotFiles(root string) (map[string]fileSnapshot, error) {
	if strings.TrimSpace(root) == "" {
		return map[string]fileSnapshot{}, nil
	}
	snapshots := make(map[string]fileSnapshot)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		snapshots[filepath.Clean(path)] = fileSnapshot{
			ModTime: info.ModTime(),
			Size:    info.Size(),
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("snapshot workspace files under %s: %w", root, err)
	}
	return snapshots, nil
}

func (e *Executor) runWithRetry(tool discovery.LLMTool, prompt, role string) (*Response, error) {
	if err := e.validateTool(tool); err != nil {
		return &Response{Tool: tool.Name, Prompt: prompt, Error: err}, err
	}

	var lastErr error
	var lastResp *Response
	backoff := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if e.Verbose {
				fmt.Printf("  [%s] Retry %d/%d after %v...\n", tool.Name, attempt, maxRetries, backoff)
			}
			time.Sleep(backoff)
			backoff *= 2
		}

		resp, err := e.run(tool, prompt, role, "")
		lastResp = resp
		if err == nil {
			resp.InputTokens = len(prompt) / 4
			resp.OutputTokens = len(resp.Output) / 4
			if err := validateOutput(resp.Output, tool.Name); err != nil {
				resp.Error = err
				lastErr = err
				if e.Verbose {
					fmt.Printf("  [%s] Output validation failed: %v\n", tool.Name, err)
				}
				// Stop retrying non-retryable errors
				if execErr, ok := err.(*ExecutorError); ok && !execErr.Retryable {
					break
				}
				continue
			}
			resp.Retries = attempt
			return resp, nil
		}
		lastErr = err
		// Stop retrying non-retryable errors
		if execErr, ok := err.(*ExecutorError); ok && !execErr.Retryable {
			break
		}
	}

	if lastResp != nil && lastResp.Error == nil {
		lastResp.Error = lastErr
	}
	return lastResp, fmt.Errorf("%s failed after %d retries: %w", tool.Name, maxRetries, lastErr)
}

func (e *Executor) run(tool discovery.LLMTool, prompt, role, outputFile string) (*Response, error) {
	return e.runWithModel(tool, prompt, role, outputFile, "")
}

func (e *Executor) runWithModel(tool discovery.LLMTool, prompt, role, outputFile, modelOverride string) (*Response, error) {
	if len(prompt) > maxPromptChars {
		err := fmt.Errorf("prompt exceeds maximum size (%d chars > %d max). Reduce source material or split into smaller runs", len(prompt), maxPromptChars)
		return &Response{Tool: tool.Name, Prompt: prompt, Error: err}, err
	}

	if e.Verbose {
		fmt.Printf("  [%s] Sending prompt (%d chars)...\n", tool.Name, len(prompt))
	}

	start := time.Now()
	// Pass the staging dir's parent as workDir so the LLM can read files there
	workDir := ""
	if e.StagingDir != "" {
		workDir = filepath.Dir(e.StagingDir)
	}
	model := modelOverride
	if model == "" && e.Models != nil {
		model = e.Models[tool.Name]
	}

	// When the prompt exceeds the size threshold, deliver it via stdin
	// instead of as a CLI argument to avoid Linux E2BIG (MAX_ARG_STRLEN = 131072).
	useStdin := len(prompt) > promptSizeThreshold
	sandboxOK := tool.Name == "codex" && e.CodexSandboxAvailable()
	if tool.Name == "codex" && !sandboxOK {
		fmt.Fprintf(os.Stderr, "WARNING: bubblewrap sandbox unavailable; using --dangerously-bypass-approvals-and-sandbox\n")
	}
	args, err := buildArgs(tool.Name, prompt, workDir, model, outputFile, useStdin, sandboxOK)
	if err != nil {
		resp := &Response{
			Tool:     tool.Name,
			Prompt:   prompt,
			Duration: time.Since(start),
			Error:    fmt.Errorf("building CLI arguments: %w", err),
		}
		return resp, resp.Error
	}

	parentCtx := e.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, e.Timeout)
	defer cancel()

	// Resolve binary (claude needs ld-linux on Linux)
	binPath, cmdArgs := resolveBinary(tool.Name, tool.Path, args)
	cmd := exec.CommandContext(ctx, binPath, cmdArgs...)
	cmd.Env = buildEnv(tool, role, model, outputFile)

	if useStdin {
		// Pipe the prompt via stdin to avoid argument length limits
		cmd.Stdin = strings.NewReader(prompt)
		if e.Verbose {
			fmt.Printf("  [%s] Prompt exceeds %d bytes, delivering via stdin\n", tool.Name, promptSizeThreshold)
		}
	} else {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			resp := &Response{
				Tool:     tool.Name,
				Prompt:   prompt,
				Duration: time.Since(start),
				Error:    fmt.Errorf("open %s for stdin suppression: %w", os.DevNull, err),
			}
			return resp, resp.Error
		}
		defer devNull.Close()
		cmd.Stdin = devNull
	}

	// Set working directory so all LLM CLIs can find relative paths
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		resp := &Response{
			Tool:     tool.Name,
			Prompt:   prompt,
			Duration: time.Since(start),
			Error:    fmt.Errorf("%s failed to start: %w", tool.Name, err),
		}
		return resp, resp.Error
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	progressDelay := e.progressAfter()
	progressInterval := e.progressInterval()
	var progressTimer *time.Timer
	var progressCh <-chan time.Time
	if progressDelay > 0 {
		progressTimer = time.NewTimer(progressDelay)
		progressCh = progressTimer.C
		defer progressTimer.Stop()
	}

	for {
		select {
		case err := <-waitCh:
			duration := time.Since(start)

			resp := &Response{
				Tool:     tool.Name,
				Prompt:   prompt,
				Duration: duration,
			}
			combined := strings.TrimSpace(stdout.String() + stderr.String())

			if ctxErr := ctx.Err(); ctxErr != nil {
				resp.Output = combined
				if ctxErr == context.DeadlineExceeded {
					resp.Error = classifyTimeout(tool.Name, e.Timeout, outputFile, combined)
				} else {
					resp.Error = fmt.Errorf("%s cancelled: %w", tool.Name, ctxErr)
				}
				return resp, resp.Error
			}

			if err != nil {
				if combined != "" {
					resp.Output = combined
					resp.Error = fmt.Errorf("%s exited with error: %w", tool.Name, err)
				} else {
					resp.Error = fmt.Errorf("%s failed: %w", tool.Name, err)
				}
				return resp, resp.Error
			}

			resp.Output = strings.TrimSpace(stdout.String())
			resp.InputTokens = len(prompt) / 4
			resp.OutputTokens = len(resp.Output) / 4

			if e.Verbose {
				fmt.Printf("  [%s] Response received (%d chars, %v)\n",
					tool.Name, len(resp.Output), duration.Round(time.Millisecond))
			}

			return resp, nil
		case <-progressCh:
			fmt.Printf("  [%s] Still running after %v (%s)\n",
				tool.Name,
				time.Since(start).Round(time.Second),
				describeOutputFileState(outputFile),
			)
			if progressInterval > 0 {
				progressTimer.Reset(progressInterval)
				progressCh = progressTimer.C
			} else {
				progressCh = nil
			}
		}
	}
}

func (e *Executor) validateTool(tool discovery.LLMTool) error {
	if err := tool.ValidateMetadata(); err != nil {
		return err
	}
	if !tool.Available {
		return fmt.Errorf("provider %q is unavailable", tool.Name)
	}
	if !discovery.IsKnownToolName(tool.Name) {
		return fmt.Errorf("provider %q is not a supported CLI", tool.Name)
	}
	if strings.TrimSpace(tool.Path) == "" {
		return fmt.Errorf("provider %q has no binary path", tool.Name)
	}
	if _, err := os.Stat(tool.Path); err != nil {
		return fmt.Errorf("provider %q binary %q is not accessible: %w", tool.Name, tool.Path, err)
	}
	return nil
}

func buildEnv(tool discovery.LLMTool, role, model, outputFile string) []string {
	env := os.Environ()
	env = append(env,
		"SWARMAKER_PROVIDER="+tool.Name,
		"SWARMAKER_ROLE="+role,
	)
	if model != "" {
		env = append(env, "SWARMAKER_MODEL="+model)
	}
	if outputFile != "" {
		env = append(env, "SWARMAKER_OUTPUT_FILE="+outputFile)
	}
	return env
}

// validateOutput checks that LLM output meets minimum quality thresholds.
// Returns an *ExecutorError with classification and retryable flag.
func validateOutput(output, toolName string) error {
	if len(output) < minOutputLen {
		return &ExecutorError{
			Code:      "SHORT_OUTPUT",
			Message:   fmt.Sprintf("%s returned suspiciously short output (%d chars, minimum %d)", toolName, len(output), minOutputLen),
			Retryable: true,
			Guidance:  "LLM may have truncated; retry",
		}
	}
	lower := strings.ToLower(output)

	rateLimitPatterns := []string{"rate limit", "quota exceeded"}
	for _, pattern := range rateLimitPatterns {
		if strings.Contains(lower, pattern) && len(output) < 500 {
			return &ExecutorError{
				Code:      "RATE_LIMIT",
				Message:   fmt.Sprintf("%s returned rate limit error: %.100s...", toolName, output),
				Retryable: true,
				Guidance:  "wait and retry with backoff",
			}
		}
	}

	refusalPatterns := []string{
		"i cannot", "i'm unable", "i am unable",
		"as an ai", "i don't have access",
	}
	for _, pattern := range refusalPatterns {
		if strings.Contains(lower, pattern) && len(output) < 500 {
			return &ExecutorError{
				Code:      "REFUSAL",
				Message:   fmt.Sprintf("%s appears to have returned a refusal: %.100s...", toolName, output),
				Retryable: false,
				Guidance:  "rephrase prompt to avoid safety filters",
			}
		}
	}

	if strings.Contains(lower, "error:") && len(output) < 500 {
		return &ExecutorError{
			Code:      "UNKNOWN",
			Message:   fmt.Sprintf("%s appears to have returned an error: %.100s...", toolName, output),
			Retryable: true,
			Guidance:  "check provider status and retry",
		}
	}
	return nil
}

// resolveBinary handles platform-specific binary invocation quirks.
// Claude CLI (built with Bun) requires invocation via the dynamic linker
// on Linux due to a loader bug.
func resolveBinary(toolName, toolPath string, args []string) (string, []string) {
	if toolName == "claude" && runtime.GOOS == "linux" {
		resolved, err := filepath.EvalSymlinks(toolPath)
		if err != nil {
			resolved = toolPath
		}
		ldLinux := "/lib64/ld-linux-x86-64.so.2"
		if _, err := os.Stat(ldLinux); err == nil {
			return ldLinux, append([]string{resolved}, args...)
		}
	}
	return toolPath, args
}

// buildArgs constructs CLI arguments for each LLM tool.
// Providers that support native output capture use it; the rest rely on the
// prompt-level file-write contract. model is optional — if empty, the tool's
// default model is used.
//
// When useStdin is true, the prompt is delivered via stdin instead of as a CLI
// argument. This avoids hitting Linux's MAX_ARG_STRLEN limit (131072 bytes)
// on large prompts (e.g. adversarial review with all generated files).
//
// sandboxOK indicates whether bubblewrap is available for codex sandboxing.
// When true, codex uses -s read-only; when false, it falls back to
// --dangerously-bypass-approvals-and-sandbox.
func buildArgs(toolName, prompt, workDir, model, outputFile string, useStdin, sandboxOK bool) ([]string, error) {
	switch toolName {
	case "claude":
		// -p            : print mode (non-interactive, exit after response)
		// -p -          : read prompt from stdin
		// --dangerously-skip-permissions : allow reads/writes to any path without prompts
		// --output-format text : plain text stdout
		// --model        : override model (e.g. "haiku", "sonnet", "opus")
		var args []string
		if useStdin {
			args = []string{"-p", "-", "--output-format", "text", "--dangerously-skip-permissions"}
		} else {
			args = []string{"-p", prompt, "--output-format", "text", "--dangerously-skip-permissions"}
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		return args, nil
	case "codex":
		// exec                                              : non-interactive mode
		// -m <model>                                        : model override (e.g. "o4-mini")
		// -o <file>                                         : write the final assistant message directly to file
		// -c model_reasoning_effort="medium"                : avoid xhigh (default) which causes
		//                                                     multi-minute agent loops with shell commands
		// -s read-only                                      : bubblewrap sandbox (preferred when bwrap available)
		// --dangerously-bypass-approvals-and-sandbox         : fallback when bwrap unavailable (WSL, containers)
		// --skip-git-repo-check                             : allow outside git repos
		// -C <dir>                                          : working root directory
		args := []string{"exec"}
		// Override reasoning effort to medium. The codex default (xhigh) triggers
		// multi-agent loops with shell commands that take 5-30+ minutes per task.
		// Medium produces equivalent quality output in ~20 seconds.
		args = append(args, "-c", `model_reasoning_effort="medium"`)
		if model != "" {
			args = append(args, "-m", model)
		}
		if outputFile != "" {
			args = append(args, "-o", outputFile)
		}
		if sandboxOK {
			args = append(args, "-s", "read-only", "--skip-git-repo-check")
		} else {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check")
		}
		if workDir != "" {
			args = append(args, "-C", workDir)
		}
		if !useStdin {
			args = append(args, prompt)
		}
		return args, nil
	case "gemini":
		// gemini CLI: --model for model override
		var args []string
		if useStdin {
			args = []string{"-p", "-"}
		} else {
			args = []string{"-p", prompt}
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", toolName)
	}
}

func supportsNativeOutputCapture(toolName string) bool {
	switch toolName {
	case "codex":
		return true
	default:
		return false
	}
}

func describeOutputFileState(outputFile string) string {
	if strings.TrimSpace(outputFile) == "" {
		return "no output-file contract"
	}
	info, err := os.Stat(outputFile)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("output file %s not created yet", outputFile)
	}
	if err != nil {
		return fmt.Sprintf("output file %s not readable yet: %v", outputFile, err)
	}
	return fmt.Sprintf("output file %s currently %d bytes", outputFile, info.Size())
}

func classifyTimeout(toolName string, timeout time.Duration, outputFile, combined string) *ExecutorError {
	outputState := describeOutputFileState(outputFile)
	snippet := timeoutSnippet(combined)
	var msg string
	if toolName == "codex" && containsInteractiveInputWait(combined) {
		msg = fmt.Sprintf("%s timed out after %v while still waiting on additional stdin or interactive input (%s). stdin is configured to be closed; this points to a Codex CLI/runtime hang or interactive fallback",
			toolName, timeout, outputState)
		if snippet != "" {
			msg += ". Output snippet: " + snippet
		}
	} else {
		msg = fmt.Sprintf("%s timed out after %v before completing the output contract (%s). This indicates provider latency or a hung CLI invocation",
			toolName, timeout, outputState)
		if snippet != "" {
			msg += ". Output snippet: " + snippet
		}
	}
	return &ExecutorError{
		Code:      "TIMEOUT",
		Message:   msg,
		Retryable: true,
		Guidance:  "increase timeout or reduce prompt size",
	}
}

func containsInteractiveInputWait(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "reading additional input from stdin") ||
		strings.Contains(lower, "waiting for stdin") ||
		strings.Contains(lower, "interactive input")
}

func timeoutSnippet(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	if len(trimmed) <= 180 {
		return trimmed
	}
	return trimmed[:180] + "..."
}
