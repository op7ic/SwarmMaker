// artifacts_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for IR artifact persistence.
// Covers artifact writing, JSON round-trip integrity, digest verification,
// manifest completeness, redaction effectiveness, tool language ambiguity
// handling, and multi-target output contracts.


package ir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/op7ic/swarmmaker/internal/contracts"
	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/ingestion"
	"github.com/op7ic/swarmmaker/prompts"
)

func TestWriteArtifactsPersistsValidatedIR(t *testing.T) {
	dir := t.TempDir()
	irDir := filepath.Join(dir, ".tasks", "ir")
	input := testArtifactInput(dir, irDir)

	paths, err := WriteArtifacts(irDir, input)
	if err != nil {
		t.Fatalf("WriteArtifacts failed: %v", err)
	}
	if paths.Directory != irDir {
		t.Fatalf("artifact directory = %q, want %q", paths.Directory, irDir)
	}
	if !strings.Contains(filepath.ToSlash(paths.ManifestPath), "/.tasks/ir/") {
		t.Fatalf("manifest path should reside under .tasks/ir, got %q", paths.ManifestPath)
	}

	for _, path := range []string{
		paths.ManifestPath,
		paths.ProductDefinitionPath,
		paths.SourceIRPath,
		paths.ProviderCapabilitiesPath,
		paths.RoutingDecisionPath,
		paths.OutputTreeSpecPath,
		paths.ToolSynthesisRequestPath,
		paths.PromptIRPath,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}

	var source contracts.SourceIR
	readJSON(t, paths.SourceIRPath, &source)
	if err := source.Validate(); err != nil {
		t.Fatalf("persisted source IR invalid: %v", err)
	}
	if len(source.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(source.Documents))
	}
	if source.Documents[0].ContentDigest == "" || !strings.HasPrefix(source.Documents[0].ContentDigest, "sha256:") {
		t.Fatalf("source digest missing: %#v", source.Documents[0])
	}

	var routing contracts.RoutingDecision
	readJSON(t, paths.RoutingDecisionPath, &routing)
	if err := routing.Validate(); err != nil {
		t.Fatalf("persisted routing decision invalid: %v", err)
	}
	if len(routing.FallbackEvents) != 1 {
		t.Fatalf("fallback events = %d, want 1", len(routing.FallbackEvents))
	}

	var promptArtifact PromptArtifact
	readJSON(t, paths.PromptIRPath, &promptArtifact)
	if err := promptArtifact.Validate(); err != nil {
		t.Fatalf("persisted prompt artifact invalid: %v", err)
	}
	if promptArtifact.PromptIR.IRManifestPath != filepath.Join(dir, ".tasks", "manifest.json") {
		t.Fatalf("prompt IR manifest path = %q, want %q", promptArtifact.PromptIR.IRManifestPath, filepath.Join(dir, ".tasks", "manifest.json"))
	}

	var manifest ArtifactManifest
	readJSON(t, paths.ManifestPath, &manifest)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("persisted artifact manifest invalid: %v", err)
	}
	if len(manifest.Artifacts) != 7 {
		t.Fatalf("manifest artifacts = %d, want 7", len(manifest.Artifacts))
	}
}

func TestWriteArtifactsRejectsEmptyReadableSource(t *testing.T) {
	dir := t.TempDir()
	input := testArtifactInput(dir, filepath.Join(dir, ".tasks", "ir"))
	input.Ingested.Files = nil
	input.Ingested.FileCount = 0

	_, err := WriteArtifacts(filepath.Join(dir, ".tasks", "ir"), input)
	if err == nil || !strings.Contains(err.Error(), "at least one readable document") {
		t.Fatalf("expected readable source failure, got %v", err)
	}
}

