// product_source.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Product definition and source IR contracts.
// Defines ProductDefinition, SourceDocument, EvidenceReference, and SourceIR
// types with cross-referential validation (evidence IDs checked against
// document references, duplicate detection).


package contracts

import (
	"errors"
	"fmt"
)

type ProductDefinition struct {
	SchemaVersion      string         `json:"schema_version"`
	ProductID          string         `json:"product_id"`
	Name               string         `json:"name"`
	CLIName            string         `json:"cli_name"`
	Description        string         `json:"description,omitempty"`
	InputRoot          string         `json:"input_root"`
	OutputRoot         string         `json:"output_root"`
	SupportedProviders []ProviderName `json:"supported_providers"`
}

func (p ProductDefinition) Validate() error {
	errs := []error{validateSchemaVersion(p.SchemaVersion)}
	errs = append(errs,
		validateID("product_id", p.ProductID),
		validateNonEmptyField("name", p.Name),
		validateNonEmptyField("cli_name", p.CLIName),
		validateNonEmptyField("input_root", p.InputRoot),
		validateNonEmptyField("output_root", p.OutputRoot),
		validateStringList("supported_providers", providerNames(p.SupportedProviders), true),
	)
	for i, provider := range p.SupportedProviders {
		if _, ok := knownProviderNames()[string(provider)]; !ok {
			errs = append(errs, fmt.Errorf("supported_providers[%d]: %w", i, ErrUnknownProviderName))
		}
	}
	return errors.Join(filterErrors(errs)...)
}

type SourceDocument struct {
	Path          string     `json:"path"`
	Kind          SourceKind `json:"kind"`
	EvidenceID    string     `json:"evidence_id"`
	ContentDigest string     `json:"content_digest"`
	SizeBytes     int64      `json:"size_bytes"`
}

func (d SourceDocument) Validate() error {
	errs := []error{
		validateNonEmptyField("path", d.Path),
		validateEnumValue("kind", string(d.Kind), knownSourceKinds(), ErrUnknownSourceKind),
		validateID("evidence_id", d.EvidenceID),
		validateNonEmptyField("content_digest", d.ContentDigest),
	}
	if d.SizeBytes < 0 {
		errs = append(errs, fmt.Errorf("size_bytes must be non-negative"))
	}
	return errors.Join(filterErrors(errs)...)
}

type EvidenceReference struct {
	SchemaVersion string         `json:"schema_version"`
	EvidenceID    string         `json:"evidence_id"`
	SourceID      string         `json:"source_id"`
	Path          string         `json:"path"`
	Kind          EvidenceKind   `json:"kind"`
	Status        EvidenceStatus `json:"status"`
	Digest        string         `json:"digest"`
}

func (e EvidenceReference) Validate() error {
	errs := []error{validateSchemaVersion(e.SchemaVersion)}
	errs = append(errs,
		validateID("evidence_id", e.EvidenceID),
		validateID("source_id", e.SourceID),
		validateNonEmptyField("path", e.Path),
		validateEnumValue("kind", string(e.Kind), knownEvidenceKinds(), ErrUnknownEvidenceKind),
		validateEnumValue("status", string(e.Status), knownEvidenceStatuses(), ErrUnknownEvidenceStatus),
		validateNonEmptyField("digest", e.Digest),
	)
	return errors.Join(filterErrors(errs)...)
}

type SourceIR struct {
	SchemaVersion      string              `json:"schema_version"`
	SourceID           string              `json:"source_id"`
	ProductID          string              `json:"product_id"`
	RootPath           string              `json:"root_path"`
	Documents          []SourceDocument    `json:"documents"`
	EvidenceReferences []EvidenceReference `json:"evidence_references"`
}

func (s SourceIR) Validate() error {
	errs := []error{validateSchemaVersion(s.SchemaVersion)}
	errs = append(errs,
		validateID("source_id", s.SourceID),
		validateID("product_id", s.ProductID),
		validateNonEmptyField("root_path", s.RootPath),
	)
	if len(s.Documents) == 0 {
		errs = append(errs, fmt.Errorf("%w: documents", ErrMissingRequiredField))
	}
	if len(s.EvidenceReferences) == 0 {
		errs = append(errs, fmt.Errorf("%w: evidence_references", ErrMissingRequiredField))
	}

	evidenceIDs := make(map[string]struct{}, len(s.EvidenceReferences))
	for i, ref := range s.EvidenceReferences {
		if err := ref.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("evidence_references[%d]: %w", i, err))
			continue
		}
		if _, ok := evidenceIDs[ref.EvidenceID]; ok {
			errs = append(errs, fmt.Errorf("evidence_references[%d]: duplicate evidence_id %q", i, ref.EvidenceID))
			continue
		}
		evidenceIDs[ref.EvidenceID] = struct{}{}
	}
	for i, doc := range s.Documents {
		if err := doc.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("documents[%d]: %w", i, err))
			continue
		}
		if _, ok := evidenceIDs[doc.EvidenceID]; !ok {
			errs = append(errs, fmt.Errorf("documents[%d]: evidence_id %q has no matching evidence reference", i, doc.EvidenceID))
		}
	}

	return errors.Join(filterErrors(errs)...)
}

func providerNames(values []ProviderName) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}
