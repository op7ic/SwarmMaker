// root.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Core pipeline orchestrator for the swarm-maker CLI.
// Implements the 5-phase pipeline: (1) ingest source files and discover LLM
// providers, (2) emit evidence and IR artifacts, (3) run the task-ledger
// generation swarm, (4) validate with programmatic checks, pre-screening,
// adversarial LLM review, and multi-round targeted revision, (5) render
// platform-specific output trees. This file is the main control flow --
// it calls into ingestion, discovery, routing, executor, swarm, output,
// and prompts packages to execute each phase.


package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/executor"
	"github.com/op7ic/swarmmaker/internal/git"
	"github.com/op7ic/swarmmaker/internal/ingestion"
	artifactir "github.com/op7ic/swarmmaker/internal/ir"
	"github.com/op7ic/swarmmaker/internal/swarm"
	"github.com/op7ic/swarmmaker/internal/textutil"
	"github.com/op7ic/swarmmaker/prompts"
)

// Version is set by main via ldflags. It defaults to "dev" for untagged builds.
var Version = "dev"

var (
	inputPath            string
	outputDir            string
	outputSwarm          string
	primaryLLM           string
	criticLLM            string
	projectName          string
	verbose              bool
	dryRun               bool
	force                bool
	modelPrimary         string
	modelCritic          string
	promptPackPath       string
	promptPackExportPath string
)

// minReadableFiles is the basic sanity threshold: at least 1 readable text
// file must be present before we even attempt an LLM call.
const minReadableFiles = 1

// preFlightSummaryLimit caps how much source content is sent in the
// pre-flight validation prompt. Keeps the call fast and cheap.
const preFlightSummaryLimit = 2000

var rootCmd = &cobra.Command{
	Use:   "swarm-maker",
	Short: "Turn loose documentation into model-specific skill bundles",
	Long: strings.TrimSpace(`
swarm-maker ingests loose documentation, routes work through installed LLM CLIs,
and emits a validated skill bundle.

Typical usage:
  swarm-maker --input ./input --model codex --critique gemini --output-swarm claude --output-folder ./SKILL
  swarm-maker --input ./input --model codex --critique gemini --output-swarm all --output-folder ./SKILL

Generated ledger artifacts are kept under .tasks/ for evidence and validation.
The requested model-specific output tree or trees are written under the output folder alongside README.md and install.sh.
`),
	RunE: runSwarmMaker,
}

var discoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover available LLM CLI tools on your system",
	RunE:  runDiscover,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print swarm-maker version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("swarm-maker %s\n", Version)
	},
}

func init() {
	rootCmd.Flags().StringVar(&inputPath, "input", "", "Path to folder containing loose documentation (required)")
	rootCmd.Flags().StringVarP(&outputDir, "output-folder", "o", ".", "Output folder for the generated swarm")
	rootCmd.Flags().StringVar(&outputSwarm, "output-swarm", "", "Target swarm format(s): claude|codex|gemini|all or comma-separated list (required)")
	rootCmd.Flags().StringVar(&primaryLLM, "model", "", "LLM CLI for generation (codex|claude|gemini) (required)")
	rootCmd.Flags().StringVar(&criticLLM, "critique", "", "LLM CLI for critique (auto-detected if not set)")
	rootCmd.Flags().StringVarP(&projectName, "name", "n", "", "Project name (derived from folder name if not set)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output showing full LLM interactions")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without calling LLMs")
	rootCmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing output-swarm tree without prompting")
	rootCmd.Flags().StringVar(&modelPrimary, "model-primary", "", "Specific provider model for generation")
	rootCmd.Flags().StringVar(&modelCritic, "model-critic", "", "Model for critic LLM (same options as primary)")
	rootCmd.Flags().StringVar(&promptPackPath, "prompt-pack", "", "Path to user-editable prompt pack JSON (default: embedded SwarmMaker pack)")
	_ = rootCmd.MarkFlagRequired("input")
	_ = rootCmd.MarkFlagRequired("model")
	_ = rootCmd.MarkFlagRequired("output-swarm")
	rootCmd.AddCommand(discoverCmd)
	promptPackCmd.AddCommand(promptPackExportCmd)
	promptPackExportCmd.Flags().StringVarP(&promptPackExportPath, "output", "o", "", "Path to write the default prompt pack JSON")
	rootCmd.AddCommand(promptPackCmd)
	rootCmd.AddCommand(versionCmd)
}

var promptPackCmd = &cobra.Command{
	Use:   "prompt-pack",
	Short: "Manage editable SwarmMaker prompt packs",
}

var promptPackExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the embedded default prompt pack JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := prompts.ExportDefaultPack(promptPackExportPath); err != nil {
			return err
		}
		fmt.Printf("Default prompt pack written to %s\n", promptPackExportPath)
		return nil
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func runDiscover(cmd *cobra.Command, args []string) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	bold.Println("Scanning for LLM CLI tools...")
	fmt.Println()
	tools := discovery.FindAllLLMs()
	if len(tools) == 0 {
		red.Println("No LLM CLI tools found on your system.")
		fmt.Println("Install at least one of: claude, codex, gemini")
		return nil
	}
	for _, tool := range tools {
		if tool.Available {
			green.Printf("  [FOUND] %-10s %s\n", tool.Name, tool.Path)
			if tool.Version != "" {
				fmt.Printf("           Version: %s\n", tool.Version)
			}
		} else {
			red.Printf("  [MISS]  %-10s not found in PATH\n", tool.Name)
		}
	}
	fmt.Println()
	available := 0
	for _, t := range tools {
		if t.Available {
			available++
		}
	}
	if available >= 2 {
		green.Printf("Ready! %d LLM tools available for cross-provider critique.\n", available)
	} else if available == 1 {
		color.New(color.FgYellow).Println("One LLM tool found. Same-provider critique is allowed only as an explicit recorded fallback.")
	} else {
		red.Println("No LLM CLI tools found. Install at least one of: claude, codex, gemini.")
	}
	return nil
}

// validationReport accumulates validation findings for the structured output file.
type validationReport struct {
	complexity       *ingestion.SourceComplexity
	evidenceCount    int
	evidencePath     string
	irManifestPath   string
	promptPackName   string
	promptPackSource string
	promptPackDigest string
	promptPackReview prompts.PackReview
	routingEvents    []string
	programmatic     []swarm.Issue
	preScreen        *swarm.PreScreenResult
	reviewVerdict    string // "approve", "revise", "unknown", "error", or "" before review
	reviewOutput     string
	revisions        []revisionResult
	postScreen       *swarm.PreScreenResult // re-screen after revision (nil if no revision)
	renderParity     []swarm.Issue
	renderError      string
	// Cost tracking
	llmCalls     int
	inputTokens  int
	outputTokens int
	// Prompt injection warnings from ingestion
	injectionWarnings []string
}

type revisionResult struct {
	File     string
	Success  bool
	Duration time.Duration
	Chars    int
	Round    int // revision round (1-based); 0 means legacy single-round
}

// maxRevisionRounds is the maximum number of revision+re-screen cycles.
const maxRevisionRounds = 3

