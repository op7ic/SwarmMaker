// output_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for output renderers and validation.
// Covers all three platform renderers, required file validation, broken
// reference detection, deterministic manifest ordering, and cross-format
// parity validation (including skill drift detection).


package output

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestRegistryRejectsUnsupportedFormat(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	_, err = registry.Render(Format("unsupported"), testBlueprint())
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("expected ErrUnsupportedFormat, got %v", err)
	}
}

func TestRegistryRendersSelectedPlatformSubtrees(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	blueprint := testBlueprint()
	formats := []Format{FormatClaude, FormatCodex, FormatGemini}
	for _, format := range formats {
		spec, ok := registry.Spec(format)
		if !ok {
			t.Fatalf("missing spec for %s", format)
		}

		manifest, err := registry.Render(format, blueprint)
		if err != nil {
			t.Fatalf("Render(%s) failed: %v", format, err)
		}
		if manifest.RootDir != spec.RootDir {
			t.Fatalf("manifest root = %q, want %q", manifest.RootDir, spec.RootDir)
		}

		for _, required := range spec.RequiredFiles {
			fullPath, err := joinRoot(spec.RootDir, required)
			if err != nil {
				t.Fatalf("joinRoot(%s) failed: %v", required, err)
			}
			if !manifestHasPath(manifest, fullPath) {
				t.Fatalf("manifest missing required file %s", fullPath)
			}
		}

		for prefix, minCount := range spec.RequiredPrefixCounts {
			if got := manifestPrefixCount(manifest, prefix); got < minCount {
				t.Fatalf("manifest prefix %q count = %d, want at least %d", prefix, got, minCount)
			}
		}

		skillRoot := spec.RootDir + "/" + spec.SkillDir + "/"
		for _, file := range manifest.Files {
			if !strings.HasPrefix(file.Path, spec.RootDir+"/") {
				t.Fatalf("file %q escaped root dir %q", file.Path, spec.RootDir)
			}
			if file.Path == spec.RootDir+"/"+spec.ReadmeFile ||
				file.Path == spec.RootDir+"/"+spec.EntryFile ||
				(spec.SkillIndexFile != "" && file.Path == spec.RootDir+"/"+spec.SkillIndexFile) {
				continue
			}
			if !strings.HasPrefix(file.Path, skillRoot) {
				t.Fatalf("file %q is outside the selected platform subtree", file.Path)
			}
		}
	}
}

func TestRegistryValidationRules(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	blueprint := testBlueprint()
	manifest, err := registry.Render(FormatClaude, blueprint)
	if err != nil {
		t.Fatalf("Render failed for valid blueprint: %v", err)
	}

	spec, ok := registry.Spec(FormatClaude)
	if !ok {
		t.Fatal("missing claude spec")
	}

	missingRequiredFile := manifest
	missingRequiredFile.Metadata = cloneMetadata(manifest.Metadata)
	filtered := make([]FileArtifact, 0, len(missingRequiredFile.Files))
	for _, file := range missingRequiredFile.Files {
		if file.Path == spec.RootDir+"/"+spec.ReadmeFile {
			continue
		}
		filtered = append(filtered, file)
	}
	missingRequiredFile.Files = filtered
	if err := ValidateManifest(spec, missingRequiredFile); err == nil || !strings.Contains(err.Error(), "required file missing") {
		t.Fatalf("expected required file validation failure, got %v", err)
	}

	missingMetadataKey := manifest
	missingMetadataKey.Metadata = cloneMetadata(manifest.Metadata)
	delete(missingMetadataKey.Metadata, "purpose")
	if err := ValidateManifest(spec, missingMetadataKey); err == nil || !strings.Contains(err.Error(), "required metadata key") {
		t.Fatalf("expected metadata validation failure, got %v", err)
	}

	legacyDocTree := manifest
	legacyDocTree.Metadata = cloneMetadata(manifest.Metadata)
	legacyDocTree.Files = append(legacyDocTree.Files, FileArtifact{
		Path:    spec.RootDir + "/docs/overview.md",
		Content: "legacy document tree content",
	})
	sort.Slice(legacyDocTree.Files, func(i, j int) bool {
		return legacyDocTree.Files[i].Path < legacyDocTree.Files[j].Path
	})
	if err := ValidateManifest(spec, legacyDocTree); err == nil || !strings.Contains(err.Error(), "outside the selected platform subtree") {
		t.Fatalf("expected subtree validation failure, got %v", err)
	}

	invalidLink := Blueprint{
		Name:    "SwarmMaker",
		Purpose: "Generate a production swarm tree",
		Metadata: map[string]string{
			"owner": "teammate-3",
		},
		Agents: testAgents(),
		Skills: testSkills(),
		Docs: []Document{
			{Path: "docs/overview.md", Content: "overview"},
		},
	}
	invalidLink.Skills[0].Body = "See [Escape](../../../escape.md)."
	if _, err := registry.Render(FormatClaude, invalidLink); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("expected relative link validation failure, got %v", err)
	}
}

