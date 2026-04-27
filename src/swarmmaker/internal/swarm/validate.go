// validate.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Pre-screening validation heuristics.
// Implements depth-adaptive validation that runs before the expensive LLM
// adversarial review. Checks citation density (sub-linear scaling for long
// documents), fabrication pattern detection, boilerplate injection detection,
// amplification ratio, gap acknowledgment analysis, and dimension coverage.
// Adapts thresholds based on source depth: shallow sources get lenient checks,
// deep sources get strict citation and length requirements.


package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/op7ic/swarmmaker/internal/ingestion"
)

// Issue represents a validation problem found in generated output.
type Issue struct {
	File     string // relative path of the problematic file
	Problem  string // description of the issue
	Severity string // "error" or "warning"
}

// citation pattern: "Source: [...]" or "Source: SECTION NAME" or "Source: `backtick`"
// Codex uses inline prose citations like "Source: DIMENSION 1" without brackets,
// and sometimes wraps section names in backticks: "Source: `DIMENSION 1`".
// We match "Source:" followed by "[", an uppercase word, or a backtick.
var citationRe = regexp.MustCompile("(?im)Sources?:\\s*(?:\\[|[A-Z]|`)")

// gapAcknowledgments — phrases that indicate the LLM is acknowledging gaps instead of
// fabricating. A POSITIVE signal of restraint. Checked case-insensitively as substrings.
// We use simple substring matching instead of a rigid regex because LLMs express gaps
// in many ways: "Not specified in source material", "Not mentioned in the notes",
// "No information available", "N/A", etc.
var gapAcknowledgments = []string{
	"not specified",
	"not mentioned",
	"not provided",
	"not described",
	"not defined",
	"not addressed",
	"not included in source",
	"no information",
	"n/a",
}

// dimension reference pattern: "Dimension N" or "Dim N" in generated output
var dimRefRe = regexp.MustCompile(`(?i)(?:dimension|dim)\.?\s*#?\s*(\d+)`)

// processStepRe matches numbered steps (e.g., "1. ", "2. ") or "## Process" headings.
var processStepRe = regexp.MustCompile(`(?m)(?:^\s*\d+\.\s|^#{1,3}\s*Process)`)

// constraintRe matches constraint indicators in skills output.
var constraintRe = regexp.MustCompile(`(?i)(?:MUST\s+DO|MUST\s+NOT|Required|Prohibited|Constraints)`)

// triggerRe matches invocation trigger patterns in skills output.
var triggerRe = regexp.MustCompile(`(?i)(?:When\s+to\s+Invoke|trigger\s+condition)`)

// coordinationRe matches coordination protocol indicators in agents output.
var coordinationRe = regexp.MustCompile(`(?i)(?:Coordination\s+Protocol|Handoff)`)

// errorHandlingRe matches error/failure handling indicators in agents output.
var errorHandlingRe = regexp.MustCompile(`(?i)(?:Error\s+Handling|(?:error|failure)\s+handling)`)

// firstPersonRe matches first-person voice at sentence boundaries.
var firstPersonRe = regexp.MustCompile(`(?m)(?:^|[.!?]\s+)(?:I |I'll |My |We )`)

// allCapsWordRe matches words of 5+ uppercase letters.
var allCapsWordRe = regexp.MustCompile(`\b[A-Z]{5,}\b`)

// allCapsWhitelist — ALL-CAPS words that are acceptable and should not be flagged.
var allCapsWhitelist = map[string]bool{
	"UNKNOWN": true, "OODA": true,
	"API": true, "JSON": true, "HTTP": true, "HTTPS": true,
	"HTML": true, "REST": true, "YAML": true, "UUID": true,
	"ASCII": true, "UTF": true, "STDIN": true, "STDOUT": true,
	"STDERR": true, "OAUTH": true, "SAML": true, "CRUD": true,
	"README": true, "REVIEW": true, "CHECKLIST": true,
}

// skillBlockRe matches skill block headings.
var skillBlockRe = regexp.MustCompile(`(?m)^#{1,3}\s+Skill:\s*(.+)`)

// agentBlockRe matches agent block headings.
var agentBlockRe = regexp.MustCompile(`(?m)^#{1,3}\s+Agent:\s*(.+)`)

// fabrication signal patterns — things LLMs commonly invent
var fabricationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:curl|wget)\s+(?:https?://)`),              // fabricated curl commands
	regexp.MustCompile(`\b[A-Z]{2,5}\b\s+(?:ticker|symbol|stock)\b`),     // fabricated tickers: case-sensitive so "are ticker" doesn't match
	regexp.MustCompile(`(?i)(?:example\.com|placeholder\.io|acme\.)`),    // placeholder domains
	regexp.MustCompile(`(?i)(?:90%|95%|99%)\s*(?:test|code)\s*coverage`), // invented coverage targets
	regexp.MustCompile(`(?i)\bv?\d+\.\d+\.\d+\b`),                        // specific semver versions rarely in notes
}

