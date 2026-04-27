// artifacts.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// IR artifact persistence.
// Writes the 7 validated JSON artifacts to .tasks/ir/: product definition,
// source IR, provider capabilities, routing decision, output tree spec,
// tool synthesis request, and prompt IR. Each artifact is validated against
// its contract, given a SHA-256 digest, and recorded in a manifest. Source
// material in prompt IR is redacted for secrets before persistence.


package ir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/op7ic/swarmmaker/internal/contracts"
	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/ingestion"
	"github.com/op7ic/swarmmaker/internal/output"
	"github.com/op7ic/swarmmaker/internal/redaction"
	"github.com/op7ic/swarmmaker/prompts"
)

// ArtifactInput contains the typed facts already established before draft
// generation. WriteArtifacts must not invent missing values.
type ArtifactInput struct {
	ProductName   string
	CLIName       string
	Description   string
	InputRoot     string
	OutputRoot    string
	OutputFormats []string
	Ingested      *ingestion.Context
	Providers     []discovery.LLMTool
	Generator     discovery.LLMTool
	Critic        discovery.LLMTool
	RoutingEvents []string
	PromptIR      prompts.PromptIR
	ToolLanguages []string
}

// WriteArtifacts persists the product/source/routing/output/prompt IR files
// into the `.tasks/ir` resident artifact directory.
func WriteArtifacts(dir string, input ArtifactInput) (ArtifactPaths, error) {
	if err := validateInput(dir, input); err != nil {
		return ArtifactPaths{}, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ArtifactPaths{}, fmt.Errorf("create IR artifact dir %s: %w", dir, err)
	}

	ids := stableIDs(input)
	product := buildProductDefinition(ids, input)
	if err := product.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("product definition invalid: %w", err)
	}
	source := buildSourceIR(ids, input.Ingested)
	if err := source.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("source IR invalid: %w", err)
	}
	capabilities := buildProviderCapabilities(input.Providers)
	if err := validateProviderCapabilities(capabilities); err != nil {
		return ArtifactPaths{}, err
	}
	outputSpec, err := buildOutputTreeSpec(ids, input.OutputFormats)
	if err != nil {
		return ArtifactPaths{}, err
	}
	routingDecision := buildRoutingDecision(ids, input, source, outputSpec)
	if err := routingDecision.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("routing decision invalid: %w", err)
	}
	toolRequest := buildToolSynthesisRequest(ids, input, outputSpec)
	if err := toolRequest.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("tool synthesis request invalid: %w", err)
	}
	persistedPromptIR, redactionReport := redactedPromptIR(input.PromptIR)
	promptArtifact := PromptArtifact{
		SchemaVersion:      contracts.SchemaVersionV1,
		PromptIRID:         ids.promptIRID,
		ProductID:          ids.productID,
		SourceID:           ids.sourceID,
		RoutingDecisionID:  ids.routingDecisionID,
		OutputTreeSpecID:   outputSpec.SpecID,
		ToolRequestID:      toolRequest.RequestID,
		PromptIR:           persistedPromptIR,
		SourceMaterialHash: digestString(input.PromptIR.SourceMaterial),
		RedactedSourceHash: digestString(persistedPromptIR.SourceMaterial),
		RedactionReport:    redactionReport,
	}
	if err := promptArtifact.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("prompt IR artifact invalid: %w", err)
	}

	paths := artifactPaths(dir)
	refs, err := writePayloads(paths, []payload{
		{name: "product_definition", kind: "contract", path: paths.ProductDefinitionPath, value: product},
		{name: "source_ir", kind: "contract", path: paths.SourceIRPath, value: source},
		{name: "provider_capabilities", kind: "contract", path: paths.ProviderCapabilitiesPath, value: capabilities},
		{name: "routing_decision", kind: "contract", path: paths.RoutingDecisionPath, value: routingDecision},
		{name: "output_tree_spec", kind: "contract", path: paths.OutputTreeSpecPath, value: outputSpec},
		{name: "tool_synthesis_request", kind: "contract", path: paths.ToolSynthesisRequestPath, value: toolRequest},
		{name: "prompt_ir", kind: "prompt_context", path: paths.PromptIRPath, value: promptArtifact},
	})
	if err != nil {
		return ArtifactPaths{}, err
	}
	manifest := ArtifactManifest{
		SchemaVersion: contracts.SchemaVersionV1,
		ManifestID:    ids.manifestID,
		ProductID:     ids.productID,
		SourceID:      ids.sourceID,
		PromptIRID:    ids.promptIRID,
		Artifacts:     refs,
	}
	if err := manifest.Validate(); err != nil {
		return ArtifactPaths{}, fmt.Errorf("IR manifest invalid: %w", err)
	}
	if err := writeJSON(paths.ManifestPath, manifest); err != nil {
		return ArtifactPaths{}, err
	}
	return paths, nil
}

