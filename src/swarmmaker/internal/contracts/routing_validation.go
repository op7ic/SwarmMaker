// routing_validation.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Routing, validation, and output contracts.
// Defines FallbackEvent, ProviderCapability, RoutingDecision, OutputTreeSpec,
// ToolSynthesisRequest/Result, ValidationFinding, ReviewerVerdict, and
// ValidationReport types. Includes policy-specific validation (e.g.,
// round-robin is explicitly rejected in v1, single-model requires all IDs
// to match).


package contracts

import (
	"errors"
	"fmt"
)

type FallbackEvent struct {
	SchemaVersion       string                      `json:"schema_version"`
	FallbackID          string                      `json:"fallback_id"`
	Category            FallbackCategory            `json:"category"`
	AuthorizationStatus FallbackAuthorizationStatus `json:"authorization_status"`
	AuthorizedBy        string                      `json:"authorized_by"`
	SourceRole          ExecutionRole               `json:"source_role"`
	Reason              string                      `json:"reason"`
	UserVisibleEffect   string                      `json:"user_visible_effect"`
}

func (f FallbackEvent) Validate() error {
	errs := []error{validateSchemaVersion(f.SchemaVersion)}
	errs = append(errs,
		validateID("fallback_id", f.FallbackID),
		validateEnumValue("category", string(f.Category), knownFallbackCategories(), ErrUnknownFallbackCategory),
		validateEnumValue("authorization_status", string(f.AuthorizationStatus), knownFallbackAuthorizationStatuses(), ErrUnknownAuthorizationStatus),
		validateNonEmptyField("authorized_by", f.AuthorizedBy),
		validateEnumValue("source_role", string(f.SourceRole), knownExecutionRoles(), ErrUnknownExecutionRole),
		validateNonEmptyField("reason", f.Reason),
		validateNonEmptyField("user_visible_effect", f.UserVisibleEffect),
	)
	return errors.Join(filterErrors(errs)...)
}

type ProviderCapability struct {
	SchemaVersion  string          `json:"schema_version"`
	ProviderID     string          `json:"provider_id"`
	ProviderName   ProviderName    `json:"provider_name"`
	BinaryName     string          `json:"binary_name"`
	Available      bool            `json:"available"`
	SupportedRoles []ExecutionRole `json:"supported_roles"`
}

func (p ProviderCapability) Validate() error {
	errs := []error{validateSchemaVersion(p.SchemaVersion)}
	errs = append(errs,
		validateID("provider_id", p.ProviderID),
		validateEnumValue("provider_name", string(p.ProviderName), knownProviderNames(), ErrUnknownProviderName),
		validateNonEmptyField("binary_name", p.BinaryName),
		validateExecutionRoles(p.SupportedRoles),
	)
	return errors.Join(filterErrors(errs)...)
}

type RoutingDecision struct {
	SchemaVersion         string          `json:"schema_version"`
	DecisionID            string          `json:"decision_id"`
	ProductID             string          `json:"product_id"`
	SourceID              string          `json:"source_id"`
	Policy                RoutingPolicy   `json:"policy"`
	GeneratorProviderID   string          `json:"generator_provider_id"`
	CriticProviderID      string          `json:"critic_provider_id"`
	OutputProviderIDs     []string        `json:"output_provider_ids"`
	ToolBuilderProviderID string          `json:"tool_builder_provider_id,omitempty"`
	CandidateProviderIDs  []string        `json:"candidate_provider_ids,omitempty"`
	SelectionIndex        int             `json:"selection_index,omitempty"`
	Reason                string          `json:"reason"`
	FallbackEvents        []FallbackEvent `json:"fallback_events,omitempty"`
}