// boilerplatePatterns — phrases LLMs inject into every plan regardless of source.
// These are fine when the source mentions them. Suspicious when the source doesn't.
// Covers: infrastructure, testing, methodology, and architecture patterns.
var boilerplatePatterns = []string{
	// Infrastructure
	"comprehensive test suite",
	"ci/cd pipeline",
	"containeriz", // containerize, containerized, containerization
	"microservices",
	"horizontal scaling",
	"load balancing",
	"monitoring and alerting",
	"rate limiting",
	"caching layer",
	"message queue",
	// Testing/methodology (LLMs love to prescribe these)
	"unit tests, integration tests",
	"end-to-end test",
	"code review",
	"pair programming",
	"agile sprint",
	"test-driven development",
	// Architecture patterns
	"event-driven architecture",
	"domain-driven design",
	"clean architecture",
}

// PreScreenResult holds the output of the smart pre-screening gate.
// If NeedsLLMReview is false, the pre-screen found no heuristic flags. The CLI
// still runs adversarial review by policy.
type PreScreenResult struct {
	NeedsLLMReview bool                // true if any signal suggests LLM review is warranted
	Reasons        []string            // why review focus is needed (empty = no pre-screen flags)
	FileFlags      map[string][]string // per-file flags (file path -> reasons)
}