func redactedPromptIR(input prompts.PromptIR) (prompts.PromptIR, redaction.Report) {
	clone := input
	redacted, report := redaction.Redact(input.SourceMaterial)
	clone.SourceMaterial = redacted
	return clone, report
}

func validateInput(dir string, input ArtifactInput) error {
	var errs []error
	for field, value := range map[string]string{
		"dir":          dir,
		"product_name": input.ProductName,
		"cli_name":     input.CLIName,
		"input_root":   input.InputRoot,
		"output_root":  input.OutputRoot,
	} {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	if len(normalizeOutputFormats(input.OutputFormats)) == 0 {
		errs = append(errs, fmt.Errorf("output_formats is required"))
	}
	if input.Ingested == nil {
		errs = append(errs, fmt.Errorf("ingested context is required"))
	} else if input.Ingested.FileCount == 0 || len(input.Ingested.Files) == 0 {
		errs = append(errs, fmt.Errorf("source IR requires at least one readable document"))
	}
	if err := input.Generator.ValidateKnownMetadata(); err != nil {
		errs = append(errs, fmt.Errorf("generator provider: %w", err))
	}
	if err := input.Critic.ValidateKnownMetadata(); err != nil {
		errs = append(errs, fmt.Errorf("critic provider: %w", err))
	}
	if err := input.PromptIR.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("prompt IR: %w", err))
	}
	return errors.Join(errs...)
}

type ids struct {
	productID         string
	sourceID          string
	routingDecisionID string
	promptIRID        string
	toolRequestID     string
	manifestID        string
}

func stableIDs(input ArtifactInput) ids {
	seed := input.ProductName + "|" + input.InputRoot + "|" + input.OutputRoot + "|" + strings.Join(normalizeOutputFormats(input.OutputFormats), ",")
	short := shortDigest(seed)
	return ids{
		productID:         stableID("product", input.ProductName, short),
		sourceID:          stableID("source", input.InputRoot, short),
		routingDecisionID: "routing-" + short,
		promptIRID:        "prompt-ir-" + short,
		toolRequestID:     "tool-request-" + short,
		manifestID:        "ir-manifest-" + short,
	}
}

func buildProductDefinition(ids ids, input ArtifactInput) contracts.ProductDefinition {
	return contracts.ProductDefinition{
		SchemaVersion: contracts.SchemaVersionV1,
		ProductID:     ids.productID,
		Name:          input.ProductName,
		CLIName:       input.CLIName,
		Description:   input.Description,
		InputRoot:     input.InputRoot,
		OutputRoot:    input.OutputRoot,
		SupportedProviders: []contracts.ProviderName{
			contracts.ProviderClaude,
			contracts.ProviderCodex,
			contracts.ProviderGemini,
		},
	}
}