func (r RoutingDecision) Validate() error {
	errs := []error{validateSchemaVersion(r.SchemaVersion)}
	errs = append(errs,
		validateID("decision_id", r.DecisionID),
		validateID("product_id", r.ProductID),
		validateID("source_id", r.SourceID),
		validateEnumValue("policy", string(r.Policy), knownRoutingPolicies(), ErrUnknownRoutingPolicy),
		validateID("generator_provider_id", r.GeneratorProviderID),
		validateID("critic_provider_id", r.CriticProviderID),
		validateNonEmptyField("reason", r.Reason),
	)
	if err := validateStringList("output_provider_ids", r.OutputProviderIDs, true); err != nil {
		errs = append(errs, err)
	}
	if r.ToolBuilderProviderID != "" {
		errs = append(errs, validateID("tool_builder_provider_id", r.ToolBuilderProviderID))
	}
	if len(r.FallbackEvents) > 0 {
		for i, event := range r.FallbackEvents {
			if err := event.Validate(); err != nil {
				errs = append(errs, fmt.Errorf("fallback_events[%d]: %w", i, err))
			}
		}
	}
	switch r.Policy {
	case RoutingPolicySingleModel:
		if len(r.OutputProviderIDs) != 1 || r.GeneratorProviderID != r.CriticProviderID || r.GeneratorProviderID != r.OutputProviderIDs[0] {
			errs = append(errs, fmt.Errorf("single_model policy requires generator, critic, and exactly one output provider to match"))
		}
	case RoutingPolicySameModelCritique:
		if r.GeneratorProviderID != r.CriticProviderID {
			errs = append(errs, fmt.Errorf("same_model_critique policy requires generator and critic providers to match"))
		}
	case RoutingPolicyRoundRobin:
		errs = append(errs, fmt.Errorf("%w: round_robin is deferred in schema v1", ErrUnknownRoutingPolicy))
	}
	if len(r.CandidateProviderIDs) > 0 {
		if err := validateStringList("candidate_provider_ids", r.CandidateProviderIDs, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(filterErrors(errs)...)
}

type OutputTreeTarget struct {
	ProviderName  ProviderName     `json:"provider_name"`
	Format        OutputTreeFormat `json:"format"`
	RootDir       string           `json:"root_dir"`
	RequiredFiles []string         `json:"required_files"`
	OptionalFiles []string         `json:"optional_files,omitempty"`
}

func (t OutputTreeTarget) Validate() error {
	errs := []error{
		validateEnumValue("provider_name", string(t.ProviderName), knownProviderNames(), ErrUnknownProviderName),
		validateEnumValue("format", string(t.Format), knownOutputTreeFormats(), ErrUnknownOutputTreeFormat),
		validateNonEmptyField("root_dir", t.RootDir),
		validateStringList("required_files", t.RequiredFiles, true),
	}
	if len(t.OptionalFiles) > 0 {
		if err := validateStringList("optional_files", t.OptionalFiles, false); err != nil {
			errs = append(errs, err)
		}
	}
	if overlap := overlappingStrings(t.RequiredFiles, t.OptionalFiles); len(overlap) > 0 {
		errs = append(errs, fmt.Errorf("required_files and optional_files overlap: %v", overlap))
	}
	return errors.Join(filterErrors(errs)...)
}

type OutputTreeSpec struct {
	SchemaVersion string             `json:"schema_version"`
	SpecID        string             `json:"spec_id"`
	Targets       []OutputTreeTarget `json:"targets"`
}

func (o OutputTreeSpec) Validate() error {
	errs := []error{validateSchemaVersion(o.SchemaVersion)}
	errs = append(errs,
		validateID("spec_id", o.SpecID),
	)
	if len(o.Targets) == 0 {
		errs = append(errs, fmt.Errorf("%w: targets", ErrMissingRequiredField))
	}
	seenRoots := make(map[string]struct{}, len(o.Targets))
	seenFormats := make(map[string]struct{}, len(o.Targets))
	for i, target := range o.Targets {
		if err := target.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("targets[%d]: %w", i, err))
			continue
		}
		if _, ok := seenRoots[target.RootDir]; ok {
			errs = append(errs, fmt.Errorf("targets[%d]: duplicate root_dir %q", i, target.RootDir))
		}
		seenRoots[target.RootDir] = struct{}{}
		formatKey := string(target.Format)
		if _, ok := seenFormats[formatKey]; ok {
			errs = append(errs, fmt.Errorf("targets[%d]: duplicate format %q", i, target.Format))
		}
		seenFormats[formatKey] = struct{}{}
	}
	return errors.Join(filterErrors(errs)...)
}

type ToolSynthesisRequest struct {
	SchemaVersion        string       `json:"schema_version"`
	RequestID            string       `json:"request_id"`
	ProductID            string       `json:"product_id"`
	SourceID             string       `json:"source_id"`
	OutputTreeSpecID     string       `json:"output_tree_spec_id"`
	Purpose              string       `json:"purpose"`
	TargetLanguage       ToolLanguage `json:"target_language"`
	RequiredCapabilities []string     `json:"required_capabilities"`
}