// PreScreenFiles runs depth-adaptive programmatic checks on generated files to decide
// whether an expensive LLM adversarial review is warranted.
//
// Key design decisions:
//
//   - Shallow sources ALWAYS trigger review. The risk of over-generation (LLM invents
//     90% of the plan from sparse notes) is too high to ignore. Pre-screen findings still
//     guide the reviewer on what to investigate.
//
//   - Moderate/deep sources use stricter citation and coverage checks. Clean outputs
//     still go through adversarial review; the flags only scope reviewer attention.
//
//   - Checks adapt to source depth:
//     Shallow: amplification ratio, gap acknowledgments, boilerplate injection
//     Deep: dimension coverage, citation density, content depth
//     Universal: fabrication patterns, file readability
//
// sourceText is the ingested manifest (what the LLM saw). Used for amplification
// ratio checks and to verify whether boilerplate terms actually appear in the source.
func PreScreenFiles(outputDir string, criticalFiles []string, complexity *ingestion.SourceComplexity, sourceText string) *PreScreenResult {
	result := &PreScreenResult{
		FileFlags: make(map[string][]string),
	}

	sourceLower := strings.ToLower(sourceText)
	sourceLen := len(sourceText)

	for _, f := range criticalFiles {
		absPath := filepath.Join(outputDir, f)
		content, err := os.ReadFile(absPath)
		if err != nil {
			result.addFlag(f, "file unreadable")
			continue
		}
		text := string(content)
		textLower := strings.ToLower(text)

		// --- UNIVERSAL CHECKS (all source depths) ---

		// Fabrication signal scan
		for _, pat := range fabricationPatterns {
			matches := pat.FindAllString(text, -1)
			for _, m := range matches {
				// Only flag if the fabricated term isn't in the source
				if !strings.Contains(sourceText, m) {
					result.addFlag(f, fmt.Sprintf("fabrication signal: %q (not in source)", m))
					break // one flag per pattern per file is enough
				}
			}
		}

		// --- SHALLOW SOURCE CHECKS (main risk: over-generation) ---
		if complexity != nil && complexity.Depth == "shallow" {

			// Amplification check: if a single output file is much longer than the
			// entire source, most of its content was invented.
			if sourceLen > 0 && len(text) > sourceLen*4 {
				result.addFlag(f, fmt.Sprintf("high amplification: %d char output from %d char source (%.1fx)",
					len(text), sourceLen, float64(len(text))/float64(sourceLen)))
			}

			// Gap acknowledgment health: a well-calibrated LLM acknowledges gaps in shallow sources.
			// If the output fills every section with content and NEVER acknowledges missing info,
			// it's likely fabricating to fill gaps.
			hasGapAck := false
			for _, ack := range gapAcknowledgments {
				if strings.Contains(textLower, ack) {
					hasGapAck = true
					break
				}
			}
			if !hasGapAck && len(text) > 1000 {
				result.addFlag(f, "no gap acknowledgments in shallow-source output (possible over-generation)")
			}

			// Boilerplate injection: check if output contains enterprise boilerplate
			// that doesn't appear in the source notes.
			boilerplateHits := 0
			for _, bp := range boilerplatePatterns {
				if strings.Contains(textLower, bp) && !strings.Contains(sourceLower, bp) {
					boilerplateHits++
				}
			}
			if boilerplateHits >= 3 {
				result.addFlag(f, fmt.Sprintf("boilerplate injection: %d terms not in source", boilerplateHits))
			}

			// Lenient citation check: for shallow sources, just need SOME citations (≥1).
			citations := citationRe.FindAllString(text, -1)
			if len(citations) == 0 && len(text) > 500 {
				result.addFlag(f, "zero citations in shallow-source output")
			}
		}

		// --- MODERATE SOURCE CHECKS ---
		if complexity != nil && complexity.Depth == "moderate" {

			citations := citationRe.FindAllString(text, -1)
			expectedCitations := len(text) / 1000
			if expectedCitations < 2 {
				expectedCitations = 2
			}
			if len(citations) < expectedCitations {
				result.addFlag(f, fmt.Sprintf("low citation density: %d citations in %d chars (expect ~%d)",
					len(citations), len(text), expectedCitations))
			}

			boilerplateHits := 0
			for _, bp := range boilerplatePatterns {
				if strings.Contains(textLower, bp) && !strings.Contains(sourceLower, bp) {
					boilerplateHits++
				}
			}
			if boilerplateHits >= 4 {
				result.addFlag(f, fmt.Sprintf("boilerplate injection: %d terms not in source", boilerplateHits))
			}
		}

		// --- DEEP SOURCE CHECKS (main risk: omission) ---
		if complexity != nil && complexity.Depth == "deep" {

			if complexity.DimensionCount > 0 {
				dimRefs := dimRefRe.FindAllStringSubmatch(text, -1)
				dimsSeen := make(map[string]bool)
				for _, m := range dimRefs {
					if len(m) > 1 {
						dimsSeen[m[1]] = true
					}
				}
				if len(dimsSeen) < complexity.DimensionCount {
					result.addFlag(f, fmt.Sprintf("dimension gap: found %d/%d dimensions referenced",
						len(dimsSeen), complexity.DimensionCount))
				}
			}

			citations := citationRe.FindAllString(text, -1)
			// Sub-linear scaling: first 5000 chars expect 1 per 500 chars (10),
			// remaining chars expect 1 per 1000 chars. This prevents unreasonably
			// high expectations for long documents where prose naturally grows
			// between citation clusters.
			expectedCitations := 10 // base for first 5000 chars
			if len(text) > 5000 {
				expectedCitations += (len(text) - 5000) / 1000
			}
			if expectedCitations < 3 {
				expectedCitations = 3
			}
			if len(citations) < expectedCitations {
				result.addFlag(f, fmt.Sprintf("low citation density: %d citations in %d chars (expect ~%d)",
					len(citations), len(text), expectedCitations))
			}

			if len(text) < 1500 {
				result.addFlag(f, fmt.Sprintf("suspiciously short for deep source: %d chars", len(text)))
			}
		}

		// --- NIL COMPLEXITY FALLBACK ---
		if complexity == nil {
			citations := citationRe.FindAllString(text, -1)
			if len(citations) == 0 && len(text) > 500 {
				result.addFlag(f, "zero citations (no complexity data available)")
			}
		}

		// --- OPERATIONAL DEPTH ADVISORY CHECKS (moderate/deep only) ---
		if complexity != nil && (complexity.Depth == "moderate" || complexity.Depth == "deep") {
			base := filepath.Base(f)

			if base == "skills.md" {
				if !processStepRe.MatchString(text) {
					result.addAdvisory(f, "missing procedural steps (no numbered process found)")
				}
				if !constraintRe.MatchString(text) {
					result.addAdvisory(f, "missing constraints (no Required/Prohibited/Constraints found)")
				}
				if !triggerRe.MatchString(text) {
					result.addAdvisory(f, "missing trigger conditions (no When to Invoke found)")
				}
			}

			if base == "agents.md" {
				if !coordinationRe.MatchString(text) {
					result.addAdvisory(f, "missing coordination protocol (no Coordination Protocol/Handoff found)")
				}
				if !errorHandlingRe.MatchString(text) {
					result.addAdvisory(f, "missing error handling (no Error Handling section found)")
				}
			}
		}

		// --- ANTI-PATTERN ADVISORY CHECKS (all source depths) ---
		base := filepath.Base(f)

		if base == "skills.md" {
			// First-person voice check
			if firstPersonRe.MatchString(text) {
				result.addAdvisory(f, "first-person voice detected (use imperative or \"The agent will...\")")
			}

			// Per-skill "When to Invoke" section check
			skillBlocks := skillBlockRe.FindAllStringIndex(text, -1)
			for i, loc := range skillBlocks {
				end := len(text)
				if i+1 < len(skillBlocks) {
					end = skillBlocks[i+1][0]
				}
				blockText := text[loc[0]:end]
				nameMatch := skillBlockRe.FindStringSubmatch(blockText)
				skillName := "unknown"
				if len(nameMatch) > 1 {
					skillName = strings.TrimSpace(nameMatch[1])
				}
				if !triggerRe.MatchString(blockText) {
					result.addAdvisory(f, fmt.Sprintf("skill %q missing \"When to Invoke\" section", skillName))
				}
			}

			// Excessive ALL-CAPS check
			capsMatches := allCapsWordRe.FindAllString(text, -1)
			nonWhitelisted := 0
			for _, w := range capsMatches {
				if !allCapsWhitelist[w] {
					nonWhitelisted++
				}
			}
			if nonWhitelisted >= 5 {
				result.addAdvisory(f, fmt.Sprintf("excessive ALL-CAPS: %d non-standard uppercase words", nonWhitelisted))
			}
		}

		if base == "agents.md" {
			// Per-agent coordination/handoff section check
			agentBlocks := agentBlockRe.FindAllStringIndex(text, -1)
			for i, loc := range agentBlocks {
				end := len(text)
				if i+1 < len(agentBlocks) {
					end = agentBlocks[i+1][0]
				}
				blockText := text[loc[0]:end]
				nameMatch := agentBlockRe.FindStringSubmatch(blockText)
				agentName := "unknown"
				if len(nameMatch) > 1 {
					agentName = strings.TrimSpace(nameMatch[1])
				}
				if !coordinationRe.MatchString(blockText) {
					result.addAdvisory(f, fmt.Sprintf("agent %q missing \"Coordination Protocol\" or \"Handoff\" section", agentName))
				}
			}
		}
	}

	// Shallow sources: ALWAYS review regardless of per-file flags.
	// The risk of over-generation from sparse notes is too high to ignore.
	// Pre-screen findings still inform the reviewer what to investigate.
	if complexity != nil && complexity.Depth == "shallow" && !result.NeedsLLMReview {
		result.NeedsLLMReview = true
		result.Reasons = append(result.Reasons, "shallow source: always review (high fabrication risk)")
	}

	return result
}