func runSwarmMaker(cmd *cobra.Command, args []string) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)
	yellow := color.New(color.FgYellow)
	red := color.New(color.FgRed)
	bold.Printf("swarm-maker %s -- AI Swarm Maker\n", Version)
	fmt.Println()

	outputFormats, err := parseOutputFormats(outputSwarm)
	if err != nil {
		return fmt.Errorf("invalid --output-swarm: %w", err)
	}
	outputFormatNames := make([]string, 0, len(outputFormats))
	for _, format := range outputFormats {
		outputFormatNames = append(outputFormatNames, string(format))
	}

	// Signal handling: trap SIGINT/SIGTERM so we can clean up staging
	// and kill child LLM processes on Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		red.Printf("\n  Received %v, cleaning up...\n", sig)
		cancel()
	}()
	defer cancel()
	defer signal.Stop(sigCh)

	// Resolve paths
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return fmt.Errorf("invalid path %q: %w", inputPath, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", absPath)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("invalid output path %q: %w", outputDir, err)
	}

	// Derive project name from output directory (the project root) if not set
	if projectName == "" {
		projectName = filepath.Base(absOutput)
		// Avoid "." as a name when output is current dir
		if projectName == "." || projectName == "/" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("cannot determine project name: %w", err)
			}
			projectName = filepath.Base(cwd)
		}
	}
	projectName = sanitizeProjectName(projectName)

	// -------------------------------------------------------
	// Phase 1: Ingest + Discover + Analyze
	// -------------------------------------------------------
	bold.Println("[1/5] Ingesting source files...")
	ingested, err := ingestion.ReadFolder(absPath)
	if err != nil {
		return fmt.Errorf("ingestion failed: %w", err)
	}
	fmt.Printf("  Read %d files (%d bytes total)\n", ingested.FileCount, ingested.TotalBytes)

	// Adaptive Depth Reasoning: analyze source complexity
	complexity := ingestion.AnalyzeComplexity(ingested.Summary)
	green.Printf("  Source depth: %s (%d sections", complexity.Depth, complexity.SectionCount)
	if complexity.DimensionCount > 0 {
		fmt.Printf(", %d dimensions", complexity.DimensionCount)
	}
	if complexity.AppendixCount > 0 {
		fmt.Printf(", %d appendices", complexity.AppendixCount)
	}
	fmt.Println(")")

	// Basic sanity check: reject empty directories before wasting time on discovery.
	// Content quality is judged by the LLM pre-flight call later in the pipeline.
	if err := checkInputQualityGate(ingested, complexity); err != nil {
		return err
	}

	tools := discovery.FindAllLLMs()
	primary, critic, routingEvents, err := selectLLMs(tools)
	if err != nil {
		return err
	}
	green.Printf("  Primary: %s (%s)\n", primary.Name, primary.Path)
	green.Printf("  Critic:  %s (%s)\n", critic.Name, critic.Path)
	for _, event := range routingEvents {
		yellow.Printf("  Routing fallback: %s\n", event)
	}
	promptPack, err := prompts.LoadPack(promptPackPath)
	if err != nil {
		return err
	}
	promptPackReview := promptPack.SemanticReview()
	green.Printf("  Prompt pack: %s (%s)\n", promptPack.Name, promptPack.Source())

	// Detect git from output directory (the project being set up)
	gitCtx := git.DetectContext(absOutput)
	if !gitCtx.IsRepo {
		// Fallback: check input folder for git context
		gitCtx = git.DetectContext(absPath)
	}
	if gitCtx.IsRepo {
		green.Printf("  Git: %s @ %s\n", gitCtx.RemoteURL, gitCtx.Branch)
	}
	fmt.Println()

	if dryRun {
		yellow.Println("[DRY RUN] Would generate a .tasks ledger, validate it, then emit the requested output-swarm tree plus README.md and install.sh.")
		fmt.Printf("  Output: %s\n", absOutput)
		fmt.Printf("  Formats: %s\n", strings.Join(outputFormatNames, ", "))
		return nil
	}

	// -------------------------------------------------------
	// Incremental regeneration: compute source hash and check
	// if we can reuse existing ledger files from a prior run.
	// -------------------------------------------------------
	sourceContentHash := textutil.DigestString(ingested.Summary)
	tasksDir := filepath.Join(absOutput, ".tasks")
	incrementalSkip := checkIncrementalSkip(tasksDir, absOutput, sourceContentHash, force)
	if incrementalSkip {
		green.Println("  Source unchanged, reusing existing ledger files (use --force to regenerate)")
		fmt.Println()
	}

	// -------------------------------------------------------
	// Overwrite protection
	// -------------------------------------------------------
	if !incrementalSkip {
		if err := prepareOutputTree(absOutput, outputFormats, force); err != nil {
			return err
		}
	}

	// -------------------------------------------------------
	// Phase 2: Prepare evidence and draft workspace
	// -------------------------------------------------------
	bold.Println("[2/5] Preparing evidence and draft workspace...")
	if err := os.MkdirAll(absOutput, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return fmt.Errorf("creating .tasks dir: %w", err)
	}
	evidencePath, err := writeEvidenceManifest(tasksDir, ingested)
	if err != nil {
		return fmt.Errorf("writing evidence manifest: %w", err)
	}
	irManifestPath := filepath.Join(tasksDir, "manifest.json")
	irDir := filepath.Join(tasksDir, "ir")
	languages := detectInputLanguages(ingested.Files)
	green.Printf("  Task ledger: %s\n", tasksDir)
	green.Printf("  Evidence ledger: %s\n", evidencePath)

	// -------------------------------------------------------
	// Phase 3: Run draft generation swarm
	// -------------------------------------------------------
	exec := executor.New(primary, critic, verbose)
	exec.Ctx = ctx
	// Track spawned processes in a temp file outside the workspace so the
	// workspace write validator doesn't flag it as an unexpected provider file.
	trackerPath := filepath.Join(os.TempDir(), fmt.Sprintf("swarmmaker-processes-%d.json", os.Getpid()))
	exec.Tracker = executor.NewProcessTracker(trackerPath)
	defer exec.Tracker.Cleanup()

	// Apply model overrides
	if modelPrimary != "" || modelCritic != "" {
		exec.Models = make(executor.ModelOverrides)
		if modelPrimary != "" {
			exec.Models[primary.Name] = modelPrimary
			green.Printf("  Primary model: %s -> %s\n", primary.Name, modelPrimary)
		}
		if modelCritic != "" {
			exec.Models[critic.Name] = modelCritic
			green.Printf("  Critic model: %s -> %s\n", critic.Name, modelCritic)
		}
	}

	if err := exec.SetStagingDir(absOutput); err != nil {
		return fmt.Errorf("staging setup: %w", err)
	}
	defer exec.Cleanup()

	// Record sandbox fallback evidence if codex is the primary and bwrap is unavailable
	if primary.Name == "codex" && !exec.CodexSandboxAvailable() {
		ingested.Evidence = append(ingested.Evidence, ingestion.EvidenceEntry{
			Phase:    ingestion.EvidencePhaseGeneration,
			Category: ingestion.EvidenceCategorySandboxFallback,
			RelPath:  "(runtime)",
			Detail:   "bubblewrap sandbox unavailable; using --dangerously-bypass-approvals-and-sandbox for codex",
		})
	}

	// Write source manifest to staging for debugging/cache (no longer referenced in prompts)
	_, err = exec.WriteContextFile("manifest.md", ingested.Summary)
	if err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	// Source content is inlined directly into prompts (eliminates Read tool call per LLM invocation)
	manifestContent := ingested.Summary

	// ADR: compute source hints that get prepended to every prompt
	sourceHints := complexity.FormatHints()
	promptIR := prompts.PromptIR{
		ProjectName:          projectName,
		SourceMaterial:       manifestContent,
		InputRoot:            absPath,
		TargetFormats:        outputFormatNames,
		GeneratorProvider:    primary.Name,
		CriticProvider:       critic.Name,
		OutputRenderers:      outputFormatNames,
		EvidenceManifestPath: evidencePath,
		IRManifestPath:       irManifestPath,
		PromptPackName:       promptPack.Name,
		PromptPackSource:     promptPack.Source(),
		PromptPackDigest:     promptPack.Digest(),
		InputFileCount:       ingested.FileCount,
		BinaryFileCount:      len(ingested.BinaryFiles),
		EvidenceEventCount:   len(ingested.Evidence),
		ToolLanguages:        languages,
		SourceFiles:          promptSourceFiles(absPath, ingested.Files),
	}
	artifactPaths, err := artifactir.WriteArtifacts(irDir, artifactir.ArtifactInput{
		ProductName:   projectName,
		CLIName:       "swarm-maker",
		Description:   "Turn loose documentation into model-specific skill bundles rooted in .tasks.",
		InputRoot:     absPath,
		OutputRoot:    absOutput,
		OutputFormats: outputFormatNames,
		Ingested:      ingested,
		Providers:     tools,
		Generator:     primary,
		Critic:        critic,
		RoutingEvents: routingEvents,
		PromptIR:      promptIR,
		ToolLanguages: languages,
	})
	if err != nil {
		return fmt.Errorf("writing IR artifacts: %w", err)
	}
	if err := writeIRManifest(irManifestPath, cliIRManifest{
		ProductName:      projectName,
		CLIName:          "swarm-maker",
		Description:      "Turn loose documentation into model-specific skill bundles.",
		InputRoot:        absPath,
		OutputRoot:       absOutput,
		OutputFormats:    outputFormatNames,
		Generator:        primary.Name,
		Critic:           critic.Name,
		RoutingEvents:    routingEvents,
		PromptPackName:   promptPack.Name,
		PromptPackSource: promptPack.Source(),
		PromptPackDigest: promptPack.Digest(),
		ToolLanguages:    languages,
		EvidencePath:     evidencePath,
		IRDirectory:      irDir,
		IRContractPath:   artifactPaths.ManifestPath,
		EvidenceCount:    len(ingested.Evidence),
		SourceFileCount:  ingested.FileCount,
		BinaryFileCount:  len(ingested.BinaryFiles),
		SourceContentHash: sourceContentHash,
	}); err != nil {
		return fmt.Errorf("writing IR manifest: %w", err)
	}
	green.Printf("  IR manifest: %s\n", irManifestPath)
	green.Printf("  Detailed IR: %s\n", artifactPaths.ManifestPath)
	fmt.Println()

	// Pre-flight, Phase 3, and post-generation evidence scan are skipped
	// when incremental regeneration detects unchanged source content.
	var swarmLLMCalls, swarmInputTokens, swarmOutputTokens int
	if !incrementalSkip {
		// Pre-flight source validation: one short LLM call to judge whether the
		// source material is rich enough to produce useful agent skills. Costs ~$0.01
		// to potentially save 9+ expensive generation calls ($1-5) on garbage input.
		bold.Println("[Pre-flight] Validating source material sufficiency...")
		if err := runPreFlightValidation(exec, ingested, complexity); err != nil {
			return err
		}
		green.Println("  Source material: SUFFICIENT")
		fmt.Println()

		// Phase 3: Run task-ledger generation in two phases.
		// Phase A generates foundational files (context.md, tasks.md).
		// Phase B uses a summary of Phase A output as ledger context for the remaining 7 files.
		sw := swarm.New(exec, absOutput, verbose)
		bold.Printf("[3/5] Running task-ledger generation swarm (%d agents, concurrency %d)...\n", len(ledgerFiles), sw.Concurrency)

		// Phase A: foundational files
		phaseATasks, err := swarm.BuildPhaseATasks(promptIR, sourceHints, promptPack)
		if err != nil {
			return fmt.Errorf("building phase A prompts: %w", err)
		}
		fmt.Println("  Phase A: generating foundational files (context.md, tasks.md)...")
		phaseAResults := sw.Run(phaseATasks)
		if failures := swarm.Failures(phaseAResults); len(failures) > 0 {
			red.Printf("  %d/%d phase A tasks failed, aborting\n", len(failures), len(phaseATasks))
			for _, f := range failures {
				red.Printf("    x %s: %v\n", f.Task.Name, f.Error)
			}
			return fmt.Errorf("%d/%d phase A swarm tasks failed", len(failures), len(phaseATasks))
		}
		green.Printf("  Phase A: %d/%d foundational files completed\n", swarm.SuccessCount(phaseAResults), len(phaseATasks))

		// Build ledger context summary from Phase A outputs
		ledgerContext := buildLedgerContext(absOutput)

		// Phase B: dependent files with ledger context
		phaseBTasks, err := swarm.BuildPhaseBTasks(promptIR, sourceHints, promptPack, ledgerContext)
		if err != nil {
			return fmt.Errorf("building phase B prompts: %w", err)
		}
		fmt.Println("  Phase B: generating dependent files with ledger context...")
		phaseBResults := sw.Run(phaseBTasks)
		if failures := swarm.Failures(phaseBResults); len(failures) > 0 {
			red.Printf("  %d/%d phase B tasks failed, aborting\n", len(failures), len(phaseBTasks))
			for _, f := range failures {
				red.Printf("    x %s: %v\n", f.Task.Name, f.Error)
			}
			return fmt.Errorf("%d/%d phase B swarm tasks failed", len(failures), len(phaseBTasks))
		}

		total := len(phaseATasks) + len(phaseBTasks)
		successes := swarm.SuccessCount(phaseAResults) + swarm.SuccessCount(phaseBResults)
		green.Printf("  %d/%d tasks completed (two-phase)\n", successes, total)
		fmt.Println()

		// -------------------------------------------------------
		// Post-generation evidence scan: count implementation decisions
		// -------------------------------------------------------
		planningFiles := []string{".tasks/todo.md", ".tasks/skills.md", ".tasks/agents.md"}
		implDecisionEvidence := scanImplementationDecisions(absOutput, planningFiles)
		if len(implDecisionEvidence) > 0 {
			if err := rewriteEvidenceManifest(tasksDir, ingested, implDecisionEvidence); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: failed to update evidence.json with implementation decisions: %v\n", err)
			} else {
				// Update the promptIR evidence count so phase 4 sees the correct value
				promptIR.EvidenceEventCount = len(ingested.Evidence)
				green.Printf("  Evidence: %d implementation decision events recorded\n", len(implDecisionEvidence))
			}
		}

		// Estimate cost from swarm generation tasks (v1: rough char/4 estimate)
		for _, t := range append(phaseATasks, phaseBTasks...) {
			swarmInputTokens += len(t.Prompt) / 4
		}
		for _, r := range append(phaseAResults, phaseBResults...) {
			swarmOutputTokens += len(r.Content) / 4
		}
		swarmLLMCalls = len(phaseATasks) + len(phaseBTasks)
	} else {
		bold.Println("[3/5] Skipped -- source unchanged, reusing existing ledger files")
		fmt.Println()
	}

	// -------------------------------------------------------
	// Phase 4: Validate + Revise (smart gate)
	// -------------------------------------------------------
	//
	// Call budget reasoning:
	//
	// Phase 3: 9 task-ledger generation calls (skipped on incremental reuse)
	// Phase 4: 1-4 calls (adversarial review always runs; revision is gated)
	//
	// How the gate works:
	//   1. Programmatic checks: file existence, sizes, links, placeholders (0 calls, instant)
	//   2. Smart pre-screen: citation density, dimension coverage, fabrication signals (0 calls, instant)
	//   3. ONE adversarial review runs even when pre-screen is clean (1 call)
	//   4. A malformed or failed review hard-fails the decision gate
	//   5. IF review says REVISE → targeted revision of flagged files (1-3 calls)
	//
	// Total pipeline: 8-11 calls depending on critique/revision outcome.
	//
	bold.Println("[4/5] Validating...")

	report := &validationReport{
		complexity:       complexity,
		evidenceCount:    len(ingested.Evidence),
		evidencePath:     evidencePath,
		irManifestPath:   irManifestPath,
		promptPackName:   promptPack.Name,
		promptPackSource: promptPack.Source(),
		promptPackDigest: promptPack.Digest(),
		promptPackReview: promptPackReview,
		routingEvents:    routingEvents,
		llmCalls:          swarmLLMCalls + 1, // +1 for pre-flight
		inputTokens:       swarmInputTokens,
		outputTokens:      swarmOutputTokens,
		injectionWarnings: collectInjectionWarnings(ingested.Evidence),
	}
	failValidation := func(err error) error {
		if reportErr := writeValidationReport(tasksDir, report); reportErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to write validation report to %s: %v\n", tasksDir, reportErr)
			fmt.Fprintf(os.Stderr, "--- validation report (stderr fallback) ---\n")
			dumpValidationReportToWriter(os.Stderr, report)
			fmt.Fprintf(os.Stderr, "--- end validation report ---\n")
			return fmt.Errorf("%w; additionally failed to write validation report: %v", err, reportErr)
		}
		return err
	}

	// Level 1: Programmatic checks (0 LLM calls)
	allFiles := append([]string(nil), ledgerFiles...)
	criticalFiles := append([]string(nil), criticalLedgerFiles...)

	issues := validateDraftProgrammatic(absOutput, allFiles, absOutput, absPath)
	report.programmatic = issues
	errorCount := swarm.ErrorCount(issues)
	if errorCount > 0 {
		yellow.Printf("  Programmatic: %d issues found\n", errorCount)
		for _, iss := range issues {
			if iss.Severity == "error" {
				yellow.Printf("    ! %s: %s\n", iss.File, iss.Problem)
			}
		}
	} else {
		green.Println("  Programmatic: all files pass")
	}

	// Level 2: Smart pre-screen gate (0 LLM calls)
	// Checks citation density, dimension coverage, fabrication patterns across 6 critical files.
	preScreen := swarm.PreScreenFiles(absOutput, criticalFiles, complexity, ingested.Summary)
	report.preScreen = preScreen

	// Always run adversarial review — pre-screen informs what the reviewer focuses on, but never replaces it.
	// One LLM call is cheap; skipping it risks letting hallucinations through unchecked.
	if preScreen.NeedsLLMReview {
		yellow.Printf("  Pre-screen: %d flags found, running adversarial review...\n", len(preScreen.Reasons))
		for _, reason := range preScreen.Reasons {
			yellow.Printf("    ! %s\n", reason)
		}
	} else if errorCount > 0 {
		yellow.Println("  Pre-screen: programmatic errors found, running adversarial review...")
	} else {
		green.Println("  Pre-screen: clean — running adversarial review for final validation...")
	}

	{
		// Determine which files to review: flagged files, or all critical if pre-screen was clean
		filesToReview := preScreen.FlaggedFiles()
		if len(filesToReview) == 0 {
			filesToReview = criticalFiles
		}

		// ONE adversarial review call (replaces 3× fidelity + coverage + cross-check = 5 calls)
		reviewSnapshots, err := readPromptFileSnapshots(absOutput, allFiles)
		if err != nil {
			return failValidation(fmt.Errorf("reading draft files for adversarial review: %w", err))
		}
		reviewPrompt, err := prompts.AdversarialReviewPromptWithPack(
			promptIR, promptPack, reviewSnapshots, filesToReview, preScreen.Reasons,
		)
		if err != nil {
			return failValidation(fmt.Errorf("building adversarial review prompt: %w", err))
		}
		reviewResp, err := exec.RunCritic(reviewPrompt)
		report.llmCalls++
		if reviewResp != nil {
			report.inputTokens += reviewResp.InputTokens
			report.outputTokens += reviewResp.OutputTokens
		}
		if err != nil {
			yellow.Printf("  Adversarial review failed: %v\n", err)
			report.reviewVerdict = "error"
			return failValidation(fmt.Errorf("adversarial review failed: %w", err))
		} else {
			verdict := parseVerdict(reviewResp.Output)
			report.reviewVerdict = verdict
			report.reviewOutput = reviewResp.Output

			if verdict == "approve" {
				green.Printf("  Adversarial review: APPROVE (%v)\n", reviewResp.Duration.Round(time.Second))
				if errorCount > 0 {
					return failValidation(fmt.Errorf("programmatic validation failed with %d error(s) despite review approval", errorCount))
				}
				if preScreen.HasConcreteFlags() {
					return failValidation(fmt.Errorf("pre-screen found concrete findings in %d file(s) despite review approval", len(preScreen.FlaggedFiles())))
				}
			} else if verdict == "unknown" {
				yellow.Printf("  Adversarial review: UNKNOWN (%v)\n", reviewResp.Duration.Round(time.Second))
				return failValidation(fmt.Errorf("adversarial review verdict is missing or malformed"))
			} else {
				yellow.Printf("  Adversarial review: REVISE (%v)\n", reviewResp.Duration.Round(time.Second))
				fmt.Printf("    %s\n", strings.ReplaceAll(strings.TrimSpace(reviewResp.Output), "\n", "\n    "))

				// Parse which specific files the review flagged for revision
				revisionFiles := parseFilesForRevision(reviewResp.Output, allFiles)
				if len(revisionFiles) == 0 {
					return failValidation(fmt.Errorf("adversarial review requested revision but did not identify files to revise"))
				}

				// Multi-round revision loop with regression detection.
				previousFlagCount := -1 // sentinel: no previous round yet
				for round := 1; round <= maxRevisionRounds; round++ {
					if round > 1 {
						yellow.Printf("  Revision round %d/%d targeting %d file(s)\n", round, maxRevisionRounds, len(revisionFiles))
					}

					// Per-file revision with cross-file context for consistency.
					roundRevisions := reviseFromReview(exec, absOutput, promptIR, promptPack, sourceHints, revisionFiles, reviewResp.Output, report, green, yellow)
					for i := range roundRevisions {
						roundRevisions[i].Round = round
					}
					report.revisions = append(report.revisions, roundRevisions...)

					failedRevisions := 0
					for _, revision := range roundRevisions {
						if !revision.Success {
							failedRevisions++
						}
					}
					if failedRevisions > 0 {
						return failValidation(fmt.Errorf("%d targeted revision(s) failed in round %d", failedRevisions, round))
					}

					// Repair citation path hallucinations before re-checking.
					// LLMs occasionally mangle directory paths while keeping filenames correct.
					knownCitationPaths := buildKnownCitationPaths(absPath, absOutput)
					for _, f := range allFiles {
						fPath := filepath.Join(absOutput, f)
						if n, err := repairCitationPaths(fPath, knownCitationPaths); err == nil && n > 0 {
							if verbose {
								fmt.Printf("  [repair] Fixed %d citation path(s) in %s\n", n, f)
							}
						}
					}

					// Post-revision programmatic re-check (0 LLM calls).
					postIssues := validateDraftProgrammatic(absOutput, allFiles, absOutput, absPath)
					report.programmatic = postIssues
					postErrorCount := swarm.ErrorCount(postIssues)
					if postErrorCount > 0 {
						return failValidation(fmt.Errorf("post-revision programmatic validation failed with %d error(s) in round %d", postErrorCount, round))
					}

					// Post-revision pre-screen re-check.
					postScreen := swarm.PreScreenFiles(absOutput, criticalFiles, complexity, ingested.Summary)
					report.postScreen = postScreen

					if !postScreen.HasConcreteFlags() {
						green.Printf("  Post-revision re-screen (round %d): all concrete flags cleared\n", round)
						break // All concrete flags cleared. Advisory-only flags don't block.
					}

					currentFlagCount := len(postScreen.FlaggedFiles())
					yellow.Printf("  Post-revision re-screen (round %d): %d flag(s) remain\n", round, currentFlagCount)
					for _, reason := range postScreen.Reasons {
						yellow.Printf("    ! %s\n", reason)
					}

					// Regression detection: if this round didn't reduce flags, stop.
					if previousFlagCount >= 0 && currentFlagCount >= previousFlagCount {
						yellow.Printf("  Revision stopped: no improvement (round %d: %d flags, round %d: %d flags)\n",
							round, currentFlagCount, round-1, previousFlagCount)
						break
					}

					// If we've exhausted all rounds, stop.
					if round == maxRevisionRounds {
						break
					}

					// Prepare next round: only target files that still have flags.
					revisionFiles = postScreen.FlaggedFiles()
					previousFlagCount = currentFlagCount
				}

				// After the loop, check if CONCRETE flags still remain.
				// Advisory-only findings (anti-pattern checks) should not block the build.
				if report.postScreen != nil && report.postScreen.HasConcreteFlags() {
					return failValidation(fmt.Errorf("post-revision pre-screen still has %d concrete flag(s) after %d revision round(s)",
						len(report.postScreen.FlaggedFiles()), revisionRoundCount(report.revisions)))
				}
			}
		}
	}
	fmt.Println()

	renderedBundle, err := renderOutputSwarms(absOutput, outputFormats, projectName, primary.Name, critic.Name, allFiles)
	if err != nil {
		report.renderError = err.Error()
		return failValidation(fmt.Errorf("rendering output swarms: %w", err))
	}
	report.renderParity = validateRenderedOutputParity(renderedBundle)
	if renderErrors := swarm.ErrorCount(report.renderParity); renderErrors > 0 {
		yellow.Printf("  Render parity: %d issues found\n", renderErrors)
		for _, iss := range report.renderParity {
			yellow.Printf("    ! %s: %s\n", iss.File, iss.Problem)
		}
		return failValidation(fmt.Errorf("render parity validation failed with %d error(s)", renderErrors))
	}
	green.Println("  Render parity: all selected platform trees match the shared .tasks decomposition")

	if err := writeRenderedOutputSwarms(absOutput, outputFormats, projectName, primary.Name, critic.Name, renderedBundle); err != nil {
		report.renderError = err.Error()
		return failValidation(fmt.Errorf("writing output swarms: %w", err))
	}

	// Write structured validation report to the central task ledger.
	if err := writeValidationReport(tasksDir, report); err != nil {
		return fmt.Errorf("writing validation report: %w", err)
	}

	// -------------------------------------------------------
	// Phase 5: Done
	// -------------------------------------------------------
	bold.Println("[5/5] Done!")
	green.Println("  Generated files:")
	if walkErr := filepath.Walk(absOutput, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(absOutput, path)
		if !strings.HasPrefix(rel, ".staging") {
			fmt.Printf("    %s\n", rel)
		}
		return nil
	}); walkErr != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not list generated files: %v\n", walkErr)
	}
	fmt.Println()
	bold.Printf("Your SKILL bundle is ready at: %s\n", absOutput)
	fmt.Printf("Validation report: %s\n", filepath.Join(tasksDir, "validation-report.md"))
	fmt.Printf("IR manifest: %s\n", irManifestPath)
	rootDirs, err := platformRootDirs(outputFormats)
	if err != nil {
		return err
	}
	for _, rootDir := range rootDirs {
		fmt.Printf("Output swarm: %s\n", filepath.Join(absOutput, rootDir))
	}
	return nil
}