func (t ToolSynthesisRequest) Validate() error {
	errs := []error{validateSchemaVersion(t.SchemaVersion)}
	errs = append(errs,
		validateID("request_id", t.RequestID),
		validateID("product_id", t.ProductID),
		validateID("source_id", t.SourceID),
		validateID("output_tree_spec_id", t.OutputTreeSpecID),
		validateNonEmptyField("purpose", t.Purpose),
		validateEnumValue("target_language", string(t.TargetLanguage), knownToolLanguages(), ErrUnknownToolLanguage),
		validateStringList("required_capabilities", t.RequiredCapabilities, true),
	)
	return errors.Join(filterErrors(errs)...)
}

type GeneratedArtifact struct {
	SchemaVersion      string        `json:"schema_version"`
	ArtifactID         string        `json:"artifact_id"`
	ProductID          string        `json:"product_id"`
	SourceID           string        `json:"source_id"`
	ProducerProviderID string        `json:"producer_provider_id"`
	ProducerRole       ExecutionRole `json:"producer_role"`
	Path               string        `json:"path"`
	Kind               ArtifactKind  `json:"kind"`
	Language           ToolLanguage  `json:"language"`
	Digest             string        `json:"digest"`
	Content            string        `json:"content,omitempty"`
}

func (g GeneratedArtifact) Validate() error {
	errs := []error{validateSchemaVersion(g.SchemaVersion)}
	errs = append(errs,
		validateID("artifact_id", g.ArtifactID),
		validateID("product_id", g.ProductID),
		validateID("source_id", g.SourceID),
		validateID("producer_provider_id", g.ProducerProviderID),
		validateEnumValue("producer_role", string(g.ProducerRole), knownExecutionRoles(), ErrUnknownExecutionRole),
		validateNonEmptyField("path", g.Path),
		validateEnumValue("kind", string(g.Kind), knownArtifactKinds(), ErrUnknownArtifactKind),
		validateEnumValue("language", string(g.Language), knownToolLanguages(), ErrUnknownToolLanguage),
		validateNonEmptyField("digest", g.Digest),
	)
	return errors.Join(filterErrors(errs)...)
}

type ValidationFinding struct {
	SchemaVersion string          `json:"schema_version"`
	FindingID     string          `json:"finding_id"`
	ArtifactID    string          `json:"artifact_id"`
	Category      string          `json:"category"`
	Severity      FindingSeverity `json:"severity"`
	Message       string          `json:"message"`
	Path          string          `json:"path,omitempty"`
	EvidenceIDs   []string        `json:"evidence_ids"`
}

func (f ValidationFinding) Validate() error {
	errs := []error{validateSchemaVersion(f.SchemaVersion)}
	errs = append(errs,
		validateID("finding_id", f.FindingID),
		validateID("artifact_id", f.ArtifactID),
		validateNonEmptyField("category", f.Category),
		validateEnumValue("severity", string(f.Severity), knownFindingSeverities(), ErrUnknownFindingSeverity),
		validateNonEmptyField("message", f.Message),
		validateStringList("evidence_ids", f.EvidenceIDs, true),
	)
	return errors.Join(filterErrors(errs)...)
}

type ReviewerVerdict struct {
	SchemaVersion string              `json:"schema_version"`
	VerdictID     string              `json:"verdict_id"`
	ReviewerID    string              `json:"reviewer_id"`
	ArtifactID    string              `json:"artifact_id"`
	Outcome       ReviewOutcome       `json:"outcome"`
	Summary       string              `json:"summary"`
	Findings      []ValidationFinding `json:"findings,omitempty"`
}

