// contracts_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for all contract types.
// Covers schema version rejection, required field omission, fallback
// authorization completeness, routing policy validation, tool language
// normalization, and JSON round-trip fidelity for all 12 contract types.


package contracts_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/op7ic/swarmmaker/internal/contracts"
)

func TestUnknownSchemaVersionRejected(t *testing.T) {
	t.Parallel()

	cases := map[string]func() error{
		"product_definition": func() error {
			v := validProductDefinition()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"source_ir": func() error {
			v := validSourceIR()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"fallback_event": func() error {
			v := validFallbackEvent()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"provider_capability": func() error {
			v := validProviderCapability()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"routing_decision": func() error {
			v := validRoutingDecision()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"output_tree_spec": func() error {
			v := validOutputTreeSpec()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"tool_synthesis_request": func() error {
			v := validToolSynthesisRequest()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"generated_artifact": func() error {
			v := validGeneratedArtifact()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"reviewer_verdict": func() error {
			v := validReviewerVerdict()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"validation_finding": func() error {
			v := validValidationFinding()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"tool_synthesis_result": func() error {
			v := validToolSynthesisResult()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
		"validation_report": func() error {
			v := validValidationReport()
			v.SchemaVersion = "v2"
			return v.Validate()
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := fn()
			if err == nil {
				t.Fatalf("expected unknown schema version error")
			}
			if !errors.Is(err, contracts.ErrUnknownSchemaVersion) {
				t.Fatalf("expected ErrUnknownSchemaVersion, got %v", err)
			}
		})
	}
}

func TestRequiredFieldOmissionsFail(t *testing.T) {
	t.Parallel()

	cases := map[string]func() error{
		"product_name": func() error {
			v := validProductDefinition()
			v.Name = ""
			return v.Validate()
		},
		"source_root": func() error {
			v := validSourceIR()
			v.RootPath = ""
			return v.Validate()
		},
		"provider_id": func() error {
			v := validProviderCapability()
			v.ProviderID = ""
			return v.Validate()
		},
		"routing_critic_id": func() error {
			v := validRoutingDecision()
			v.CriticProviderID = ""
			return v.Validate()
		},
		"output_required_files": func() error {
			v := validOutputTreeSpec()
			v.Targets = nil
			return v.Validate()
		},
		"tool_request_capabilities": func() error {
			v := validToolSynthesisRequest()
			v.RequiredCapabilities = nil
			return v.Validate()
		},
		"artifact_digest": func() error {
			v := validGeneratedArtifact()
			v.Digest = ""
			return v.Validate()
		},
		"report_ids": func() error {
			v := validValidationReport()
			v.ReportID = ""
			return v.Validate()
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := fn()
			if err == nil {
				t.Fatalf("expected validation failure")
			}
			if !errors.Is(err, contracts.ErrMissingRequiredField) && !errors.Is(err, contracts.ErrEmptyID) {
				t.Fatalf("expected a required-field error, got %v", err)
			}
		})
	}
}

func TestFallbackAuthorizationCompleteness(t *testing.T) {
	t.Parallel()

	cases := map[string]func() error{
		"missing_category": func() error {
			v := validFallbackEvent()
			v.Category = ""
			return v.Validate()
		},
		"missing_authorization_status": func() error {
			v := validFallbackEvent()
			v.AuthorizationStatus = ""
			return v.Validate()
		},
		"missing_authorized_by": func() error {
			v := validFallbackEvent()
			v.AuthorizedBy = ""
			return v.Validate()
		},
		"missing_user_visible_effect": func() error {
			v := validFallbackEvent()
			v.UserVisibleEffect = ""
			return v.Validate()
		},
		"unknown_category": func() error {
			v := validFallbackEvent()
			v.Category = "mystery"
			return v.Validate()
		},
	}

	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := fn()
			if err == nil {
				t.Fatalf("expected fallback validation failure")
			}
			if !errors.Is(err, contracts.ErrMissingRequiredField) &&
				!errors.Is(err, contracts.ErrUnknownFallbackCategory) &&
				!errors.Is(err, contracts.ErrUnknownAuthorizationStatus) {
				t.Fatalf("expected fallback authorization error, got %v", err)
			}
		})
	}
}

func TestV1RejectsDeferredRoundRobinPolicy(t *testing.T) {
	t.Parallel()

	v := validRoutingDecision()
	v.Policy = contracts.RoutingPolicyRoundRobin
	v.CandidateProviderIDs = []string{"provider-1", "provider-2"}
	v.SelectionIndex = 0

	err := v.Validate()
	if err == nil {
		t.Fatal("expected round_robin to be rejected in schema v1")
	}
	if !errors.Is(err, contracts.ErrUnknownRoutingPolicy) {
		t.Fatalf("expected ErrUnknownRoutingPolicy, got %v", err)
	}
}

func TestRoutingDecisionAllowsMultiOutputProvidersUnderCapabilityPolicy(t *testing.T) {
	t.Parallel()

	v := validRoutingDecision()
	v.Policy = contracts.RoutingPolicyCapabilityBased
	v.OutputProviderIDs = []string{"provider-1", "provider-2", "provider-3"}

	if err := v.Validate(); err != nil {
		t.Fatalf("expected multi-output routing decision to validate, got %v", err)
	}
}

func TestToolLanguageUnknownUsesContractSpelling(t *testing.T) {
	t.Parallel()

	if contracts.ToolLanguageUnknown != contracts.ToolLanguage("UNKNOWN") {
		t.Fatalf("ToolLanguageUnknown = %q, want UNKNOWN", contracts.ToolLanguageUnknown)
	}
}

func TestJSONRoundTripPreservesContracts(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"product_definition":     validProductDefinition(),
		"source_ir":              validSourceIR(),
		"fallback_event":         validFallbackEvent(),
		"provider_capability":    validProviderCapability(),
		"routing_decision":       validRoutingDecision(),
		"output_tree_spec":       validOutputTreeSpec(),
		"tool_synthesis_request": validToolSynthesisRequest(),
		"generated_artifact":     validGeneratedArtifact(),
		"validation_finding":     validValidationFinding(),
		"reviewer_verdict":       validReviewerVerdict(),
		"tool_synthesis_result":  validToolSynthesisResult(),
		"validation_report":      validValidationReport(),
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := mustRoundTrip(t, value)
			if !reflect.DeepEqual(value, got) {
				t.Fatalf("round trip mismatch:\nwant: %#v\ngot:  %#v", value, got)
			}
		})
	}
}

func mustRoundTrip(t *testing.T, value any) any {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	ptr := reflect.New(reflect.TypeOf(value))
	if err := json.Unmarshal(data, ptr.Interface()); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	return ptr.Elem().Interface()
}

func validProductDefinition() contracts.ProductDefinition {
	return contracts.ProductDefinition{
		SchemaVersion: contracts.SchemaVersionV1,
		ProductID:     "swarm-maker",
		Name:          "AI Swarm Maker",
		CLIName:       "swarm-me",
		Description:   "turn loose docs into agent swarms",
		InputRoot:     "input",
		OutputRoot:    "SKILL",
		SupportedProviders: []contracts.ProviderName{
			contracts.ProviderClaude,
			contracts.ProviderCodex,
			contracts.ProviderGemini,
		},
	}
}

func validSourceIR() contracts.SourceIR {
	return contracts.SourceIR{
		SchemaVersion: contracts.SchemaVersionV1,
		SourceID:      "source-1",
		ProductID:     "swarm-maker",
		RootPath:      "input",
		Documents: []contracts.SourceDocument{
			{
				Path:          "input/notes.md",
				Kind:          contracts.SourceKindNotes,
				EvidenceID:    "evidence-1",
				ContentDigest: "sha256:notes",
				SizeBytes:     128,
			},
		},
		EvidenceReferences: []contracts.EvidenceReference{
			{
				SchemaVersion: contracts.SchemaVersionV1,
				EvidenceID:    "evidence-1",
				SourceID:      "source-1",
				Path:          "input/notes.md",
				Kind:          contracts.EvidenceKindSourceFile,
				Status:        contracts.EvidenceStatusCaptured,
				Digest:        "sha256:evidence-1",
			},
		},
	}
}

func validFallbackEvent() contracts.FallbackEvent {
	return contracts.FallbackEvent{
		SchemaVersion:       contracts.SchemaVersionV1,
		FallbackID:          "fallback-1",
		Category:            contracts.FallbackCategoryCriticUnavailable,
		AuthorizationStatus: contracts.FallbackAuthorizationAuthorized,
		AuthorizedBy:        "policy-engine",
		SourceRole:          contracts.ExecutionRoleCritic,
		Reason:              "critic binary missing",
		UserVisibleEffect:   "same-model critique was used",
	}
}

func validProviderCapability() contracts.ProviderCapability {
	return contracts.ProviderCapability{
		SchemaVersion:  contracts.SchemaVersionV1,
		ProviderID:     "provider-1",
		ProviderName:   contracts.ProviderClaude,
		BinaryName:     "claude",
		Available:      true,
		SupportedRoles: []contracts.ExecutionRole{contracts.ExecutionRoleGenerator, contracts.ExecutionRoleCritic},
	}
}

func validRoutingDecision() contracts.RoutingDecision {
	return contracts.RoutingDecision{
		SchemaVersion:       contracts.SchemaVersionV1,
		DecisionID:          "decision-1",
		ProductID:           "swarm-maker",
		SourceID:            "source-1",
		Policy:              contracts.RoutingPolicySingleModel,
		GeneratorProviderID: "provider-1",
		CriticProviderID:    "provider-1",
		OutputProviderIDs:   []string{"provider-1"},
		Reason:              "single installed provider",
	}
}

func validOutputTreeSpec() contracts.OutputTreeSpec {
	return contracts.OutputTreeSpec{
		SchemaVersion: contracts.SchemaVersionV1,
		SpecID:        "tree-1",
		Targets: []contracts.OutputTreeTarget{
			{
				ProviderName:  contracts.ProviderClaude,
				Format:        contracts.OutputTreeFormatClaude,
				RootDir:       ".claude",
				RequiredFiles: []string{"SKILL.md", "README.md"},
				OptionalFiles: []string{"skills/index.md"},
			},
		},
	}
}

func validToolSynthesisRequest() contracts.ToolSynthesisRequest {
	return contracts.ToolSynthesisRequest{
		SchemaVersion:        contracts.SchemaVersionV1,
		RequestID:            "tool-request-1",
		ProductID:            "swarm-maker",
		SourceID:             "source-1",
		OutputTreeSpecID:     "tree-1",
		Purpose:              "build API client helpers",
		TargetLanguage:       contracts.ToolLanguageGo,
		RequiredCapabilities: []string{"http_client", "json_parsing"},
	}
}

func validGeneratedArtifact() contracts.GeneratedArtifact {
	return contracts.GeneratedArtifact{
		SchemaVersion:      contracts.SchemaVersionV1,
		ArtifactID:         "artifact-1",
		ProductID:          "swarm-maker",
		SourceID:           "source-1",
		ProducerProviderID: "provider-1",
		ProducerRole:       contracts.ExecutionRoleToolBuilder,
		Path:               "tools/api_client.go",
		Kind:               contracts.ArtifactKindTool,
		Language:           contracts.ToolLanguageGo,
		Digest:             "sha256:artifact-1",
		Content:            "package tools",
	}
}

func validValidationFinding() contracts.ValidationFinding {
	return contracts.ValidationFinding{
		SchemaVersion: contracts.SchemaVersionV1,
		FindingID:     "finding-1",
		ArtifactID:    "artifact-1",
		Category:      "link",
		Severity:      contracts.FindingSeverityError,
		Message:       "broken relative link",
		Path:          "README.md",
		EvidenceIDs:   []string{"evidence-1"},
	}
}

func validReviewerVerdict() contracts.ReviewerVerdict {
	return contracts.ReviewerVerdict{
		SchemaVersion: contracts.SchemaVersionV1,
		VerdictID:     "verdict-1",
		ReviewerID:    "critic-1",
		ArtifactID:    "artifact-1",
		Outcome:       contracts.ReviewOutcomeRevise,
		Summary:       "revise the broken link",
		Findings:      []contracts.ValidationFinding{validValidationFinding()},
	}
}

func validToolSynthesisResult() contracts.ToolSynthesisResult {
	return contracts.ToolSynthesisResult{
		SchemaVersion:      contracts.SchemaVersionV1,
		ResultID:           "result-1",
		RequestID:          "tool-request-1",
		Success:            true,
		GeneratedArtifacts: []contracts.GeneratedArtifact{validGeneratedArtifact()},
	}
}

func validValidationReport() contracts.ValidationReport {
	return contracts.ValidationReport{
		SchemaVersion:         contracts.SchemaVersionV1,
		ReportID:              "report-1",
		ProductID:             "swarm-maker",
		SourceID:              "source-1",
		RoutingDecisionID:     "decision-1",
		OutputTreeSpecID:      "tree-1",
		ToolSynthesisResultID: "result-1",
		Success:               true,
		Findings:              []contracts.ValidationFinding{validValidationFinding()},
		FallbackEvents:        []contracts.FallbackEvent{validFallbackEvent()},
		ReviewerVerdicts:      []contracts.ReviewerVerdict{validReviewerVerdict()},
		GeneratedArtifacts:    []contracts.GeneratedArtifact{validGeneratedArtifact()},
		EvidenceReferences:    []contracts.EvidenceReference{validSourceIR().EvidenceReferences[0]},
	}
}
