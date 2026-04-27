// toolsynthesis_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for tool synthesis.
// Covers mandatory/forbidden/unknown build decisions, language resolution
// with preference ordering, plan validation for valid and invalid inputs,
// and edge cases (no preferences, case normalization, no overlap).


package toolsynthesis

import (
	"strings"
	"testing"
)

func TestSynthesizerRejectsGuessedLanguage(t *testing.T) {
	synthesizer := NewSynthesizer()

	_, err := synthesizer.Build([]Request{
		{
			Name:     "fetch-api",
			Decision: DecisionMandatory,
			LanguageInput: LanguageSelectionInput{
				SupportedLanguages: []Language{LanguagePython, LanguageGo},
			},
			ConsumerPaths: []string{"docs/agent.md"},
			EvidencePaths: []string{"docs/spec.md"},
			Purpose:       "Fetch API data for the swarm",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "language could not be resolved") {
		t.Fatalf("expected unresolved-language failure, got %v", err)
	}
}

func TestValidatePlanFailsForUnusedGeneratedTool(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "fetch-api",
				Decision:      DecisionMandatory,
				Language:      LanguageGo,
				Generated:     true,
				ConsumerPaths: []string{"docs/agent.md"},
				EvidencePaths: nil,
				Purpose:       "Fetch API data for the swarm",
			},
		},
	}

	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "proof-of-use evidence") {
		t.Fatalf("expected proof-of-use validation failure, got %v", err)
	}
}

func TestResolveLanguageUsesExplicitPreferenceOrder(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageGo, LanguagePython, LanguageShell},
		PreferredLanguages: []Language{LanguagePython, LanguageGo},
	})
	if got != LanguagePython {
		t.Fatalf("expected explicit preference to win, got %q", got)
	}
}

// --- Build tests ---