// reviseFromReview takes the adversarial review output and feeds it to the primary LLM
// as a targeted revision prompt for each flagged file. Each per-file prompt includes
// cross-file context (summaries of sibling flagged files and the reviewer's cross-file
// findings) so the LLM can maintain consistency across files without fragile multi-file
// response parsing.
func reviseFromReview(
	exec *executor.Executor,
	draftDir string,
	promptIR prompts.PromptIR,
	promptPack prompts.Pack,
	sourceHints string,
	filesToRevise []string,
	reviewFindings string,
	report *validationReport,
	green, yellow *color.Color,
) []revisionResult {
	results := make([]revisionResult, 0, len(filesToRevise))

	// Read all flagged file snapshots up front for cross-file context.
	allSnapshots := make([]prompts.PromptFileSnapshot, 0, len(filesToRevise))
	for _, f := range filesToRevise {
		snapshot, err := readPromptFileSnapshot(draftDir, f)
		if err != nil {
			// Will be caught again in the per-file loop below.
			continue
		}
		allSnapshots = append(allSnapshots, snapshot)
	}

	for _, f := range filesToRevise {
		snapshot, err := readPromptFileSnapshot(draftDir, f)
		if err != nil {
			yellow.Printf("    ? %s: read current draft failed: %v\n", f, err)
			results = append(results, revisionResult{File: f, Success: false})
			continue
		}
		revisionPrompt, err := prompts.RevisionPromptWithPack(promptIR, promptPack, snapshot, reviewFindings)
		if err != nil {
			yellow.Printf("    ? %s: revision prompt failed: %v\n", f, err)
			results = append(results, revisionResult{File: f, Success: false})
			continue
		}

		// Build cross-file context when multiple files are being revised.
		crossFileCtx := ""
		if len(filesToRevise) > 1 {
			crossFileCtx = prompts.BuildCrossFileContext(f, allSnapshots, reviewFindings)
		}

		revPrompt := sourceHints + crossFileCtx + revisionPrompt
		revResp, err := exec.RunPrimaryToFile(revPrompt, snapshot.AbsPath)
		report.llmCalls++
		if revResp != nil {
			report.inputTokens += revResp.InputTokens
			report.outputTokens += revResp.OutputTokens
		}
		if err != nil {
			yellow.Printf("    ? %s: revision failed: %v\n", f, err)
			results = append(results, revisionResult{File: f, Success: false})
			continue
		}
		green.Printf("    ~ %s: revised (%d chars, %v)\n", f, len(revResp.Output), revResp.Duration.Round(time.Second))
		results = append(results, revisionResult{
			File:     f,
			Success:  true,
			Duration: revResp.Duration,
			Chars:    len(revResp.Output),
		})
	}

	return results
}