func (v ReviewerVerdict) Validate() error {
	errs := []error{validateSchemaVersion(v.SchemaVersion)}
	errs = append(errs,
		validateID("verdict_id", v.VerdictID),
		validateID("reviewer_id", v.ReviewerID),
		validateID("artifact_id", v.ArtifactID),
		validateEnumValue("outcome", string(v.Outcome), knownReviewOutcomes(), ErrUnknownReviewOutcome),
		validateNonEmptyField("summary", v.Summary),
	)
	for i, finding := range v.Findings {
		if err := finding.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	if (v.Outcome == ReviewOutcomeRevise || v.Outcome == ReviewOutcomeReject) && len(v.Findings) == 0 {
		errs = append(errs, fmt.Errorf("%w: findings", ErrMissingRequiredField))
	}
	return errors.Join(filterErrors(errs)...)
}

type ToolSynthesisResult struct {
	SchemaVersion      string              `json:"schema_version"`
	ResultID           string              `json:"result_id"`
	RequestID          string              `json:"request_id"`
	Success            bool                `json:"success"`
	GeneratedArtifacts []GeneratedArtifact `json:"generated_artifacts,omitempty"`
	Findings           []ValidationFinding `json:"findings,omitempty"`
	ReviewerVerdicts   []ReviewerVerdict   `json:"reviewer_verdicts,omitempty"`
}

func (r ToolSynthesisResult) Validate() error {
	errs := []error{validateSchemaVersion(r.SchemaVersion)}
	errs = append(errs,
		validateID("result_id", r.ResultID),
		validateID("request_id", r.RequestID),
	)
	for i, artifact := range r.GeneratedArtifacts {
		if err := artifact.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("generated_artifacts[%d]: %w", i, err))
		}
	}
	for i, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	for i, verdict := range r.ReviewerVerdicts {
		if err := verdict.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("reviewer_verdicts[%d]: %w", i, err))
		}
	}
	if r.Success && len(r.GeneratedArtifacts) == 0 {
		errs = append(errs, fmt.Errorf("%w: generated_artifacts", ErrMissingRequiredField))
	}
	if !r.Success && len(r.Findings) == 0 {
		errs = append(errs, fmt.Errorf("%w: findings", ErrMissingRequiredField))
	}
	return errors.Join(filterErrors(errs)...)
}

type ValidationReport struct {
	SchemaVersion         string              `json:"schema_version"`
	ReportID              string              `json:"report_id"`
	ProductID             string              `json:"product_id"`
	SourceID              string              `json:"source_id"`
	RoutingDecisionID     string              `json:"routing_decision_id"`
	OutputTreeSpecID      string              `json:"output_tree_spec_id"`
	ToolSynthesisResultID string              `json:"tool_synthesis_result_id"`
	Success               bool                `json:"success"`
	Findings              []ValidationFinding `json:"findings,omitempty"`
	FallbackEvents        []FallbackEvent     `json:"fallback_events,omitempty"`
	ReviewerVerdicts      []ReviewerVerdict   `json:"reviewer_verdicts,omitempty"`
	GeneratedArtifacts    []GeneratedArtifact `json:"generated_artifacts,omitempty"`
	EvidenceReferences    []EvidenceReference `json:"evidence_references,omitempty"`
}

func (r ValidationReport) Validate() error {
	errs := []error{validateSchemaVersion(r.SchemaVersion)}
	errs = append(errs,
		validateID("report_id", r.ReportID),
		validateID("product_id", r.ProductID),
		validateID("source_id", r.SourceID),
		validateID("routing_decision_id", r.RoutingDecisionID),
		validateID("output_tree_spec_id", r.OutputTreeSpecID),
		validateID("tool_synthesis_result_id", r.ToolSynthesisResultID),
	)
	for i, finding := range r.Findings {
		if err := finding.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("findings[%d]: %w", i, err))
		}
	}
	for i, event := range r.FallbackEvents {
		if err := event.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("fallback_events[%d]: %w", i, err))
		}
	}
	for i, verdict := range r.ReviewerVerdicts {
		if err := verdict.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("reviewer_verdicts[%d]: %w", i, err))
		}
	}
	for i, artifact := range r.GeneratedArtifacts {
		if err := artifact.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("generated_artifacts[%d]: %w", i, err))
		}
	}
	for i, evidence := range r.EvidenceReferences {
		if err := evidence.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("evidence_references[%d]: %w", i, err))
		}
	}
	if !r.Success && len(r.Findings) == 0 {
		errs = append(errs, fmt.Errorf("%w: findings", ErrMissingRequiredField))
	}
	return errors.Join(filterErrors(errs)...)
}