func TestBuildSuccessfulMandatoryTool(t *testing.T) {
	s := NewSynthesizer()
	plan, err := s.Build([]Request{
		{
			Name:     "data-fetcher",
			Decision: DecisionMandatory,
			LanguageInput: LanguageSelectionInput{
				SupportedLanguages: []Language{LanguageGo, LanguagePython},
				PreferredLanguages: []Language{LanguageGo},
			},
			ConsumerPaths: []string{"cmd/main.go"},
			EvidencePaths: []string{"docs/spec.md"},
			Purpose:       "Fetch data from remote API",
		},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("expected 1 tool plan, got %d", len(plan.Tools))
	}
	tp := plan.Tools[0]
	if tp.Name != "data-fetcher" {
		t.Errorf("Name = %q, want %q", tp.Name, "data-fetcher")
	}
	if tp.Decision != DecisionMandatory {
		t.Errorf("Decision = %q, want %q", tp.Decision, DecisionMandatory)
	}
	if tp.Language != LanguageGo {
		t.Errorf("Language = %q, want %q", tp.Language, LanguageGo)
	}
	if !tp.Generated {
		t.Error("Generated = false, want true")
	}
	if len(tp.ConsumerPaths) != 1 || tp.ConsumerPaths[0] != "cmd/main.go" {
		t.Errorf("ConsumerPaths = %v, want [cmd/main.go]", tp.ConsumerPaths)
	}
	if len(tp.EvidencePaths) != 1 || tp.EvidencePaths[0] != "docs/spec.md" {
		t.Errorf("EvidencePaths = %v, want [docs/spec.md]", tp.EvidencePaths)
	}
	if tp.Purpose != "Fetch data from remote API" {
		t.Errorf("Purpose = %q, want %q", tp.Purpose, "Fetch data from remote API")
	}
}

func TestBuildForbiddenTool(t *testing.T) {
	s := NewSynthesizer()
	plan, err := s.Build([]Request{
		{
			Name:     "dangerous-tool",
			Decision: DecisionForbidden,
			Purpose:  "Should not be generated",
		},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if len(plan.Tools) != 1 {
		t.Fatalf("expected 1 tool plan, got %d", len(plan.Tools))
	}
	tp := plan.Tools[0]
	if tp.Decision != DecisionForbidden {
		t.Errorf("Decision = %q, want %q", tp.Decision, DecisionForbidden)
	}
	if tp.Generated {
		t.Error("forbidden tool should not be generated")
	}
	if tp.Language != LanguageUnknown {
		t.Errorf("Language = %q, want %q", tp.Language, LanguageUnknown)
	}
}

func TestBuildUnknownDecisionTool(t *testing.T) {
	s := NewSynthesizer()
	plan, err := s.Build([]Request{
		{
			Name:     "ambiguous-tool",
			Decision: DecisionUnknown,
			Purpose:  "Insufficient evidence",
		},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	tp := plan.Tools[0]
	if tp.Decision != DecisionUnknown {
		t.Errorf("Decision = %q, want %q", tp.Decision, DecisionUnknown)
	}
	if tp.Generated {
		t.Error("unknown decision should not produce generated tool")
	}
	if tp.Language != LanguageUnknown {
		t.Errorf("Language = %q, want %q", tp.Language, LanguageUnknown)
	}
	if tp.Reason != "insufficient evidence to justify tool generation" {
		t.Errorf("Reason = %q, unexpected", tp.Reason)
	}
}

func TestBuildMultipleToolsSortedDeterministically(t *testing.T) {
	s := NewSynthesizer()
	plan, err := s.Build([]Request{
		{Name: "zebra", Decision: DecisionForbidden},
		{Name: "alpha", Decision: DecisionForbidden},
		{Name: "mid", Decision: DecisionForbidden},
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if len(plan.Tools) != 3 {
		t.Fatalf("expected 3 tool plans, got %d", len(plan.Tools))
	}
	names := StablePlanNames(plan)
	if names[0] != "alpha" || names[1] != "mid" || names[2] != "zebra" {
		t.Errorf("tools not sorted: %v", names)
	}
}

func TestBuildRejectsMandatoryWithoutConsumerPaths(t *testing.T) {
	s := NewSynthesizer()
	_, err := s.Build([]Request{
		{
			Name:     "no-consumer",
			Decision: DecisionMandatory,
			LanguageInput: LanguageSelectionInput{
				SupportedLanguages: []Language{LanguageGo},
				PreferredLanguages: []Language{LanguageGo},
			},
			EvidencePaths: []string{"docs/spec.md"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "consumer paths") {
		t.Fatalf("expected consumer-paths error, got %v", err)
	}
}

func TestBuildRejectsMandatoryWithoutEvidencePaths(t *testing.T) {
	s := NewSynthesizer()
	_, err := s.Build([]Request{
		{
			Name:     "no-evidence",
			Decision: DecisionMandatory,
			LanguageInput: LanguageSelectionInput{
				SupportedLanguages: []Language{LanguageGo},
				PreferredLanguages: []Language{LanguageGo},
			},
			ConsumerPaths: []string{"cmd/main.go"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("expected evidence-paths error, got %v", err)
	}
}

func TestBuildRejectsEmptyName(t *testing.T) {
	s := NewSynthesizer()
	_, err := s.Build([]Request{
		{Name: "", Decision: DecisionForbidden},
	})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name-required error, got %v", err)
	}
}

// --- ValidatePlan tests ---

func TestValidatePlanValidMandatoryTool(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "fetcher",
				Decision:      DecisionMandatory,
				Language:      LanguageGo,
				Generated:     true,
				ConsumerPaths: []string{"cmd/main.go"},
				EvidencePaths: []string{"docs/spec.md"},
			},
		},
	}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("ValidatePlan rejected valid plan: %v", err)
	}
}

func TestValidatePlanRejectsOutOfOrderTools(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{Name: "zebra", Decision: DecisionForbidden},
			{Name: "alpha", Decision: DecisionForbidden},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "not deterministically ordered") {
		t.Fatalf("expected ordering error, got %v", err)
	}
}

func TestValidatePlanRejectsForbiddenWithGenerated(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{Name: "bad", Decision: DecisionForbidden, Generated: true},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "forbidden tools cannot be generated") {
		t.Fatalf("expected forbidden+generated error, got %v", err)
	}
}

func TestValidatePlanRejectsUnknownWithGenerated(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{Name: "bad", Decision: DecisionUnknown, Generated: true, Language: LanguageUnknown},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "unknown decision cannot produce generated") {
		t.Fatalf("expected unknown+generated error, got %v", err)
	}
}

func TestValidatePlanRejectsUnknownWithLanguageSet(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{Name: "bad", Decision: DecisionUnknown, Language: LanguageGo},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "must keep UNKNOWN language") {
		t.Fatalf("expected unknown-language error, got %v", err)
	}
}

func TestValidatePlanRejectsMandatoryWithUnknownLanguage(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "bad",
				Decision:      DecisionMandatory,
				Language:      LanguageUnknown,
				Generated:     true,
				ConsumerPaths: []string{"a.go"},
				EvidencePaths: []string{"b.md"},
			},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "cannot keep UNKNOWN language") {
		t.Fatalf("expected unknown-language error for mandatory, got %v", err)
	}
}

func TestValidatePlanRejectsAbsolutePaths(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "bad",
				Decision:      DecisionMandatory,
				Language:      LanguageGo,
				Generated:     true,
				ConsumerPaths: []string{"/etc/passwd"},
				EvidencePaths: []string{"docs/spec.md"},
			},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "absolute paths") {
		t.Fatalf("expected absolute-path error, got %v", err)
	}
}