// revisionRoundCount returns the highest round number across all revision results.
func revisionRoundCount(revisions []revisionResult) int {
	maxRound := 0
	for _, r := range revisions {
		if r.Round > maxRound {
			maxRound = r.Round
		}
	}
	if maxRound == 0 && len(revisions) > 0 {
		return 1 // legacy single-round
	}
	return maxRound
}

type cliIRManifest struct {
	ProductName      string   `json:"product_name"`
	CLIName          string   `json:"cli_name"`
	Description      string   `json:"description"`
	InputRoot        string   `json:"input_root"`
	OutputRoot       string   `json:"output_root"`
	OutputFormats    []string `json:"output_formats"`
	Generator        string   `json:"generator"`
	Critic           string   `json:"critic"`
	RoutingEvents    []string `json:"routing_events,omitempty"`
	PromptPackName   string   `json:"prompt_pack_name"`
	PromptPackSource string   `json:"prompt_pack_source"`
	PromptPackDigest string   `json:"prompt_pack_digest"`
	ToolLanguages    []string `json:"tool_languages"`
	EvidencePath     string   `json:"evidence_manifest_path"`
	IRDirectory      string   `json:"ir_directory"`
	IRContractPath   string   `json:"ir_contract_manifest_path"`
	EvidenceCount    int      `json:"evidence_count"`
	SourceFileCount  int      `json:"source_file_count"`
	BinaryFileCount  int      `json:"binary_file_count"`
	SourceContentHash string  `json:"source_content_hash,omitempty"`
}