func TestToolLanguageAmbiguityPersistsUnknown(t *testing.T) {
	dir := t.TempDir()
	input := testArtifactInput(dir, filepath.Join(dir, ".tasks", "ir"))
	input.ToolLanguages = []string{"go", "python"}

	paths, err := WriteArtifacts(filepath.Join(dir, ".tasks", "ir"), input)
	if err != nil {
		t.Fatalf("WriteArtifacts failed: %v", err)
	}

	var request contracts.ToolSynthesisRequest
	readJSON(t, paths.ToolSynthesisRequestPath, &request)
	if request.TargetLanguage != contracts.ToolLanguageUnknown {
		t.Fatalf("target language = %q, want UNKNOWN", request.TargetLanguage)
	}
}

func TestWriteArtifactsRedactsPersistedPromptSource(t *testing.T) {
	dir := t.TempDir()
	input := testArtifactInput(dir, filepath.Join(dir, ".tasks", "ir"))
	input.PromptIR.SourceMaterial = "api_key = sk-live-1234567890\nAuthorization: Bearer abcdefghijklmnopqrstuvwxyz\n"

	paths, err := WriteArtifacts(filepath.Join(dir, ".tasks", "ir"), input)
	if err != nil {
		t.Fatalf("WriteArtifacts failed: %v", err)
	}

	data, err := os.ReadFile(paths.PromptIRPath)
	if err != nil {
		t.Fatalf("read prompt IR: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{"sk-live-1234567890", "abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("prompt IR leaked %q:\n%s", forbidden, text)
		}
	}

	var promptArtifact PromptArtifact
	readJSON(t, paths.PromptIRPath, &promptArtifact)
	if !promptArtifact.RedactionReport.Redacted {
		t.Fatalf("expected redaction report, got %#v", promptArtifact.RedactionReport)
	}
	if promptArtifact.SourceMaterialHash == promptArtifact.RedactedSourceHash {
		t.Fatalf("source hash and redacted hash should differ after redaction")
	}
}

func TestWriteArtifactsSupportsMultiTargetOutputContracts(t *testing.T) {
	dir := t.TempDir()
	input := testArtifactInput(dir, filepath.Join(dir, ".tasks", "ir"))
	input.OutputFormats = []string{"gemini", "claude", "codex"}
	input.PromptIR.TargetFormats = []string{"gemini", "claude", "codex"}
	input.PromptIR.OutputRenderers = []string{"gemini", "claude", "codex"}

	paths, err := WriteArtifacts(filepath.Join(dir, ".tasks", "ir"), input)
	if err != nil {
		t.Fatalf("WriteArtifacts failed: %v", err)
	}

	var spec contracts.OutputTreeSpec
	readJSON(t, paths.OutputTreeSpecPath, &spec)
	if err := spec.Validate(); err != nil {
		t.Fatalf("persisted multi-target output tree spec invalid: %v", err)
	}
	if len(spec.Targets) != 3 {
		t.Fatalf("targets = %d, want 3", len(spec.Targets))
	}

	var routing contracts.RoutingDecision
	readJSON(t, paths.RoutingDecisionPath, &routing)
	if err := routing.Validate(); err != nil {
		t.Fatalf("persisted multi-target routing decision invalid: %v", err)
	}
	if len(routing.OutputProviderIDs) != 3 {
		t.Fatalf("output providers = %d, want 3", len(routing.OutputProviderIDs))
	}
	if routing.Policy != contracts.RoutingPolicyCapabilityBased && routing.Policy != contracts.RoutingPolicySameModelCritique {
		t.Fatalf("unexpected routing policy for multi-target run: %q", routing.Policy)
	}
}

