// synthesizer.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tool synthesis decision engine.
// Determines whether helper tool generation is mandatory, forbidden, or
// unknown based on source evidence. Resolves tool implementation language
// using intersection of supported/hinted languages with explicit preference
// ordering. No LLM calls -- this is deterministic planning.


package toolsynthesis

import (
	"fmt"
	"strings"
)

// Synthesizer resolves tool-generation decisions without invoking any LLM.
type Synthesizer struct{}

// NewSynthesizer returns the stdlib-only synthesis decision engine.
func NewSynthesizer() Synthesizer {
	return Synthesizer{}
}

// Build converts requests into a deterministic plan.
func (Synthesizer) Build(requests []Request) (Plan, error) {
	ordered := sortedRequests(requests)
	plan := Plan{Tools: make([]ToolPlan, 0, len(ordered))}
	for _, request := range ordered {
		tool, err := buildToolPlan(request)
		if err != nil {
			return Plan{}, fmt.Errorf("tool %q: %w", request.Name, err)
		}
		plan.Tools = append(plan.Tools, tool)
	}
	if err := ValidatePlan(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func buildToolPlan(request Request) (ToolPlan, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return ToolPlan{}, fmt.Errorf("name is required")
	}
	consumerPaths, err := normalizePaths(request.ConsumerPaths)
	if err != nil {
		return ToolPlan{}, fmt.Errorf("consumer paths: %w", err)
	}
	evidencePaths, err := normalizePaths(request.EvidencePaths)
	if err != nil {
		return ToolPlan{}, fmt.Errorf("evidence paths: %w", err)
	}

	switch request.Decision {
	case DecisionForbidden:
		return ToolPlan{
			Name:          name,
			Decision:      DecisionForbidden,
			Language:      LanguageUnknown,
			Generated:     false,
			ConsumerPaths: consumerPaths,
			EvidencePaths: evidencePaths,
			Purpose:       request.Purpose,
			Reason:        "tool generation is forbidden",
		}, nil
	case DecisionUnknown:
		return ToolPlan{
			Name:          name,
			Decision:      DecisionUnknown,
			Language:      LanguageUnknown,
			Generated:     false,
			ConsumerPaths: consumerPaths,
			EvidencePaths: evidencePaths,
			Purpose:       request.Purpose,
			Reason:        "insufficient evidence to justify tool generation",
		}, nil
	case DecisionMandatory:
		if len(consumerPaths) == 0 {
			return ToolPlan{}, fmt.Errorf("consumer paths are required for mandatory tool generation")
		}
		if len(evidencePaths) == 0 {
			return ToolPlan{}, fmt.Errorf("proof-of-use evidence is required for mandatory tool generation")
		}
		language := ResolveLanguage(request.LanguageInput)
		if language == LanguageUnknown {
			return ToolPlan{}, fmt.Errorf("language could not be resolved")
		}
		return ToolPlan{
			Name:          name,
			Decision:      DecisionMandatory,
			Language:      language,
			Generated:     true,
			ConsumerPaths: consumerPaths,
			EvidencePaths: evidencePaths,
			Purpose:       request.Purpose,
			Reason:        "mandatory tool generation was justified",
		}, nil
	default:
		return ToolPlan{}, fmt.Errorf("unknown decision %q", request.Decision)
	}
}

// ResolveLanguage picks a language from explicit evidence only.
func ResolveLanguage(input LanguageSelectionInput) Language {
	supported := normalizeLanguages(input.SupportedLanguages)
	preferred := normalizeLanguages(input.PreferredLanguages)
	hints := normalizeLanguages(input.LanguageHints)

	candidates := supported
	if len(hints) > 0 {
		candidates = intersectLanguages(supported, hints)
	}
	if len(candidates) == 0 {
		return LanguageUnknown
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	for _, preferredLanguage := range preferred {
		if containsLanguage(candidates, preferredLanguage) {
			return preferredLanguage
		}
	}
	return LanguageUnknown
}

func intersectLanguages(left, right []Language) []Language {
	allowed := make(map[Language]struct{}, len(right))
	for _, language := range right {
		allowed[language] = struct{}{}
	}
	out := make([]Language, 0, len(left))
	for _, language := range left {
		if _, ok := allowed[language]; ok {
			out = append(out, language)
		}
	}
	return out
}

func containsLanguage(values []Language, want Language) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
