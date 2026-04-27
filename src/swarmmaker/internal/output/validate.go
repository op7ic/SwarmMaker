// validate.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Output manifest validation and parity checking.
// ValidateManifest checks path sanity (no escapes, no backslashes, no
// absolutes), required file presence, prefix counts, and markdown link
// resolution within tree boundaries. ValidateManifestParity checks
// cross-manifest consistency when multiple formats are rendered from
// the same ledger -- catches skill drift, agent drift, metadata drift,
// and source reference drift between platforms.


package output

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

// ParityIssue captures cross-target drift between a rendered manifest and the
// shared blueprint derived from `.tasks/`.
type ParityIssue struct {
	Format  Format
	File    string
	Problem string
}

// ValidateManifest checks required paths, metadata, path sanity, and link sanity.
func ValidateManifest(spec TreeSpec, manifest Manifest) error {
	if err := validateTreeSpec(spec); err != nil {
		return err
	}
	if manifest.Format != spec.Format {
		return fmt.Errorf("manifest format %q does not match tree spec %q", manifest.Format, spec.Format)
	}
	if manifest.RootDir != spec.RootDir {
		return fmt.Errorf("manifest root %q does not match tree spec root %q", manifest.RootDir, spec.RootDir)
	}
	if len(manifest.Metadata) == 0 {
		return fmt.Errorf("manifest metadata is required")
	}
	for _, key := range spec.RequiredMetadataKeys {
		if strings.TrimSpace(manifest.Metadata[key]) == "" {
			return fmt.Errorf("required metadata key %q is missing", key)
		}
	}
	allowed := make(map[string]struct{}, len(spec.RequiredFiles))
	for _, required := range spec.RequiredFiles {
		joined, err := joinRoot(spec.RootDir, required)
		if err != nil {
			return fmt.Errorf("required file path %q: %w", required, err)
		}
		allowed[joined] = struct{}{}
	}
	skillRoot := ""
	if strings.TrimSpace(spec.SkillDir) != "" {
		skillRoot = path.Clean(path.Join(spec.RootDir, spec.SkillDir))
	}
	paths := make(map[string]struct{}, len(manifest.Files))
	sortedPaths := make([]string, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		normalized, err := validateTreePath(manifest.RootDir, file.Path)
		if err != nil {
			return fmt.Errorf("file path %q: %w", file.Path, err)
		}
		if _, exists := paths[normalized]; exists {
			return fmt.Errorf("duplicate file path %q", normalized)
		}
		paths[normalized] = struct{}{}
		sortedPaths = append(sortedPaths, normalized)
	}
	if !sort.StringsAreSorted(sortedPaths) {
		return fmt.Errorf("manifest files are not deterministically ordered")
	}
	for _, file := range manifest.Files {
		normalized, err := validateTreePath(manifest.RootDir, file.Path)
		if err != nil {
			return fmt.Errorf("file path %q: %w", file.Path, err)
		}
		if _, ok := allowed[normalized]; ok {
			continue
		}
		if skillRoot != "" && (normalized == skillRoot || strings.HasPrefix(normalized, skillRoot+"/")) {
			continue
		}
		return fmt.Errorf("file %q is outside the selected platform subtree", normalized)
	}
	for _, required := range spec.RequiredFiles {
		joined, err := joinRoot(spec.RootDir, required)
		if err != nil {
			return fmt.Errorf("required file path %q: %w", required, err)
		}
		if _, ok := paths[joined]; !ok {
			return fmt.Errorf("required file missing: %s", joined)
		}
	}
	for prefix, minCount := range spec.RequiredPrefixCounts {
		if minCount < 0 {
			return fmt.Errorf("required prefix count for %q cannot be negative", prefix)
		}
		count := 0
		for _, normalized := range sortedPaths {
			if strings.HasPrefix(normalized, prefix) {
				count++
			}
		}
		if count < minCount {
			return fmt.Errorf("prefix %q requires at least %d files, found %d", prefix, minCount, count)
		}
	}
	for _, file := range manifest.Files {
		if err := validateMarkdownLinks(manifest.RootDir, file.Path, file.Content); err != nil {
			return fmt.Errorf("file %q: %w", file.Path, err)
		}
	}
	return nil
}