func buildSourceIR(ids ids, ctx *ingestion.Context) contracts.SourceIR {
	documents := make([]contracts.SourceDocument, 0, len(ctx.Files))
	refs := make([]contracts.EvidenceReference, 0, len(ctx.Files)+len(ctx.BinaryFiles)+len(ctx.Evidence))
	nextID := 1
	for _, file := range ctx.Files {
		evidenceID := evidenceID(nextID)
		nextID++
		digest := digestString(file.Content)
		refs = append(refs, contracts.EvidenceReference{
			SchemaVersion: contracts.SchemaVersionV1,
			EvidenceID:    evidenceID,
			SourceID:      ids.sourceID,
			Path:          file.RelPath,
			Kind:          contracts.EvidenceKindSourceFile,
			Status:        contracts.EvidenceStatusCaptured,
			Digest:        digest,
		})
		documents = append(documents, contracts.SourceDocument{
			Path:          file.RelPath,
			Kind:          sourceKind(file),
			EvidenceID:    evidenceID,
			ContentDigest: digest,
			SizeBytes:     file.Size,
		})
	}
	for _, file := range ctx.BinaryFiles {
		refs = append(refs, contracts.EvidenceReference{
			SchemaVersion: contracts.SchemaVersionV1,
			EvidenceID:    evidenceID(nextID),
			SourceID:      ids.sourceID,
			Path:          file.RelPath,
			Kind:          contracts.EvidenceKindBinaryFile,
			Status:        contracts.EvidenceStatusSkipped,
			Digest:        digestString(fmt.Sprintf("%s|%s|%d", file.RelPath, file.FileType, file.Size)),
		})
		nextID++
	}
	for _, entry := range ctx.Evidence {
		path := evidencePath(entry)
		refs = append(refs, contracts.EvidenceReference{
			SchemaVersion: contracts.SchemaVersionV1,
			EvidenceID:    evidenceID(nextID),
			SourceID:      ids.sourceID,
			Path:          path,
			Kind:          evidenceKind(entry.Category),
			Status:        evidenceStatus(entry.Category),
			Digest:        digestString(evidenceDigestSeed(entry, path)),
		})
		nextID++
	}
	return contracts.SourceIR{
		SchemaVersion:      contracts.SchemaVersionV1,
		SourceID:           ids.sourceID,
		ProductID:          ids.productID,
		RootPath:           ctx.RootPath,
		Documents:          documents,
		EvidenceReferences: refs,
	}
}

