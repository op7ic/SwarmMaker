// types.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Intermediate representation type definitions.
// Defines ArtifactPaths, PromptArtifact, ArtifactManifest, and ArtifactRef
// types with validation. These types structure the 7 JSON artifacts persisted
// under .tasks/ir/ that form the auditable bridge between ingestion/routing
// and draft generation.


package ir

import (
	"errors"
	"fmt"
	"strings"

	"github.com/op7ic/swarmmaker/internal/contracts"
	"github.com/op7ic/swarmmaker/internal/redaction"
	"github.com/op7ic/swarmmaker/prompts"
)

const manifestFileName = "manifest.json"

// ArtifactPaths lists the persisted IR files created under the `.tasks/ir`
// contract directory for one swarm-maker run.
type ArtifactPaths struct {
	Directory                string
	ManifestPath             string
	ProductDefinitionPath    string
	SourceIRPath             string
	ProviderCapabilitiesPath string
	RoutingDecisionPath      string
	OutputTreeSpecPath       string
	ToolSynthesisRequestPath string
	PromptIRPath             string
}

// PromptArtifact persists the prompt compiler input alongside contract-backed
// IR so reviewers can prove which facts were available before generation.
type PromptArtifact struct {
	SchemaVersion      string           `json:"schema_version"`
	PromptIRID         string           `json:"prompt_ir_id"`
	ProductID          string           `json:"product_id"`
	SourceID           string           `json:"source_id"`
	RoutingDecisionID  string           `json:"routing_decision_id"`
	OutputTreeSpecID   string           `json:"output_tree_spec_id"`
	ToolRequestID      string           `json:"tool_request_id"`
	PromptIR           prompts.PromptIR `json:"prompt_ir"`
	SourceMaterialHash string           `json:"source_material_hash"`
	RedactedSourceHash string           `json:"redacted_source_material_hash"`
	RedactionReport    redaction.Report `json:"redaction_report"`
}

func (p PromptArtifact) Validate() error {
	var errs []error
	if p.SchemaVersion != contracts.SchemaVersionV1 {
		errs = append(errs, fmt.Errorf("unsupported schema version %q (expected %q)", p.SchemaVersion, contracts.SchemaVersionV1))
	}
	for field, value := range map[string]string{
		"prompt_ir_id":         p.PromptIRID,
		"product_id":           p.ProductID,
		"source_id":            p.SourceID,
		"routing_decision_id":  p.RoutingDecisionID,
		"output_tree_spec_id":  p.OutputTreeSpecID,
		"tool_request_id":      p.ToolRequestID,
		"source_material_hash": p.SourceMaterialHash,
		"redacted_source_hash": p.RedactedSourceHash,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	if err := p.PromptIR.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("prompt_ir: %w", err))
	}
	return errors.Join(errs...)
}

type ArtifactManifest struct {
	SchemaVersion string        `json:"schema_version"`
	ManifestID    string        `json:"manifest_id"`
	ProductID     string        `json:"product_id"`
	SourceID      string        `json:"source_id"`
	PromptIRID    string        `json:"prompt_ir_id"`
	Artifacts     []ArtifactRef `json:"artifacts"`
}

func (m ArtifactManifest) Validate() error {
	var errs []error
	if m.SchemaVersion != contracts.SchemaVersionV1 {
		errs = append(errs, fmt.Errorf("unsupported schema version %q (expected %q)", m.SchemaVersion, contracts.SchemaVersionV1))
	}
	for field, value := range map[string]string{
		"manifest_id":  m.ManifestID,
		"product_id":   m.ProductID,
		"source_id":    m.SourceID,
		"prompt_ir_id": m.PromptIRID,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	if len(m.Artifacts) == 0 {
		errs = append(errs, fmt.Errorf("artifacts are required"))
	}
	seen := make(map[string]struct{}, len(m.Artifacts))
	for i, artifact := range m.Artifacts {
		if err := artifact.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("artifacts[%d]: %w", i, err))
			continue
		}
		if _, ok := seen[artifact.Path]; ok {
			errs = append(errs, fmt.Errorf("artifacts[%d]: duplicate path %q", i, artifact.Path))
			continue
		}
		seen[artifact.Path] = struct{}{}
	}
	return errors.Join(errs...)
}

type ArtifactRef struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func (r ArtifactRef) Validate() error {
	var errs []error
	for field, value := range map[string]string{
		"name":   r.Name,
		"kind":   r.Kind,
		"path":   r.Path,
		"digest": r.Digest,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	return errors.Join(errs...)
}