// ValidateManifestParity checks that each rendered manifest preserves the same
// skill, agent, metadata, and source coverage described by the shared
// blueprint. This catches renderer-specific drift before bundle files are
// written to disk.
func ValidateManifestParity(blueprint Blueprint, manifests []Manifest) []ParityIssue {
	if err := validateBlueprint(blueprint); err != nil {
		return []ParityIssue{{Problem: fmt.Sprintf("invalid blueprint: %v", err)}}
	}
	if len(manifests) == 0 {
		return []ParityIssue{{Problem: "no rendered manifests provided"}}
	}

	specs := DefaultSpecs()
	expectedSkills := sortedSkills(blueprint.Skills)
	expectedAgents := sortedAgents(blueprint.Agents)
	expectedDocs := sortedDocs(blueprint.Docs)
	seenFormats := make(map[Format]struct{}, len(manifests))
	var issues []ParityIssue

	for _, manifest := range manifests {
		spec, ok := specs[manifest.Format]
		if !ok {
			issues = append(issues, ParityIssue{
				Format:  manifest.Format,
				Problem: fmt.Sprintf("unsupported rendered format %q", manifest.Format),
			})
			continue
		}
		if _, exists := seenFormats[manifest.Format]; exists {
			issues = append(issues, ParityIssue{
				Format:  manifest.Format,
				Problem: "duplicate manifest for format",
			})
			continue
		}
		seenFormats[manifest.Format] = struct{}{}

		files := fileArtifactMap(manifest.Files)
		readmePath := mustJoinRoot(spec.RootDir, spec.ReadmeFile)
		entryPath := mustJoinRoot(spec.RootDir, spec.EntryFile)
		indexPath := ""
		if strings.TrimSpace(spec.SkillIndexFile) != "" {
			indexPath = mustJoinRoot(spec.RootDir, spec.SkillIndexFile)
		}

		issues = append(issues, validateManifestMetadataParity(blueprint, manifest)...)
		issues = append(issues, validateManifestAgentParity(manifest.Format, files, readmePath, entryPath, expectedAgents)...)
		issues = append(issues, validateManifestSourceParity(manifest.Format, files, readmePath, indexPath, expectedDocs)...)
		issues = append(issues, validateManifestSkillParity(spec, manifest.Format, files, indexPath, expectedSkills)...)
	}

	return issues
}

// CanonicalizeManifest sorts manifest files so serialization stays deterministic.
func CanonicalizeManifest(manifest Manifest) Manifest {
	clone := Manifest{
		Format:   manifest.Format,
		RootDir:  manifest.RootDir,
		Metadata: cloneMetadata(manifest.Metadata),
		Files:    append([]FileArtifact(nil), manifest.Files...),
	}
	sort.Slice(clone.Files, func(i, j int) bool {
		return clone.Files[i].Path < clone.Files[j].Path
	})
	return clone
}

func validateManifestMetadataParity(blueprint Blueprint, manifest Manifest) []ParityIssue {
	expected := map[string]string{
		"name":        blueprint.Name,
		"purpose":     blueprint.Purpose,
		"format":      string(manifest.Format),
		"skill_count": fmt.Sprintf("%d", len(blueprint.Skills)),
		"agent_count": fmt.Sprintf("%d", len(blueprint.Agents)),
	}
	if _, ok := manifest.Metadata["document_count"]; ok {
		expected["document_count"] = fmt.Sprintf("%d", len(blueprint.Docs))
	}
	if _, ok := manifest.Metadata["source_count"]; ok {
		expected["source_count"] = fmt.Sprintf("%d", len(blueprint.Docs))
	}

	var issues []ParityIssue
	for key, want := range expected {
		if got := strings.TrimSpace(manifest.Metadata[key]); got != want {
			issues = append(issues, ParityIssue{
				Format:  manifest.Format,
				File:    "metadata",
				Problem: fmt.Sprintf("metadata %q = %q, want %q", key, got, want),
			})
		}
	}
	return issues
}

