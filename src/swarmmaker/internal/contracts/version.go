// version.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Schema version definitions and validation helpers.
// Defines the v1 schema version, provider names, execution roles, routing
// policies, and all sentinel error types. Provides generic validation helpers
// for schema versions, non-empty fields, IDs, and enum values used across
// all contract types.


package contracts

import (
	"errors"
	"fmt"
	"strings"
)

const SchemaVersionV1 = "v1"

var (
	ErrUnknownSchemaVersion       = errors.New("unknown schema version")
	ErrMissingRequiredField       = errors.New("missing required field")
	ErrEmptyID                    = errors.New("empty id")
	ErrUnknownProviderName        = errors.New("unknown provider name")
	ErrUnknownExecutionRole       = errors.New("unknown execution role")
	ErrUnknownFallbackCategory    = errors.New("unknown fallback category")
	ErrUnknownAuthorizationStatus = errors.New("unknown fallback authorization status")
	ErrUnknownRoutingPolicy       = errors.New("unknown routing policy")
	ErrUnknownOutputTreeFormat    = errors.New("unknown output tree format")
	ErrUnknownToolLanguage        = errors.New("unknown tool language")
	ErrUnknownArtifactKind        = errors.New("unknown artifact kind")
	ErrUnknownReviewOutcome       = errors.New("unknown review outcome")
	ErrUnknownFindingSeverity     = errors.New("unknown finding severity")
	ErrUnknownSourceKind          = errors.New("unknown source kind")
	ErrUnknownEvidenceKind        = errors.New("unknown evidence kind")
	ErrUnknownEvidenceStatus      = errors.New("unknown evidence status")
)

type ProviderName string

const (
	ProviderClaude ProviderName = "claude"
	ProviderCodex  ProviderName = "codex"
	ProviderGemini ProviderName = "gemini"
)

type ExecutionRole string

const (
	ExecutionRoleGenerator      ExecutionRole = "generator"
	ExecutionRoleCritic         ExecutionRole = "critic"
	ExecutionRoleOutputRenderer ExecutionRole = "output_renderer"
	ExecutionRoleToolBuilder    ExecutionRole = "tool_builder"
)

type FallbackCategory string

const (
	FallbackCategoryProviderUnavailable   FallbackCategory = "provider_unavailable"
	FallbackCategoryCriticUnavailable     FallbackCategory = "critic_unavailable"
	FallbackCategoryOutputRendererMissing FallbackCategory = "output_renderer_missing"
	FallbackCategoryToolLanguageUnknown   FallbackCategory = "tool_language_unknown"
	FallbackCategoryValidationRetry       FallbackCategory = "validation_retry"
	FallbackCategoryInputRecovery         FallbackCategory = "input_recovery"
)

type FallbackAuthorizationStatus string

const (
	FallbackAuthorizationAuthorized     FallbackAuthorizationStatus = "authorized"
	FallbackAuthorizationDenied         FallbackAuthorizationStatus = "denied"
	FallbackAuthorizationRequiresReview FallbackAuthorizationStatus = "requires_review"
)

type RoutingPolicy string

const (
	RoutingPolicySingleModel       RoutingPolicy = "single_model"
	RoutingPolicySameModelCritique RoutingPolicy = "same_model_critique"
	RoutingPolicyCapabilityBased   RoutingPolicy = "capability_based"
	RoutingPolicyRoundRobin        RoutingPolicy = "round_robin" // defined for explicit v1 rejection; not accepted by Validate.
)

type OutputTreeFormat string

const (
	OutputTreeFormatClaude OutputTreeFormat = "claude"
	OutputTreeFormatCodex  OutputTreeFormat = "codex"
	OutputTreeFormatGemini OutputTreeFormat = "gemini"
)

type ToolLanguage string

const (
	ToolLanguageGo      ToolLanguage = "go"
	ToolLanguagePython  ToolLanguage = "python"
	ToolLanguageShell   ToolLanguage = "shell"
	ToolLanguageUnknown ToolLanguage = "UNKNOWN"
)

type ArtifactKind string

const (
	ArtifactKindFile      ArtifactKind = "file"
	ArtifactKindDirectory ArtifactKind = "directory"
	ArtifactKindTool      ArtifactKind = "tool"
	ArtifactKindManifest  ArtifactKind = "manifest"
	ArtifactKindReport    ArtifactKind = "report"
)

type ReviewOutcome string

const (
	ReviewOutcomeApprove ReviewOutcome = "approve"
	ReviewOutcomeRevise  ReviewOutcome = "revise"
	ReviewOutcomeReject  ReviewOutcome = "reject"
)

type FindingSeverity string

const (
	FindingSeverityError   FindingSeverity = "error"
	FindingSeverityWarning FindingSeverity = "warning"
	FindingSeverityInfo    FindingSeverity = "info"
)

type SourceKind string

const (
	SourceKindNotes   SourceKind = "notes"
	SourceKindSpec    SourceKind = "spec"
	SourceKindAPIDoc  SourceKind = "api_doc"
	SourceKindCode    SourceKind = "code"
	SourceKindData    SourceKind = "data"
	SourceKindBinary  SourceKind = "binary"
	SourceKindUnknown SourceKind = "unknown"
)

type EvidenceKind string

const (
	EvidenceKindSourceFile        EvidenceKind = "source_file"
	EvidenceKindBinaryFile        EvidenceKind = "binary_file"
	EvidenceKindUnreadableFile    EvidenceKind = "unreadable_file"
	EvidenceKindSkippedPath       EvidenceKind = "skipped_path"
	EvidenceKindProviderOutput    EvidenceKind = "provider_output"
	EvidenceKindValidationReport  EvidenceKind = "validation_report"
	EvidenceKindFallbackEvent     EvidenceKind = "fallback_event"
	EvidenceKindGeneratedArtifact EvidenceKind = "generated_artifact"
)

type EvidenceStatus string

const (
	EvidenceStatusCaptured EvidenceStatus = "captured"
	EvidenceStatusSkipped  EvidenceStatus = "skipped"
	EvidenceStatusRejected EvidenceStatus = "rejected"
)

func validateSchemaVersion(version string) error {
	if version != SchemaVersionV1 {
		return fmt.Errorf("%w: %q", ErrUnknownSchemaVersion, version)
	}
	return nil
}

func validateNonEmptyField(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrMissingRequiredField, field)
	}
	return nil
}

func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}

func validateEnumValue(field, value string, allowed map[string]struct{}, unknownErr error) error {
	if _, ok := allowed[value]; !ok {
		return fmt.Errorf("%w: %s=%q", unknownErr, field, value)
	}
	return nil
}

func validateStringList(field string, values []string, required bool) error {
	if len(values) == 0 {
		if required {
			return fmt.Errorf("%w: %s", ErrMissingRequiredField, field)
		}
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" {
			return fmt.Errorf("%w: %s[%d]", ErrMissingRequiredField, field, i)
		}
		if _, ok := seen[clean]; ok {
			return fmt.Errorf("%s contains duplicate value %q", field, clean)
		}
		seen[clean] = struct{}{}
	}
	return nil
}