func TestRegistryRequiresAgentsAndSkills(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	_, err = registry.Render(FormatGemini, Blueprint{
		Name:    "SwarmMaker",
		Purpose: "Generate a production swarm tree",
		Metadata: map[string]string{
			"owner": "teammate-3",
		},
		Skills: testSkills(),
	})
	if err == nil || !strings.Contains(err.Error(), "agents are required") {
		t.Fatalf("expected missing agents failure, got %v", err)
	}

	_, err = registry.Render(FormatGemini, Blueprint{
		Name:    "SwarmMaker",
		Purpose: "Generate a production swarm tree",
		Metadata: map[string]string{
			"owner": "teammate-3",
		},
		Agents: testAgents(),
	})
	if err == nil || !strings.Contains(err.Error(), "skills are required") {
		t.Fatalf("expected missing skills failure, got %v", err)
	}
}

func TestRegistryProducesDeterministicManifest(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	metadata := map[string]string{
		"owner": "teammate-3",
	}
	first := Blueprint{
		Name:     "Deterministic",
		Purpose:  "Validate stable output ordering",
		Metadata: metadata,
		Agents:   testAgents(),
		Skills:   reversedSkills(),
		Docs: []Document{
			{Path: "tools/b.md", Content: "B"},
			{Path: "docs/a.md", Content: "A"},
		},
	}
	second := Blueprint{
		Name:     "Deterministic",
		Purpose:  "Validate stable output ordering",
		Metadata: metadata,
		Agents:   testAgents(),
		Skills:   testSkills(),
		Docs: []Document{
			{Path: "docs/a.md", Content: "A"},
			{Path: "tools/b.md", Content: "B"},
		},
	}

	firstManifest, err := registry.Render(FormatGemini, first)
	if err != nil {
		t.Fatalf("Render(first) failed: %v", err)
	}
	secondManifest, err := registry.Render(FormatGemini, second)
	if err != nil {
		t.Fatalf("Render(second) failed: %v", err)
	}

	firstJSON, err := StableJSON(firstManifest)
	if err != nil {
		t.Fatalf("StableJSON(first) failed: %v", err)
	}
	secondJSON, err := StableJSON(secondManifest)
	if err != nil {
		t.Fatalf("StableJSON(second) failed: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("expected deterministic manifest JSON\nfirst:\n%s\nsecond:\n%s", firstJSON, secondJSON)
	}
}

func TestValidateManifestParityPassesForSharedBlueprint(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	blueprint := testBlueprint()
	formats := []Format{FormatClaude, FormatCodex, FormatGemini}
	manifests := make([]Manifest, 0, len(formats))
	for _, format := range formats {
		manifest, err := registry.Render(format, blueprint)
		if err != nil {
			t.Fatalf("Render(%s) failed: %v", format, err)
		}
		manifests = append(manifests, manifest)
	}

	if issues := ValidateManifestParity(blueprint, manifests); len(issues) != 0 {
		t.Fatalf("ValidateManifestParity returned issues: %#v", issues)
	}
}

func TestValidateManifestParityDetectsSkillDrift(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	blueprint := testBlueprint()
	codexManifest, err := registry.Render(FormatCodex, blueprint)
	if err != nil {
		t.Fatalf("Render(%s) failed: %v", FormatCodex, err)
	}

	for i := range codexManifest.Files {
		if strings.HasSuffix(codexManifest.Files[i].Path, "/ingest.md") {
			codexManifest.Files[i].Content = strings.Replace(codexManifest.Files[i].Content, "Normalize input docs", "Drifted summary", 1)
			break
		}
	}

	issues := ValidateManifestParity(blueprint, []Manifest{codexManifest})
	if len(issues) == 0 {
		t.Fatal("expected parity issues, got none")
	}
	found := false
	for _, issue := range issues {
		if issue.Format == FormatCodex && strings.Contains(issue.Problem, "content drifted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected codex skill drift issue, got %#v", issues)
	}
}

func manifestHasPath(manifest Manifest, want string) bool {
	for _, file := range manifest.Files {
		if file.Path == want {
			return true
		}
	}
	return false
}

func manifestPrefixCount(manifest Manifest, prefix string) int {
	count := 0
	for _, file := range manifest.Files {
		if strings.HasPrefix(file.Path, prefix) {
			count++
		}
	}
	return count
}

func testBlueprint() Blueprint {
	return Blueprint{
		Name:    "SwarmMaker",
		Purpose: "Generate a production swarm tree",
		Metadata: map[string]string{
			"owner": "teammate-3",
		},
		Agents: testAgents(),
		Skills: testSkills(),
		Docs: []Document{
			{Path: "docs/overview.md", Content: "Overview"},
			{Path: "docs/api.md", Content: "API"},
		},
	}
}

func testAgents() []Agent {
	return []Agent{
		{Name: "Observe", Role: "Collect evidence", Instructions: "Inventory inputs and evidence."},
		{Name: "Orient", Role: "Normalize facts", Instructions: "Map requirements and UNKNOWN values."},
		{Name: "Decide", Role: "Choose actions", Instructions: "Select validated next actions."},
		{Name: "Act", Role: "Execute changes", Instructions: "Apply changes and record proof."},
	}
}

func testSkills() []Skill {
	return []Skill{
		{Name: "Ingest", Slug: "ingest", Summary: "Normalize input docs", Body: "Read source files and preserve evidence."},
		{Name: "Validate", Slug: "validate", Summary: "Check output tree", Body: "Validate file structure, links, and counts."},
	}
}

func reversedSkills() []Skill {
	return []Skill{
		{Name: "Validate", Slug: "validate", Summary: "Check output tree", Body: "Validate file structure, links, and counts."},
		{Name: "Ingest", Slug: "ingest", Summary: "Normalize input docs", Body: "Read source files and preserve evidence."},
	}
}

func TestJoinRootReturnsErrorForMalformedPaths(t *testing.T) {
	tests := []struct {
		name    string
		rootDir string
		rel     string
	}{
		{"empty root", "", "file.md"},
		{"empty rel", ".claude", ""},
		{"backslash in rel", ".claude", "skills\\file.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := joinRoot(tt.rootDir, tt.rel)
			if err == nil {
				t.Fatalf("joinRoot(%q, %q) = nil error, want error", tt.rootDir, tt.rel)
			}
		})
	}
}

func TestMustJoinRootReturnsEmptyOnError(t *testing.T) {
	result := mustJoinRoot("", "file.md")
	if result != "" {
		t.Fatalf("mustJoinRoot with empty root = %q, want empty string", result)
	}
}