func validateManifestAgentParity(format Format, files map[string]string, readmePath, entryPath string, expectedAgents []Agent) []ParityIssue {
	var issues []ParityIssue
	readmeContent, readmeOK := files[readmePath]
	entryContent, entryOK := files[entryPath]
	if !readmeOK {
		issues = append(issues, ParityIssue{
			Format:  format,
			File:    readmePath,
			Problem: "readme missing from manifest parity check",
		})
	}
	if !entryOK {
		issues = append(issues, ParityIssue{
			Format:  format,
			File:    entryPath,
			Problem: "entry file missing from manifest parity check",
		})
	}
	for _, agent := range expectedAgents {
		line := "- " + agent.Name + ": " + agent.Role
		if readmeOK && !strings.Contains(readmeContent, line) {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    readmePath,
				Problem: fmt.Sprintf("agent %q missing from readme coordination roles", agent.Name),
			})
		}
		if entryOK && !strings.Contains(entryContent, line) {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    entryPath,
				Problem: fmt.Sprintf("agent %q missing from entry coordination roles", agent.Name),
			})
		}
	}
	return issues
}

func validateManifestSourceParity(format Format, files map[string]string, readmePath, indexPath string, expectedDocs []Document) []ParityIssue {
	var issues []ParityIssue
	readmeContent, readmeOK := files[readmePath]
	indexContent := ""
	indexOK := false
	if indexPath != "" {
		indexContent, indexOK = files[indexPath]
		if !indexOK {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    indexPath,
				Problem: "skill index missing from manifest parity check",
			})
		}
	}
	for _, doc := range expectedDocs {
		want := "`" + doc.Path + "`"
		if readmeOK && !strings.Contains(readmeContent, want) {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    readmePath,
				Problem: fmt.Sprintf("source document %q missing from readme", doc.Path),
			})
		}
		if indexOK && !strings.Contains(indexContent, want) {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    indexPath,
				Problem: fmt.Sprintf("source document %q missing from skill index", doc.Path),
			})
		}
	}
	return issues
}

func validateManifestSkillParity(spec TreeSpec, format Format, files map[string]string, indexPath string, expectedSkills []Skill) []ParityIssue {
	var issues []ParityIssue
	skillRoot := path.Clean(path.Join(spec.RootDir, spec.SkillDir))
	actualSkillCount := 0
	for filePath := range files {
		if indexPath != "" && filePath == indexPath {
			continue
		}
		if strings.HasPrefix(filePath, skillRoot+"/") {
			actualSkillCount++
		}
	}
	if actualSkillCount != len(expectedSkills) {
		issues = append(issues, ParityIssue{
			Format:  format,
			File:    skillRoot,
			Problem: fmt.Sprintf("skill file count = %d, want %d", actualSkillCount, len(expectedSkills)),
		})
	}

	indexContent := ""
	indexOK := false
	if indexPath != "" {
		indexContent, indexOK = files[indexPath]
	}
	for _, skill := range expectedSkills {
		skillPath := mustJoinRoot(spec.RootDir, skillFilePath(spec, skill))
		content, ok := files[skillPath]
		if !ok {
			issues = append(issues, ParityIssue{
				Format:  format,
				File:    skillPath,
				Problem: fmt.Sprintf("skill %q missing from rendered subtree", skill.Slug),
			})
			continue
		}
		for _, want := range []string{"# " + skill.Name, skill.Summary, skill.Body} {
			if !strings.Contains(content, want) {
				issues = append(issues, ParityIssue{
					Format:  format,
					File:    skillPath,
					Problem: fmt.Sprintf("skill %q content drifted from .tasks blueprint", skill.Slug),
				})
				break
			}
		}
		if indexOK {
			if !strings.Contains(indexContent, "- ["+skill.Name+"](") {
				issues = append(issues, ParityIssue{
					Format:  format,
					File:    indexPath,
					Problem: fmt.Sprintf("skill %q missing from index", skill.Slug),
				})
			}
			if !strings.Contains(indexContent, skill.Summary) {
				issues = append(issues, ParityIssue{
					Format:  format,
					File:    indexPath,
					Problem: fmt.Sprintf("skill %q summary missing from index", skill.Slug),
				})
			}
		}
	}
	return issues
}