func TestValidatePlanRejectsDuplicatePaths(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "dup",
				Decision:      DecisionMandatory,
				Language:      LanguageGo,
				Generated:     true,
				ConsumerPaths: []string{"a.go", "a.go"},
				EvidencePaths: []string{"docs/spec.md"},
			},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "duplicate path") {
		t.Fatalf("expected duplicate-path error, got %v", err)
	}
}

func TestValidatePlanRejectsPathTraversal(t *testing.T) {
	plan := Plan{
		Tools: []ToolPlan{
			{
				Name:          "escape",
				Decision:      DecisionMandatory,
				Language:      LanguageGo,
				Generated:     true,
				ConsumerPaths: []string{"../../etc/passwd"},
				EvidencePaths: []string{"docs/spec.md"},
			},
		},
	}
	if err := ValidatePlan(plan); err == nil || !strings.Contains(err.Error(), "escapes tree root") {
		t.Fatalf("expected path-traversal error, got %v", err)
	}
}

// --- ResolveLanguage edge cases ---

func TestResolveLanguageSingleSupported(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageShell},
	})
	if got != LanguageShell {
		t.Fatalf("single supported language should resolve, got %q", got)
	}
}

func TestResolveLanguageHintsNarrowCandidates(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageGo, LanguagePython, LanguageShell},
		LanguageHints:      []Language{LanguagePython},
	})
	if got != LanguagePython {
		t.Fatalf("hints should narrow to python, got %q", got)
	}
}

func TestResolveLanguageHintsNoOverlap(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageGo},
		LanguageHints:      []Language{LanguagePython},
	})
	if got != LanguageUnknown {
		t.Fatalf("no overlap should return UNKNOWN, got %q", got)
	}
}

func TestResolveLanguageEmptySupported(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: nil,
		PreferredLanguages: []Language{LanguageGo},
	})
	if got != LanguageUnknown {
		t.Fatalf("empty supported should return UNKNOWN, got %q", got)
	}
}

func TestResolveLanguageMultipleSupportedNoPreference(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageGo, LanguagePython},
	})
	if got != LanguageUnknown {
		t.Fatalf("ambiguous without preference should return UNKNOWN, got %q", got)
	}
}

func TestResolveLanguagePreferenceBreaksTie(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{LanguageGo, LanguagePython},
		PreferredLanguages: []Language{LanguageGo},
		LanguageHints:      []Language{LanguageGo, LanguagePython},
	})
	if got != LanguageGo {
		t.Fatalf("preference should break tie, got %q", got)
	}
}

func TestResolveLanguageNormalizesCase(t *testing.T) {
	got := ResolveLanguage(LanguageSelectionInput{
		SupportedLanguages: []Language{Language("GO")},
	})
	if got != LanguageGo {
		t.Fatalf("case-insensitive normalization failed, got %q", got)
	}
}