func writeEvidenceManifest(outputDir string, ctx *ingestion.Context) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("ingestion context is required")
	}
	path := filepath.Join(outputDir, "evidence.json")
	// Write all current entries as JSONL (one JSON object per line), truncating any existing file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	for _, entry := range ctx.Evidence {
		line, err := json.Marshal(entry)
		if err != nil {
			return "", fmt.Errorf("marshal evidence entry: %w", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return "", fmt.Errorf("write evidence entry: %w", err)
		}
	}
	return path, nil
}

// buildLedgerContext reads the Phase A foundational files (context.md, tasks.md)
// and produces a short summary block that gets prepended to Phase B prompts.
// This ensures dependent files are consistent with foundational decisions.
func buildLedgerContext(outputDir string) string {
	const maxSummaryLen = 500
	var parts []string
	for _, rel := range []string{".tasks/context.md", ".tasks/tasks.md"} {
		data, err := os.ReadFile(filepath.Join(outputDir, rel))
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(data))
		if len(text) > maxSummaryLen {
			text = text[:maxSummaryLen] + "..."
		}
		parts = append(parts, fmt.Sprintf("--- %s ---\n%s", rel, text))
	}
	if len(parts) == 0 {
		return ""
	}
	return "LEDGER CONTEXT (from already-generated foundational files):\n" +
		strings.Join(parts, "\n") +
		"\nEnsure your output is consistent with these foundational decisions.\n\n"
}

// checkIncrementalSkip reads the existing manifest.json from the tasks directory
// and compares the stored source content hash against the current source hash.
// It returns true if the source is unchanged AND all ledger files exist, meaning
// the generation swarm can be skipped. When --force is set, it always returns false.
func checkIncrementalSkip(tasksDir string, outputDir string, currentHash string, forceFlag bool) bool {
	if forceFlag {
		return false
	}
	manifestPath := filepath.Join(tasksDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return false
	}
	var manifest cliIRManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	if manifest.SourceContentHash == "" || manifest.SourceContentHash != currentHash {
		return false
	}
	for _, rel := range ledgerFiles {
		path := filepath.Join(outputDir, rel)
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

// scanImplementationDecisions scans planning-mode files for occurrences of
// "implementation decision" (both hyphenated and space-separated variants).
// It returns new evidence entries for files with >0 occurrences.
// The citation contract itself is excluded by only counting in planning files.
func scanImplementationDecisions(tasksDir string, planningFiles []string) []ingestion.EvidenceEntry {
	var entries []ingestion.EvidenceEntry
	for _, file := range planningFiles {
		content, err := os.ReadFile(filepath.Join(tasksDir, file))
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(content))
		count := strings.Count(lower, "implementation decision") +
			strings.Count(lower, "implementation-decision")
		if count > 0 {
			entries = append(entries, ingestion.EvidenceEntry{
				Phase:    ingestion.EvidencePhaseGeneration,
				Category: ingestion.EvidenceCategoryImplementationDecision,
				RelPath:  file,
				Detail:   fmt.Sprintf("%d implementation decisions recorded", count),
			})
		}
	}
	return entries
}

// rewriteEvidenceManifest appends new evidence entries to evidence.json using
// JSONL append-only semantics. Each new entry is one JSON object per line,
// appended to the existing file without reading or rewriting prior content.
func rewriteEvidenceManifest(tasksDir string, ctx *ingestion.Context, newEntries []ingestion.EvidenceEntry) error {
	if len(newEntries) == 0 {
		return nil
	}
	ctx.Evidence = append(ctx.Evidence, newEntries...)
	path := filepath.Join(tasksDir, "evidence.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s for append: %w", path, err)
	}
	defer f.Close()
	for _, entry := range newEntries {
		line, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal evidence entry: %w", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("append evidence entry: %w", err)
		}
	}
	return nil
}

