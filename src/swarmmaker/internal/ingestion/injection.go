// injection.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Prompt injection detection for ingested source material.
// Scans file content for common injection patterns that could manipulate
// LLM behavior. Records evidence without modifying content -- flagged
// files are left intact for human review.


package ingestion

import (
	"strings"
)

// EvidenceCategoryPromptInjection is the evidence category for detected
// prompt injection patterns in source files.
const EvidenceCategoryPromptInjection EvidenceCategory = "prompt_injection_detected"

// injectionPatterns are common prompt injection phrases that attempt to
// override or hijack LLM instructions. Matching is case-insensitive.
var injectionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"disregard previous",
	"forget your instructions",
	"new instructions:",
	"system prompt:",
	"you are now",
	"act as if",
	"pretend you are",
}

// InjectionMatch records a single prompt injection pattern found in a file.
type InjectionMatch struct {
	RelPath string
	AbsPath string
	Pattern string
}

// ScanForInjection checks all ingested files for prompt injection patterns.
// It returns evidence entries for each match found. Content is never modified.
func ScanForInjection(ctx *Context) []EvidenceEntry {
	var evidence []EvidenceEntry
	for _, f := range ctx.Files {
		lower := strings.ToLower(f.Content)
		for _, pattern := range injectionPatterns {
			if strings.Contains(lower, pattern) {
				evidence = append(evidence, EvidenceEntry{
					Phase:    EvidencePhaseIngestion,
					Category: EvidenceCategoryPromptInjection,
					RelPath:  f.RelPath,
					AbsPath:  f.AbsPath,
					FileType: f.FileType,
					Size:     f.Size,
					Detail:   "prompt injection pattern detected: " + pattern,
				})
			}
		}
	}
	return evidence
}