func buildProviderCapabilities(tools []discovery.LLMTool) []contracts.ProviderCapability {
	out := make([]contracts.ProviderCapability, 0, len(tools))
	for _, tool := range tools {
		out = append(out, contracts.ProviderCapability{
			SchemaVersion:  contracts.SchemaVersionV1,
			ProviderID:     providerID(tool.Name),
			ProviderName:   contracts.ProviderName(tool.Name),
			BinaryName:     binaryName(tool),
			Available:      tool.Available,
			SupportedRoles: executionRoles(tool.Capabilities),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return out
}

func validateProviderCapabilities(values []contracts.ProviderCapability) error {
	if len(values) == 0 {
		return fmt.Errorf("provider capabilities are required")
	}
	for i, value := range values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("provider capabilities[%d] invalid: %w", i, err)
		}
	}
	return nil
}

func buildOutputTreeSpec(ids ids, formatNames []string) (contracts.OutputTreeSpec, error) {
	formatNames = normalizeOutputFormats(formatNames)
	if len(formatNames) == 0 {
		return contracts.OutputTreeSpec{}, fmt.Errorf("at least one output format is required")
	}
	result := contracts.OutputTreeSpec{
		SchemaVersion: contracts.SchemaVersionV1,
		SpecID:        stableID("output-tree", strings.Join(formatNames, "-"), ids.productID),
		Targets:       make([]contracts.OutputTreeTarget, 0, len(formatNames)),
	}
	for _, formatName := range formatNames {
		format := output.Format(formatName)
		spec, ok := output.DefaultSpecs()[format]
		if !ok {
			return contracts.OutputTreeSpec{}, fmt.Errorf("unsupported output format %q", formatName)
		}
		result.Targets = append(result.Targets, contracts.OutputTreeTarget{
			ProviderName:  contracts.ProviderName(format),
			Format:        contracts.OutputTreeFormat(format),
			RootDir:       spec.RootDir,
			RequiredFiles: append([]string(nil), spec.RequiredFiles...),
		})
	}
	if err := result.Validate(); err != nil {
		return contracts.OutputTreeSpec{}, fmt.Errorf("output tree spec invalid: %w", err)
	}
	return result, nil
}

func buildRoutingDecision(ids ids, input ArtifactInput, source contracts.SourceIR, spec contracts.OutputTreeSpec) contracts.RoutingDecision {
	generatorID := providerID(input.Generator.Name)
	criticID := providerID(input.Critic.Name)
	outputIDs := make([]string, 0, len(spec.Targets))
	for _, target := range spec.Targets {
		outputIDs = append(outputIDs, providerID(string(target.ProviderName)))
	}
	return contracts.RoutingDecision{
		SchemaVersion:        contracts.SchemaVersionV1,
		DecisionID:           ids.routingDecisionID,
		ProductID:            ids.productID,
		SourceID:             source.SourceID,
		Policy:               routingPolicy(generatorID, criticID, outputIDs),
		GeneratorProviderID:  generatorID,
		CriticProviderID:     criticID,
		OutputProviderIDs:    outputIDs,
		CandidateProviderIDs: candidateProviderIDs(input.Providers),
		Reason:               routingReason(input, spec),
		FallbackEvents:       fallbackEvents(input.RoutingEvents),
	}
}

func buildToolSynthesisRequest(ids ids, input ArtifactInput, spec contracts.OutputTreeSpec) contracts.ToolSynthesisRequest {
	return contracts.ToolSynthesisRequest{
		SchemaVersion:        contracts.SchemaVersionV1,
		RequestID:            ids.toolRequestID,
		ProductID:            ids.productID,
		SourceID:             ids.sourceID,
		OutputTreeSpecID:     spec.SpecID,
		Purpose:              "Classify source-evidenced helper tools and generate them only when validation proof can be produced.",
		TargetLanguage:       toolLanguage(input.ToolLanguages),
		RequiredCapabilities: []string{"tool_intent_classification", "proof_of_use_links", "compile_or_runtime_validation"},
	}
}

type payload struct {
	name  string
	kind  string
	path  string
	value any
}

func writePayloads(paths ArtifactPaths, payloads []payload) ([]ArtifactRef, error) {
	refs := make([]ArtifactRef, 0, len(payloads))
	for _, payload := range payloads {
		if err := writeJSON(payload.path, payload.value); err != nil {
			return nil, err
		}
		bytes, err := os.ReadFile(payload.path)
		if err != nil {
			return nil, fmt.Errorf("read %s for digest: %w", payload.path, err)
		}
		rel, err := filepath.Rel(paths.Directory, payload.path)
		if err != nil {
			return nil, fmt.Errorf("relative IR artifact path: %w", err)
		}
		refs = append(refs, ArtifactRef{
			Name:   payload.name,
			Kind:   payload.kind,
			Path:   filepath.ToSlash(rel),
			Digest: digestBytes(bytes),
		})
	}
	return refs, nil
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func artifactPaths(dir string) ArtifactPaths {
	return ArtifactPaths{
		Directory:                dir,
		ManifestPath:             filepath.Join(dir, manifestFileName),
		ProductDefinitionPath:    filepath.Join(dir, "product-definition.json"),
		SourceIRPath:             filepath.Join(dir, "source-ir.json"),
		ProviderCapabilitiesPath: filepath.Join(dir, "provider-capabilities.json"),
		RoutingDecisionPath:      filepath.Join(dir, "routing-decision.json"),
		OutputTreeSpecPath:       filepath.Join(dir, "output-tree-spec.json"),
		ToolSynthesisRequestPath: filepath.Join(dir, "tool-synthesis-request.json"),
		PromptIRPath:             filepath.Join(dir, "prompt-ir.json"),
	}
}

// normalizeOutputFormats deduplicates and lowercases format names, then sorts
// them for deterministic IR artifact IDs. This intentionally differs from
// prompts.normalizedOutputFormatNames which preserves input order.
func normalizeOutputFormats(values []string) []string {
	allowed := map[string]struct{}{
		"claude": {},
		"codex":  {},
		"gemini": {},
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[clean]; !ok {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}