// readEvidenceEntries reads JSONL evidence entries from evidence.json.
func readEvidenceEntries(path string) ([]ingestion.EvidenceEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []ingestion.EvidenceEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry ingestion.EvidenceEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse evidence line: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func writeIRManifest(path string, manifest cliIRManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal IR manifest: %w", err)
	}
	if err := os.WriteFile(path, payload, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

var draftMarkdownLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// templateLeakPatterns detect prompt template fragments that an LLM may have
// reproduced verbatim instead of filling them with real content.
var templateLeakPatterns = []string{
	"absolute/path",
	"example/path",
	"relative/path.md",
	"[FILENAME](",
	"[FILENAME1](",
	"[FILENAME2](",
	"(PATH)",
	"(PATH1)",
	"(PATH2)",
	"<Skill Name>",
	"<Agent Name>",
	"<kebab-case slug>",
	"<one-sentence purpose>",
	"<markdown body>",
	"<Observe|Orient|Decide|Act or source-backed specialized role>",
}

var draftMetaNotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)^\s*(Created|Updated|Rewrote|Replaced|Wrote)\b`),
	regexp.MustCompile(`(?im)^\s*What changed:`),
	regexp.MustCompile(`(?im)^\s*Updated files:`),
	regexp.MustCompile(`(?im)^\s*Checks passed:`),
	regexp.MustCompile(`(?im)^\s*Verified files:`),
	regexp.MustCompile(`(?im)^\s*I did not\b`),
	regexp.MustCompile(`(?im)^\s*If you want\b`),
	regexp.MustCompile(`(?im)^\s*The target markdown files already contain\b`),
}

func validateDraftProgrammatic(draftDir string, expectedFiles []string, allowedLinkRoots ...string) []swarm.Issue {
	var issues []swarm.Issue
	roots := make([]string, 0, len(allowedLinkRoots)+1)
	roots = append(roots, filepath.Clean(draftDir))
	for _, root := range allowedLinkRoots {
		clean := filepath.Clean(root)
		if clean == "." || clean == "" {
			continue
		}
		duplicate := false
		for _, existing := range roots {
			if existing == clean {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, clean)
		}
	}
	for _, rel := range expectedFiles {
		absPath := filepath.Join(draftDir, rel)
		info, err := os.Stat(absPath)
		if err != nil {
			issues = append(issues, swarm.Issue{File: rel, Problem: "file missing", Severity: "error"})
			continue
		}
		if minLen := ledgerMinLengths[rel]; minLen > 0 && info.Size() < minLen {
			issues = append(issues, swarm.Issue{
				File:     rel,
				Problem:  fmt.Sprintf("too small (%d bytes, need %d)", info.Size(), minLen),
				Severity: "error",
			})
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			issues = append(issues, swarm.Issue{File: rel, Problem: "file unreadable", Severity: "error"})
			continue
		}
		text := string(content)
		for _, pattern := range []string{"Awaiting swarm output", "{LLM-generated", "{project-name}", "{Project Name}"} {
			if strings.Contains(text, pattern) {
				issues = append(issues, swarm.Issue{
					File:     rel,
					Problem:  fmt.Sprintf("contains placeholder: %q", pattern),
					Severity: "error",
				})
			}
		}
		for _, leak := range templateLeakPatterns {
			if strings.Contains(text, leak) {
				issues = append(issues, swarm.Issue{
					File:     rel,
					Problem:  fmt.Sprintf("leaked template fragment: %q", leak),
					Severity: "error",
				})
			}
		}
		for _, pat := range draftMetaNotePatterns {
			if pat.MatchString(text) {
				issues = append(issues, swarm.Issue{
					File:     rel,
					Problem:  "appears to be status-note/meta commentary instead of the artifact body",
					Severity: "error",
				})
				break
			}
		}
		for _, match := range draftMarkdownLinkRe.FindAllStringSubmatch(text, -1) {
			if len(match) < 3 {
				continue
			}
			target := strings.TrimSpace(match[2])
			if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}
			if strings.Contains(target, "://") {
				issues = append(issues, swarm.Issue{File: rel, Problem: fmt.Sprintf("unsupported link target: %s", target), Severity: "error"})
				continue
			}
			targetPath := resolveMarkdownTargetPath(filepath.Dir(absPath), target)
			if !pathWithinAllowedRoots(targetPath, roots) {
				issues = append(issues, swarm.Issue{File: rel, Problem: fmt.Sprintf("link escapes allowed roots: %s", target), Severity: "error"})
				continue
			}
			if _, err := os.Stat(targetPath); err != nil {
				issues = append(issues, swarm.Issue{File: rel, Problem: fmt.Sprintf("broken link: [%s](%s)", match[1], target), Severity: "error"})
			}
		}
	}
	return issues
}

func resolveMarkdownTargetPath(baseDir, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(baseDir, target))
}

func pathWithinAllowedRoots(path string, roots []string) bool {
	for _, root := range roots {
		if path == root {
			return true
		}
		if strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func detectInputLanguages(files []ingestion.FileEntry) []string {
	seen := make(map[string]struct{})
	for _, file := range files {
		switch strings.ToLower(filepath.Ext(file.RelPath)) {
		case ".py":
			seen["python"] = struct{}{}
		case ".go":
			seen["go"] = struct{}{}
		case ".ts", ".tsx":
			seen["typescript"] = struct{}{}
		case ".js", ".jsx":
			seen["javascript"] = struct{}{}
		case ".rs":
			seen["rust"] = struct{}{}
		}
	}
	order := []string{"go", "python", "typescript", "javascript", "rust"}
	languages := make([]string, 0, len(seen))
	for _, language := range order {
		if _, ok := seen[language]; ok {
			languages = append(languages, language)
		}
	}
	return languages
}

func promptSourceFiles(root string, files []ingestion.FileEntry) []prompts.SourceFileRef {
	refs := make([]prompts.SourceFileRef, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.RelPath) == "" {
			continue
		}
		refs = append(refs, prompts.SourceFileRef{
			RelPath: file.RelPath,
			AbsPath: filepath.Join(root, filepath.FromSlash(file.RelPath)),
		})
	}
	return refs
}

func readPromptFileSnapshots(root string, relFiles []string) ([]prompts.PromptFileSnapshot, error) {
	snapshots := make([]prompts.PromptFileSnapshot, 0, len(relFiles))
	for _, rel := range relFiles {
		snapshot, err := readPromptFileSnapshot(root, rel)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func readPromptFileSnapshot(root, rel string) (prompts.PromptFileSnapshot, error) {
	if strings.TrimSpace(rel) == "" {
		return prompts.PromptFileSnapshot{}, fmt.Errorf("relative path is required")
	}
	absPath := filepath.Join(root, rel)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return prompts.PromptFileSnapshot{}, fmt.Errorf("read %s: %w", absPath, err)
	}
	return prompts.PromptFileSnapshot{
		RelPath: rel,
		AbsPath: absPath,
		Content: string(content),
	}, nil
}

// writeValidationReport persists a structured validation report to the output directory.
// This gives the user a persistent artifact to review before committing to further API runs.
func writeValidationReport(outputDir string, r *validationReport) error {
	var b strings.Builder

	b.WriteString("# Validation Report\n\n")

	// Source analysis
	b.WriteString("## Source Analysis\n\n")
	if r.complexity != nil {
		c := r.complexity
		b.WriteString(fmt.Sprintf("- **Depth**: %s\n", c.Depth))
		b.WriteString(fmt.Sprintf("- **Length**: %d characters\n", c.SourceLength))
		b.WriteString(fmt.Sprintf("- **Sections**: %d\n", c.SectionCount))
		if c.DimensionCount > 0 {
			b.WriteString(fmt.Sprintf("- **Dimensions**: %d\n", c.DimensionCount))
		}
		if c.AppendixCount > 0 {
			b.WriteString(fmt.Sprintf("- **Appendices**: %d\n", c.AppendixCount))
		}
		b.WriteString(fmt.Sprintf("- **Numerical density**: %.1f per 1000 chars\n", c.NumericalDensity))
		b.WriteString(fmt.Sprintf("- **List items**: %d\n", c.ListItemCount))
	}
	b.WriteString("\n")

	b.WriteString("## Evidence Ledger\n\n")
	if r.evidencePath != "" {
		b.WriteString(fmt.Sprintf("- **Path**: %s\n", r.evidencePath))
		b.WriteString(fmt.Sprintf("- **Evidence events**: %d\n\n", r.evidenceCount))
	} else {
		b.WriteString("- [FAIL] evidence ledger path missing\n\n")
	}

	b.WriteString("## IR Manifest\n\n")
	if r.irManifestPath != "" {
		b.WriteString(fmt.Sprintf("- **Path**: %s\n\n", r.irManifestPath))
	} else {
		b.WriteString("- [FAIL] IR manifest path missing\n\n")
	}

	b.WriteString("## Prompt Pack\n\n")
	if r.promptPackName != "" && r.promptPackSource != "" && r.promptPackDigest != "" {
		b.WriteString(fmt.Sprintf("- **Name**: %s\n", r.promptPackName))
		b.WriteString(fmt.Sprintf("- **Source**: %s\n", r.promptPackSource))
		b.WriteString(fmt.Sprintf("- **Digest**: %s\n", r.promptPackDigest))
		b.WriteString(fmt.Sprintf("- **Semantic review**: %s\n", reviewStatus(r.promptPackReview.Approved)))
		for _, finding := range r.promptPackReview.Findings {
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", strings.ToUpper(finding.Severity), finding.Scope, finding.Message))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- [FAIL] prompt pack metadata missing\n\n")
	}

	b.WriteString("## Routing Events\n\n")
	if len(r.routingEvents) == 0 {
		b.WriteString("No routing fallbacks recorded.\n\n")
	} else {
		for _, event := range r.routingEvents {
			b.WriteString(fmt.Sprintf("- [FALLBACK] %s\n", event))
		}
		b.WriteString("\n")
	}

	// Programmatic checks
	b.WriteString("## Programmatic Checks\n\n")
	programmaticErrorCount := swarm.ErrorCount(r.programmatic)
	if programmaticErrorCount == 0 {
		b.WriteString("All checks passed.\n\n")
	} else {
		for _, iss := range r.programmatic {
			icon := "PASS"
			if iss.Severity == "error" {
				icon = "FAIL"
			}
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", icon, iss.File, iss.Problem))
		}
		b.WriteString("\n")
	}

	// Pre-screen gate
	b.WriteString("## Pre-Screen Gate\n\n")
	if r.preScreen != nil {
		b.WriteString(fmt.Sprintf("- **Concrete file flags**: %d\n", len(r.preScreen.ConcreteReasons())))
	}
	if r.preScreen != nil && !r.preScreen.NeedsLLMReview {
		b.WriteString("All critical files passed pre-screening. Adversarial review still ran by policy.\n\n")
	} else if r.preScreen != nil {
		b.WriteString(fmt.Sprintf("Pre-screening flagged %d issues:\n", len(r.preScreen.Reasons)))
		for _, reason := range r.preScreen.Reasons {
			b.WriteString(fmt.Sprintf("- %s\n", reason))
		}
		b.WriteString("- Concrete file flags are routed into adversarial review and targeted revision automatically.\n")
		b.WriteString("\n")
	}

	// Adversarial review
	b.WriteString("## Adversarial Review\n\n")
	verdict := strings.ToUpper(strings.TrimSpace(r.reviewVerdict))
	if verdict == "" {
		verdict = "NOT RUN"
	}
	b.WriteString(fmt.Sprintf("**Verdict**: %s\n\n", verdict))
	if r.reviewOutput != "" && r.reviewVerdict != "approve" {
		b.WriteString(r.reviewOutput)
		b.WriteString("\n\n")
	}

	// Revisions
	if len(r.revisions) > 0 {
		rounds := revisionRoundCount(r.revisions)
		b.WriteString(fmt.Sprintf("## Revisions (%d round(s))\n\n", rounds))
		for _, rev := range r.revisions {
			roundLabel := ""
			if rev.Round > 0 {
				roundLabel = fmt.Sprintf(" [round %d]", rev.Round)
			}
			if rev.Success {
				b.WriteString(fmt.Sprintf("- **%s**%s: revised (%d chars, %v)\n", rev.File, roundLabel, rev.Chars, rev.Duration.Round(time.Second)))
			} else {
				b.WriteString(fmt.Sprintf("- **%s**%s: revision failed\n", rev.File, roundLabel))
			}
		}
		b.WriteString("\n")
	}

	// Post-revision re-screen
	if r.postScreen != nil {
		b.WriteString("## Post-Revision Re-Screen\n\n")
		if !r.postScreen.NeedsLLMReview {
			b.WriteString("All files pass post-revision re-screening.\n\n")
		} else {
			b.WriteString(fmt.Sprintf("Re-screening flagged %d remaining issues:\n", len(r.postScreen.Reasons)))
			for _, reason := range r.postScreen.Reasons {
				b.WriteString(fmt.Sprintf("- %s\n", reason))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Render Parity\n\n")
	if r.renderError != "" {
		b.WriteString(fmt.Sprintf("- [FAIL] %s\n\n", r.renderError))
	} else if swarm.ErrorCount(r.renderParity) == 0 {
		b.WriteString("All selected platform trees match the shared `.tasks` decomposition.\n\n")
	} else {
		for _, iss := range r.renderParity {
			b.WriteString(fmt.Sprintf("- [FAIL] %s: %s\n", iss.File, iss.Problem))
		}
		b.WriteString("\n")
	}

	// Prompt injection warnings
	if len(r.injectionWarnings) > 0 {
		b.WriteString("## Prompt Injection Warnings\n\n")
		b.WriteString(fmt.Sprintf("**%d file(s) contain potential prompt injection patterns.**\n", len(r.injectionWarnings)))
		b.WriteString("These files were NOT modified. Review them before trusting LLM output.\n\n")
		for _, w := range r.injectionWarnings {
			b.WriteString(fmt.Sprintf("- %s\n", w))
		}
		b.WriteString("\n")
	}

	// Cost estimate
	b.WriteString("## Cost Estimate\n\n")
	b.WriteString(fmt.Sprintf("- **Total LLM calls**: %d\n", r.llmCalls))
	b.WriteString(fmt.Sprintf("- **Estimated input tokens**: %d\n", r.inputTokens))
	b.WriteString(fmt.Sprintf("- **Estimated output tokens**: %d\n", r.outputTokens))
	inputCost := float64(r.inputTokens) * 3.0 / 1_000_000
	outputCost := float64(r.outputTokens) * 15.0 / 1_000_000
	b.WriteString(fmt.Sprintf("- **Estimated cost at $3/$15 per MTok (Sonnet)**: $%.4f\n\n", inputCost+outputCost))

	// Summary
	b.WriteString("## Summary\n\n")
	totalErrorCount := programmaticErrorCount + swarm.ErrorCount(r.renderParity)
	decision := "FAIL"
	if totalErrorCount == 0 && validationPassed(r) {
		decision = "PASS"
	}
	b.WriteString(fmt.Sprintf("**Decision**: %s\n\n", decision))
	if r.preScreen != nil && r.preScreen.HasConcreteFlags() {
		b.WriteString("Concrete pre-screen findings were not ignored; they were fed into adversarial review and any unresolved findings would fail the final decision gate.\n")
	} else if r.preScreen != nil && r.preScreen.NeedsLLMReview {
		b.WriteString("Advisory pre-screen findings were treated as reviewer-focus signals, not as standalone approval blockers.\n")
	}

	// Risk Analysis: compounding error across process steps
	skillsPath := filepath.Join(outputDir, "skills.md")
	if skillsBytes, readErr := os.ReadFile(skillsPath); readErr == nil {
		stepCount := countProcessSteps(string(skillsBytes))
		if stepCount > 0 {
			reliability95 := math.Pow(0.95, float64(stepCount))
			reliability99 := math.Pow(0.99, float64(stepCount))
			b.WriteString("\n## Risk Analysis\n\n")
			b.WriteString(fmt.Sprintf("- **Total process steps across all skills**: %d\n", stepCount))
			b.WriteString(fmt.Sprintf("- **Estimated compound reliability at 95%% per-step**: %.1f%%\n", reliability95*100))
			b.WriteString(fmt.Sprintf("- **Estimated compound reliability at 99%% per-step**: %.1f%%\n", reliability99*100))
			if stepCount > 50 {
				b.WriteString("- **Warning**: High step count increases compounding error risk. Consider checkpoints and independent verification at key stages.\n")
			}
			b.WriteString("\n")
		}
	}

	reportPath := filepath.Join(outputDir, "validation-report.md")
	if err := os.WriteFile(reportPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", reportPath, err)
	}
	return nil
}

// countProcessSteps counts numbered process step lines (e.g., "1. ", "2. ")
// in skills content to estimate total pipeline step count.
var processStepRe = regexp.MustCompile(`(?m)^[0-9]+\. `)

func countProcessSteps(skillsContent string) int {
	return len(processStepRe.FindAllString(skillsContent, -1))
}

// dumpValidationReportToWriter writes a compact summary of the validation report
// to the given writer. Used as a last-resort fallback when the report file cannot
// be written to disk.
func dumpValidationReportToWriter(w io.Writer, r *validationReport) {
	if r == nil {
		fmt.Fprintln(w, "(nil report)")
		return
	}
	fmt.Fprintf(w, "verdict=%s programmatic_errors=%d\n", r.reviewVerdict, swarm.ErrorCount(r.programmatic))
	if r.preScreen != nil {
		fmt.Fprintf(w, "prescreen: needs_review=%v concrete_flags=%v reasons=%d\n",
			r.preScreen.NeedsLLMReview, r.preScreen.HasConcreteFlags(), len(r.preScreen.Reasons))
	}
	if r.renderError != "" {
		fmt.Fprintf(w, "render_error: %s\n", r.renderError)
	}
	if len(r.renderParity) > 0 {
		fmt.Fprintf(w, "render_parity_errors: %d\n", swarm.ErrorCount(r.renderParity))
	}
	if len(r.revisions) > 0 {
		for _, rev := range r.revisions {
			fmt.Fprintf(w, "revision: file=%s success=%v\n", rev.File, rev.Success)
		}
	}
	if r.postScreen != nil {
		fmt.Fprintf(w, "postscreen: needs_review=%v concrete_flags=%v\n",
			r.postScreen.NeedsLLMReview, r.postScreen.HasConcreteFlags())
	}
	fmt.Fprintf(w, "passed=%v\n", validationPassed(r))
}

func validationPassed(r *validationReport) bool {
	if r == nil {
		return false
	}
	if r.renderError != "" || swarm.ErrorCount(r.renderParity) > 0 {
		return false
	}
	if swarm.ErrorCount(r.programmatic) > 0 {
		return false
	}
	if r.reviewVerdict == "approve" {
		if r.preScreen.HasConcreteFlags() {
			// Concrete pre-screen findings were not cleared by revision.
			// A postScreen that clears them is required.
			return r.postScreen != nil && !r.postScreen.NeedsLLMReview && !r.postScreen.HasConcreteFlags()
		}
		return r.postScreen == nil || !r.postScreen.NeedsLLMReview
	}
	if r.reviewVerdict != "revise" || len(r.revisions) == 0 {
		return false
	}
	for _, revision := range r.revisions {
		if !revision.Success {
			return false
		}
	}
	return r.postScreen != nil && !r.postScreen.NeedsLLMReview
}

func reviewStatus(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// collectInjectionWarnings extracts prompt injection evidence entries
// and formats them as human-readable warnings for the validation report.
func collectInjectionWarnings(evidence []ingestion.EvidenceEntry) []string {
	var warnings []string
	for _, e := range evidence {
		if e.Category == ingestion.EvidenceCategoryPromptInjection {
			warnings = append(warnings, fmt.Sprintf("%s: %s", e.RelPath, e.Detail))
		}
	}
	return warnings
}

// parseVerdict extracts the required APPROVE/REVISE verdict from cross-check output.
// The critic must return a Verdict section with exactly APPROVE or REVISE on the
// first non-empty line. Anything else is malformed and returns "unknown".
func parseVerdict(output string) string {
	lines := strings.Split(output, "\n")
	inVerdict := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## verdict") || strings.HasPrefix(lower, "### verdict") {
			inVerdict = true
			continue
		}
		if !inVerdict || trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return "unknown"
		}
		switch strings.ToUpper(trimmed) {
		case "APPROVE":
			return "approve"
		case "REVISE":
			return "revise"
		}
		return "unknown"
	}
	return "unknown"
}

// parseFilesForRevision extracts the list of files the adversarial review flagged
// for revision. Looks for "### Files Needing Revision" section and parses "- path: reason" lines.
// Returns an empty slice if the required section is missing; the CLI treats that as malformed review output.
func parseFilesForRevision(reviewOutput string, candidates []string) []string {
	lines := strings.Split(reviewOutput, "\n")
	inSection := false
	candidateSet := make(map[string]string, len(candidates))
	for _, c := range candidates {
		normalized := normalizeRevisionPath(c)
		candidateSet[normalized] = c
		if strings.HasPrefix(normalized, ".tasks/") {
			candidateSet[strings.TrimPrefix(normalized, ".tasks/")] = c
		}
	}

	var files []string
	seen := make(map[string]bool, len(candidates))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "files needing revision") {
			inSection = true
			continue
		}
		// Stop at next heading
		if inSection && strings.HasPrefix(trimmed, "#") {
			break
		}
		if inSection && strings.HasPrefix(trimmed, "- ") {
			// Parse "- path/to/file.md: reason" or "- `path/to/file.md`: reason"
			entry := strings.TrimPrefix(trimmed, "- ")
			path := entry
			if idx := strings.Index(entry, ":"); idx > 0 {
				path = strings.TrimSpace(entry[:idx])
			}
			if canonical, ok := candidateSet[normalizeRevisionPath(path)]; ok && !seen[canonical] {
				files = append(files, canonical)
				seen[canonical] = true
			}
		}
	}
	return files
}

func normalizeRevisionPath(path string) string {
	clean := strings.TrimSpace(path)
	clean = strings.Trim(clean, "`'\"")
	clean = filepath.ToSlash(clean)
	if idx := strings.Index(clean, ".tasks/"); idx >= 0 {
		clean = clean[idx:]
	}
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	return clean
}