func filterErrors(errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

func knownProviderNames() map[string]struct{} {
	return map[string]struct{}{
		string(ProviderClaude): {},
		string(ProviderCodex):  {},
		string(ProviderGemini): {},
		string(ProviderOllama): {},
	}
}

func knownExecutionRoles() map[string]struct{} {
	return map[string]struct{}{
		string(ExecutionRoleGenerator):      {},
		string(ExecutionRoleCritic):         {},
		string(ExecutionRoleOutputRenderer): {},
		string(ExecutionRoleToolBuilder):    {},
	}
}

func knownFallbackCategories() map[string]struct{} {
	return map[string]struct{}{
		string(FallbackCategoryProviderUnavailable):   {},
		string(FallbackCategoryCriticUnavailable):     {},
		string(FallbackCategoryOutputRendererMissing): {},
		string(FallbackCategoryToolLanguageUnknown):   {},
		string(FallbackCategoryValidationRetry):       {},
		string(FallbackCategoryInputRecovery):         {},
	}
}

func knownFallbackAuthorizationStatuses() map[string]struct{} {
	return map[string]struct{}{
		string(FallbackAuthorizationAuthorized):     {},
		string(FallbackAuthorizationDenied):         {},
		string(FallbackAuthorizationRequiresReview): {},
	}
}

func knownRoutingPolicies() map[string]struct{} {
	return map[string]struct{}{
		string(RoutingPolicySingleModel):       {},
		string(RoutingPolicySameModelCritique): {},
		string(RoutingPolicyCapabilityBased):   {},
	}
}

func knownOutputTreeFormats() map[string]struct{} {
	return map[string]struct{}{
		string(OutputTreeFormatClaude): {},
		string(OutputTreeFormatCodex):  {},
		string(OutputTreeFormatGemini): {},
	}
}

func knownToolLanguages() map[string]struct{} {
	return map[string]struct{}{
		string(ToolLanguageGo):      {},
		string(ToolLanguagePython):  {},
		string(ToolLanguageShell):   {},
		string(ToolLanguageUnknown): {},
	}
}

func knownArtifactKinds() map[string]struct{} {
	return map[string]struct{}{
		string(ArtifactKindFile):      {},
		string(ArtifactKindDirectory): {},
		string(ArtifactKindTool):      {},
		string(ArtifactKindManifest):  {},
		string(ArtifactKindReport):    {},
	}
}

func knownReviewOutcomes() map[string]struct{} {
	return map[string]struct{}{
		string(ReviewOutcomeApprove): {},
		string(ReviewOutcomeRevise):  {},
		string(ReviewOutcomeReject):  {},
	}
}

func knownFindingSeverities() map[string]struct{} {
	return map[string]struct{}{
		string(FindingSeverityError):   {},
		string(FindingSeverityWarning): {},
		string(FindingSeverityInfo):    {},
	}
}

func knownSourceKinds() map[string]struct{} {
	return map[string]struct{}{
		string(SourceKindNotes):   {},
		string(SourceKindSpec):    {},
		string(SourceKindAPIDoc):  {},
		string(SourceKindCode):    {},
		string(SourceKindData):    {},
		string(SourceKindBinary):  {},
		string(SourceKindUnknown): {},
	}
}

func knownEvidenceKinds() map[string]struct{} {
	return map[string]struct{}{
		string(EvidenceKindSourceFile):        {},
		string(EvidenceKindBinaryFile):        {},
		string(EvidenceKindUnreadableFile):    {},
		string(EvidenceKindSkippedPath):       {},
		string(EvidenceKindProviderOutput):    {},
		string(EvidenceKindValidationReport):  {},
		string(EvidenceKindFallbackEvent):     {},
		string(EvidenceKindGeneratedArtifact): {},
	}
}

func knownEvidenceStatuses() map[string]struct{} {
	return map[string]struct{}{
		string(EvidenceStatusCaptured): {},
		string(EvidenceStatusSkipped):  {},
		string(EvidenceStatusRejected): {},
	}
}

func overlappingStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	for _, value := range a {
		seen[value] = struct{}{}
	}
	var overlap []string
	for _, value := range b {
		if _, ok := seen[value]; ok {
			overlap = append(overlap, value)
		}
	}
	return overlap
}

func validateExecutionRoles(values []ExecutionRole) error {
	if len(values) == 0 {
		return fmt.Errorf("%w: supported_roles", ErrMissingRequiredField)
	}
	seen := make(map[ExecutionRole]struct{}, len(values))
	for i, value := range values {
		if _, ok := knownExecutionRoles()[string(value)]; !ok {
			return fmt.Errorf("%w: supported_roles[%d]=%q", ErrUnknownExecutionRole, i, value)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("supported_roles contains duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