func (r *PreScreenResult) addFlag(file, reason string) {
	r.FileFlags[file] = append(r.FileFlags[file], reason)
	r.Reasons = append(r.Reasons, fmt.Sprintf("%s: %s", file, reason))
	r.NeedsLLMReview = true
}

// addAdvisory adds a non-blocking advisory reason. It informs the reviewer
// but does NOT appear in FileFlags, so HasConcreteFlags remains unaffected.
func (r *PreScreenResult) addAdvisory(file, reason string) {
	r.Reasons = append(r.Reasons, fmt.Sprintf("[advisory] %s: %s", file, reason))
	r.NeedsLLMReview = true
}

// FlaggedFiles returns the list of files that have pre-screening flags, sorted
// for deterministic ordering across runs.
func (r *PreScreenResult) FlaggedFiles() []string {
	files := make([]string, 0, len(r.FileFlags))
	for f := range r.FileFlags {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// HasConcreteFlags reports whether the pre-screen found concrete per-file
// problems. Advisory policy reasons such as "always review shallow sources" do
// not count as concrete flags.
func (r *PreScreenResult) HasConcreteFlags() bool {
	return r != nil && len(r.FileFlags) > 0
}

// ConcreteReasons returns only concrete per-file reasons in deterministic order.
func (r *PreScreenResult) ConcreteReasons() []string {
	if r == nil {
		return nil
	}
	files := r.FlaggedFiles()
	reasons := make([]string, 0, len(r.Reasons))
	for _, file := range files {
		for _, reason := range r.FileFlags[file] {
			reasons = append(reasons, fmt.Sprintf("%s: %s", file, reason))
		}
	}
	return reasons
}

// ErrorCount returns the number of issues with "error" severity.
func ErrorCount(issues []Issue) int {
	n := 0
	for _, iss := range issues {
		if iss.Severity == "error" {
			n++
		}
	}
	return n
}

// CountGapAcknowledgments returns the number of gap-acknowledgment phrases found
// in text (case-insensitive). Exported for testing.
func CountGapAcknowledgments(text string) int {
	lower := strings.ToLower(text)
	count := 0
	for _, ack := range gapAcknowledgments {
		if strings.Contains(lower, ack) {
			count++
		}
	}
	return count
}