// checkInputQualityGate performs a basic sanity check: at least one readable
// file with non-empty content. This catches empty directories and avoids
// wasting an LLM call on literally nothing. Content quality is judged by
// the LLM-based pre-flight validation that runs later in the pipeline.
func checkInputQualityGate(ingested *ingestion.Context, complexity *ingestion.SourceComplexity) error {
	if len(ingested.Files) < minReadableFiles || len(ingested.Summary) == 0 {
		detail := fmt.Sprintf("Found %d readable files with %d chars of content. "+
			"Provide at least %d readable text file with non-empty content.",
			len(ingested.Files), len(ingested.Summary), minReadableFiles)
		ingested.Evidence = append(ingested.Evidence, ingestion.EvidenceEntry{
			Phase:    ingestion.EvidencePhaseIngestion,
			Category: ingestion.EvidenceCategoryInputQualityGate,
			Detail:   detail,
		})
		return fmt.Errorf("Insufficient source material: %s", detail)
	}
	return nil
}

// buildPreFlightPrompt constructs the prompt for the LLM-based pre-flight
// source validation call.
func buildPreFlightPrompt(ingested *ingestion.Context, complexity *ingestion.SourceComplexity) string {
	summary := ingested.Summary
	if len(summary) > preFlightSummaryLimit {
		summary = summary[:preFlightSummaryLimit]
	}

	sectionCount := 0
	depth := "unknown"
	if complexity != nil {
		sectionCount = complexity.SectionCount
		depth = complexity.Depth
	}

	return fmt.Sprintf(`You are evaluating whether source material is sufficient to produce a working AI agent skill bundle.

Source material summary:
- Files: %d (%d bytes)
- Sections/headings: %d
- Depth classification: %s

Source content:
%s

Answer with exactly one of:
SUFFICIENT: The source contains enough domain concepts, requirements, constraints, or data structures to decompose into at least one agent skill with concrete process steps.
INSUFFICIENT: [specific explanation of what is missing and what the user should add]

Do not explain your reasoning beyond the verdict line.`,
		ingested.FileCount, ingested.TotalBytes, sectionCount, depth, summary)
}