func TestPromptArtifactRejectsUnknownSchemaVersion(t *testing.T) {
	p := PromptArtifact{
		SchemaVersion:      "v99",
		PromptIRID:         "id",
		ProductID:          "id",
		SourceID:           "id",
		RoutingDecisionID:  "id",
		OutputTreeSpecID:   "id",
		ToolRequestID:      "id",
		SourceMaterialHash: "hash",
		RedactedSourceHash: "hash",
		PromptIR: prompts.PromptIR{
			ProjectName:      "test",
			SourceMaterial:   "src",
			TargetFormats:    []string{"gemini"},
			GeneratorProvider: "codex",
			CriticProvider:   "gemini",
			OutputRenderers:  []string{"gemini"},
			PromptPackName:   "default",
			PromptPackSource: "embedded",
			PromptPackDigest: "sha256:test",
		},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for unknown schema version")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unsupported") || !strings.Contains(msg, "v99") {
		t.Fatalf("error should mention unsupported and v99, got: %s", msg)
	}
}

func TestArtifactManifestRejectsUnknownSchemaVersion(t *testing.T) {
	m := ArtifactManifest{
		SchemaVersion: "v99",
		ManifestID:    "id",
		ProductID:     "id",
		SourceID:      "id",
		PromptIRID:    "id",
		Artifacts: []ArtifactRef{
			{Name: "a", Kind: "file", Path: "/a", Digest: "sha256:abc"},
		},
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("expected error for unknown schema version")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unsupported") || !strings.Contains(msg, "v99") {
		t.Fatalf("error should mention unsupported and v99, got: %s", msg)
	}
}

func testArtifactInput(root, irDir string) ArtifactInput {
	evidencePath := filepath.Join(root, ".tasks", "evidence.json")
	manifestPath := filepath.Join(root, ".tasks", "manifest.json")
	return ArtifactInput{
		ProductName:   "SwarmMaker",
		CLIName:       "swarm-maker",
		Description:   "Turn loose documentation into agent swarms.",
		InputRoot:     filepath.Join(root, "input"),
		OutputRoot:    root,
		OutputFormats: []string{"gemini"},
		Ingested: &ingestion.Context{
			RootPath:   filepath.Join(root, "input"),
			FileCount:  1,
			TotalBytes: 19,
			Files: []ingestion.FileEntry{
				{RelPath: "api-spec.md", Content: "# API\nBuild tool.\n", Size: 19, FileType: "markdown"},
			},
			BinaryFiles: []ingestion.FileEntry{
				{RelPath: "diagram.png", Size: 12, FileType: "image"},
			},
			Evidence: []ingestion.EvidenceEntry{
				{
					Phase:    ingestion.EvidencePhaseIngestion,
					Category: ingestion.EvidenceCategoryHiddenPath,
					RelPath:  ".env",
					Detail:   "hidden path excluded from ingestion",
				},
			},
		},
		Providers: []discovery.LLMTool{
			testTool("codex", true),
			testTool("gemini", true),
			testTool("claude", false),
		},
		Generator:     testTool("codex", true),
		Critic:        testTool("gemini", true),
		RoutingEvents: []string{"auto-selected critic provider \"gemini\" because --critique was not set"},
		PromptIR: prompts.PromptIR{
			ProjectName:          "SwarmMaker",
			SourceMaterial:       "# API\nBuild tool.\n",
			TargetFormats:        []string{"gemini"},
			GeneratorProvider:    "codex",
			CriticProvider:       "gemini",
			OutputRenderers:      []string{"gemini"},
			EvidenceManifestPath: evidencePath,
			IRManifestPath:       manifestPath,
			PromptPackName:       "swarmmaker-default",
			PromptPackSource:     "embedded:prompts/default_pack.json",
			PromptPackDigest:     "sha256:test",
			InputFileCount:       1,
			BinaryFileCount:      1,
			EvidenceEventCount:   1,
			ToolLanguages:        []string{"go"},
		},
		ToolLanguages: []string{"go"},
	}
}

func testTool(name string, available bool) discovery.LLMTool {
	return discovery.LLMTool{
		Name:      name,
		Path:      "/usr/bin/" + name,
		Available: available,
		Capabilities: []discovery.Capability{
			discovery.CapabilityGenerate,
			discovery.CapabilityCritique,
			discovery.CapabilityRenderOutput,
			discovery.CapabilityBuildTools,
		},
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