func fileArtifactMap(files []FileArtifact) map[string]string {
	artifacts := make(map[string]string, len(files))
	for _, file := range files {
		artifacts[file.Path] = file.Content
	}
	return artifacts
}

// StableJSON returns a deterministic JSON encoding of the manifest.
func StableJSON(manifest Manifest) ([]byte, error) {
	clone := CanonicalizeManifest(manifest)
	payload := struct {
		Format   Format          `json:"format"`
		RootDir  string          `json:"root_dir"`
		Metadata []metadataEntry `json:"metadata"`
		Files    []FileArtifact  `json:"files"`
	}{
		Format:   clone.Format,
		RootDir:  clone.RootDir,
		Metadata: sortedMetadataEntries(clone.Metadata),
		Files:    clone.Files,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func validateTreePath(rootDir, treePath string) (string, error) {
	normalized, err := normalizeRelativePath(treePath)
	if err != nil {
		return "", err
	}
	if rootDir != "" && normalized != rootDir && !strings.HasPrefix(normalized, rootDir+"/") {
		return "", fmt.Errorf("path escapes root")
	}
	return normalized, nil
}

func joinRoot(rootDir, rel string) (string, error) {
	root, err := normalizeRelativePath(rootDir)
	if err != nil {
		return "", err
	}
	relPath, err := normalizeRelativePath(rel)
	if err != nil {
		return "", err
	}
	if root == "" {
		return relPath, nil
	}
	return path.Clean(path.Join(root, relPath)), nil
}

func normalizeRelativePath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.Contains(rel, "\\") {
		return "", fmt.Errorf("path must use forward slashes")
	}
	if path.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "/" {
		return "", fmt.Errorf("path escapes tree root")
	}
	return cleaned, nil
}

func validateMarkdownLinks(rootDir, sourcePath, content string) error {
	matches := markdownLinkPattern.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		target := strings.TrimSpace(match[1])
		if target == "" {
			return fmt.Errorf("empty link target")
		}
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
			continue
		}
		if strings.Contains(target, "://") {
			return fmt.Errorf("unsupported link target %q", target)
		}
		if strings.Contains(target, "\\") {
			return fmt.Errorf("link target must use forward slashes: %q", target)
		}
		joined, err := resolveLinkTarget(rootDir, sourcePath, target)
		if err != nil {
			return err
		}
		if joined == "" {
			return fmt.Errorf("link target resolved to empty path")
		}
	}
	return nil
}

func resolveLinkTarget(rootDir, sourcePath, target string) (string, error) {
	sourceDir := path.Dir(sourcePath)
	joined := path.Clean(path.Join(sourceDir, target))
	if strings.HasPrefix(joined, "../") || joined == ".." || strings.HasPrefix(joined, "/") {
		return "", fmt.Errorf("link target escapes root: %q", target)
	}
	if rootDir != "" && joined != rootDir && !strings.HasPrefix(joined, rootDir+"/") {
		return "", fmt.Errorf("link target escapes root: %q", target)
	}
	return joined, nil
}

func validateDocumentRoots(docPath string, roots []string) error {
	normalized, err := normalizeRelativePath(docPath)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		return nil
	}
	for _, root := range roots {
		if normalized == root || strings.HasPrefix(normalized, root+"/") {
			return nil
		}
	}
	return fmt.Errorf("document path %q is outside the allowed roots", docPath)
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

type metadataEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func sortedMetadataEntries(metadata map[string]string) []metadataEntry {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]metadataEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, metadataEntry{Key: key, Value: metadata[key]})
	}
	return entries
}