// runPreFlightValidation sends one short LLM call to judge whether the source
// material is rich enough to produce useful agent skills. If the LLM returns
// INSUFFICIENT, the run is aborted before the 9-task swarm.
func runPreFlightValidation(exec *executor.Executor, ingested *ingestion.Context, complexity *ingestion.SourceComplexity) error {
	prompt := buildPreFlightPrompt(ingested, complexity)
	resp, err := exec.RunPreFlight(prompt)
	if err != nil {
		// If the pre-flight call itself fails (timeout, LLM error), log a warning
		// and proceed -- we don't want infrastructure failures to block the pipeline.
		fmt.Fprintf(os.Stderr, "WARNING: pre-flight validation call failed: %v (proceeding anyway)\n", err)
		return nil
	}

	output := strings.TrimSpace(resp.Output)
	if strings.HasPrefix(strings.ToUpper(output), "INSUFFICIENT") {
		// Extract the explanation after "INSUFFICIENT:" if present.
		explanation := output
		if idx := strings.Index(output, ":"); idx >= 0 && idx < len(output)-1 {
			explanation = strings.TrimSpace(output[idx+1:])
		}
		detail := fmt.Sprintf("LLM pre-flight verdict: %s", explanation)
		ingested.Evidence = append(ingested.Evidence, ingestion.EvidenceEntry{
			Phase:    ingestion.EvidencePhaseIngestion,
			Category: ingestion.EvidenceCategoryInputQualityGate,
			Detail:   detail,
		})
		return fmt.Errorf("Insufficient source material for skill generation. %s", explanation)
	}

	return nil
}

// sanitizeProjectName strips characters that could cause shell injection or
// markdown rendering issues when interpolated into generated scripts/docs.
func sanitizeProjectName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.', r == ' ':
			b.WriteRune(r)
		default:
			// Drop potentially dangerous characters: $`"';|&(){}[]<>
		}
	}
	result := strings.TrimSpace(b.String())
	if result == "" {
		return "Project"
	}
	return result
}

func selectLLMs(tools []discovery.LLMTool) (discovery.LLMTool, discovery.LLMTool, []string, error) {
	available := make(map[string]discovery.LLMTool)
	for _, t := range tools {
		if t.Available {
			available[t.Name] = t
		}
	}
	if len(available) == 0 {
		return discovery.LLMTool{}, discovery.LLMTool{}, nil,
			fmt.Errorf("no LLM CLI tools found on system")
	}
	if strings.TrimSpace(primaryLLM) == "" {
		return discovery.LLMTool{}, discovery.LLMTool{}, nil,
			fmt.Errorf("--model is required")
	}
	primary, ok := available[primaryLLM]
	if !ok {
		return discovery.LLMTool{}, discovery.LLMTool{}, nil,
			fmt.Errorf("primary LLM %q not found on system", primaryLLM)
	}
	if criticLLM == "" {
		// Try to find a different tool for critic; fall back to same tool if only one available.
		for _, name := range []string{"claude", "gemini", "codex"} {
			if name != primaryLLM {
				if t, exists := available[name]; exists {
					return primary, t, []string{fmt.Sprintf("auto-selected critic provider %q because --critique was not set", t.Name)}, nil
				}
			}
		}
		// Only one tool available — use it for both roles
		return primary, primary, []string{fmt.Sprintf("same-model critique fallback: only %q is available", primary.Name)}, nil
	}
	critic, ok := available[criticLLM]
	if !ok {
		return discovery.LLMTool{}, discovery.LLMTool{}, nil,
			fmt.Errorf("critic LLM %q not found on system", criticLLM)
	}
	if critic.Name == primary.Name {
		return primary, critic, []string{fmt.Sprintf("same-model critique explicitly requested: %q", primary.Name)}, nil
	}
	return primary, critic, nil, nil
}

// repairCitationPaths fixes near-miss citation path hallucinations in generated
// files. LLMs occasionally truncate or mangle directory paths while keeping the
// filename correct (e.g., /mnt/d/function/file.md instead of /mnt/d/github/SwarmMaker/file.md).
// This function replaces any markdown link target whose basename matches a known
// source file but whose full path is wrong.
func repairCitationPaths(filePath string, knownPaths []string) (int, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return 0, err
	}

	// Build a map: basename -> correct full path
	byBasename := make(map[string]string, len(knownPaths))
	for _, p := range knownPaths {
		base := filepath.Base(p)
		byBasename[base] = p
	}

	linkRe := regexp.MustCompile(`\]\(([^)]+)\)`)
	repaired := 0
	result := linkRe.ReplaceAllStringFunc(string(content), func(match string) string {
		sub := linkRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		target := sub[1]
		base := filepath.Base(target)
		if correct, ok := byBasename[base]; ok && target != correct {
			repaired++
			return "]("+correct+")"
		}
		return match
	})

	if repaired > 0 {
		if err := os.WriteFile(filePath, []byte(result), 0644); err != nil {
			return repaired, err
		}
	}
	return repaired, nil
}

// buildKnownCitationPaths returns the set of absolute paths that are valid
// citation targets: source input files + evidence.json + manifest.json.
func buildKnownCitationPaths(inputDir, outputDir string) []string {
	var paths []string
	// Source files from input directory
	_ = filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		paths = append(paths, filepath.Clean(path))
		return nil
	})
	// Evidence and manifest in output
	for _, name := range []string{"evidence.json", "manifest.json"} {
		p := filepath.Join(outputDir, ".tasks", name)
		paths = append(paths, filepath.Clean(p))
	}
	return paths
}
