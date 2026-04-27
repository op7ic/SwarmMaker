// complexity.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Source complexity analysis.
// Analyzes ingested source material to classify depth (shallow/moderate/deep)
// based on section count, character length, numerical density, and list item
// count. The depth classification drives pre-screening thresholds -- deeper
// sources trigger stricter citation density and dimension coverage checks.


package ingestion

import (
	"fmt"
	"regexp"
	"strings"
)

// SourceComplexity captures metrics about source material that drive
// Adaptive Depth Reasoning (ADR) in prompt generation and validation.
// All fields are computed from the source text — no LLM calls required.
type SourceComplexity struct {
	SectionCount     int     // ## or ### headings
	DimensionCount   int     // numbered dimensions/modules (e.g. "Dimension 1", "Dim 5")
	AppendixCount    int     // appendix sections (e.g. "Appendix A")
	NumericalDensity float64 // numeric tokens per 1000 chars
	ListItemCount    int     // bullet or numbered list items
	SourceLength     int     // total characters
	Depth            string  // "shallow" | "moderate" | "deep"
}

// dimension patterns: "Dimension 1", "Dim 1", "Dim. 1", "Dimension #1"
var dimPattern = regexp.MustCompile(`(?im)^\s*#{0,4}\s*(?:dimension|dim)\.?\s*#?\s*(\d+)`)

// appendix patterns: "Appendix A", "## Appendix B", "APPENDIX C"
var appendixPattern = regexp.MustCompile(`(?im)^\s*#{0,4}\s*appendix\s+([A-Z])`)

// heading patterns: ## or ### at line start
var headingPattern = regexp.MustCompile(`(?m)^#{2,3}\s+\S`)

// number tokens: standalone numbers (not inside words)
var numberPattern = regexp.MustCompile(`\b\d+(?:\.\d+)?%?\b`)

// list items: "- item", "* item", "1. item", "2) item"
var listItemPattern = regexp.MustCompile(`(?m)^\s*(?:[-*]|\d+[.)]\s)\s*\S`)

// AnalyzeComplexity computes source metrics from raw text.
// These metrics drive prompt depth (ADR) and validation thresholds.
func AnalyzeComplexity(source string) *SourceComplexity {
	c := &SourceComplexity{
		SourceLength: len(source),
	}

	c.SectionCount = len(headingPattern.FindAllString(source, -1))

	// Deduplicate dimensions by number (same dimension may be referenced multiple times)
	dimMatches := dimPattern.FindAllStringSubmatch(source, -1)
	dimSeen := make(map[string]bool)
	for _, m := range dimMatches {
		if len(m) > 1 {
			dimSeen[m[1]] = true
		}
	}
	c.DimensionCount = len(dimSeen)

	// Deduplicate appendices by letter
	appMatches := appendixPattern.FindAllStringSubmatch(source, -1)
	appSeen := make(map[string]bool)
	for _, m := range appMatches {
		if len(m) > 1 {
			appSeen[m[1]] = true
		}
	}
	c.AppendixCount = len(appSeen)

	// Numerical density
	numCount := len(numberPattern.FindAllString(source, -1))
	if c.SourceLength > 0 {
		c.NumericalDensity = float64(numCount) / (float64(c.SourceLength) / 1000.0)
	}

	c.ListItemCount = len(listItemPattern.FindAllString(source, -1))

	// Classify depth
	switch {
	case c.SourceLength > 30000 || c.SectionCount > 15 || c.DimensionCount > 5:
		c.Depth = "deep"
	case c.SourceLength > 10000 || c.SectionCount > 5:
		c.Depth = "moderate"
	default:
		c.Depth = "shallow"
	}

	return c
}

// FormatHints produces an instruction preamble for LLM prompts based on source
// complexity. This is the core of Adaptive Depth Reasoning: the prompt adapts
// to what's actually in the source material, with verified counts the LLM can't
// hallucinate because we computed them mechanically.
func (c *SourceComplexity) FormatHints() string {
	if c == nil {
		return ""
	}

	var b strings.Builder

	b.WriteString("SOURCE MATERIAL ANALYSIS (computed by automated scan — these counts are verified):\n")
	b.WriteString(fmt.Sprintf("- Source length: %d characters\n", c.SourceLength))
	b.WriteString(fmt.Sprintf("- Named sections/headings: %d\n", c.SectionCount))

	if c.DimensionCount > 0 {
		b.WriteString(fmt.Sprintf("- Numbered dimensions/modules: %d — Cover all %d because the validation pipeline checks dimension coverage and flags gaps.\n",
			c.DimensionCount, c.DimensionCount))
	}
	if c.AppendixCount > 0 {
		b.WriteString(fmt.Sprintf("- Appendices: %d — Reproduce or create tasks for each appendix because the validation pipeline checks appendix coverage.\n", c.AppendixCount))
	}
	if c.NumericalDensity > 5.0 {
		b.WriteString("- Source is numerically dense. Preserve all specific numbers, thresholds, and formulas exactly as stated because the review checks numerical fidelity.\n")
	}
	if c.ListItemCount > 20 {
		b.WriteString(fmt.Sprintf("- Source contains %d+ enumerated items. Preserve each one rather than summarizing because the review checks item coverage.\n", c.ListItemCount))
	}

	b.WriteString("\n")
	return b.String()
}
