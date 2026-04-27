// types.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tool synthesis type definitions.
// Defines Decision (mandatory/forbidden/unknown), Language types, and the
// Request/ToolPlan/Plan structures used to determine whether helper tools
// should be generated as part of the skill bundle.


// Package toolsynthesis is scaffolding for planned helper-tool generation.
// It defines the decision engine and types for determining whether helper tools
// should be generated as part of a skill bundle. Currently no production code
// calls into this package; it will be wired into the pipeline once tool-building
// is promoted from experimental to supported.
package toolsynthesis

import (
	"sort"
	"strings"
)

// Decision captures the tool-generation outcome required by upstream analysis.
type Decision string

const (
	DecisionMandatory Decision = "mandatory"
	DecisionForbidden Decision = "forbidden"
	DecisionUnknown   Decision = "unknown"
)

// Language is the implementation language for a generated helper tool.
type Language string

const (
	LanguageUnknown Language = "UNKNOWN"
	LanguageGo      Language = "go"
	LanguagePython  Language = "python"
	LanguageShell   Language = "shell"
)

// LanguageSelectionInput defines the explicit evidence used to choose a tool language.
type LanguageSelectionInput struct {
	SupportedLanguages []Language
	PreferredLanguages []Language
	LanguageHints      []Language
}

// Request describes one tool-synthesis decision.
type Request struct {
	Name          string
	Decision      Decision
	LanguageInput LanguageSelectionInput
	ConsumerPaths []string
	EvidencePaths []string
	Purpose       string
}

// ToolPlan is the validated outcome for a single tool request.
type ToolPlan struct {
	Name          string
	Decision      Decision
	Language      Language
	Generated     bool
	ConsumerPaths []string
	EvidencePaths []string
	Purpose       string
	Reason        string
}

// Plan is the deterministic synthesis result.
type Plan struct {
	Tools []ToolPlan
}

func normalizeLanguages(values []Language) []Language {
	out := make([]Language, 0, len(values))
	seen := make(map[Language]struct{}, len(values))
	for _, value := range values {
		value = normalizeLanguage(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeLanguage(value Language) Language {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case "go":
		return LanguageGo
	case "python":
		return LanguagePython
	case "shell":
		return LanguageShell
	case "unknown":
		return LanguageUnknown
	default:
		return Language("")
	}
}

func sortedRequests(requests []Request) []Request {
	out := append([]Request(nil), requests...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return string(out[i].Decision) < string(out[j].Decision)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
