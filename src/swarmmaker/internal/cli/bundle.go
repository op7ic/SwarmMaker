// bundle.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Output bundle assembly and rendering.
// Parses the .tasks/ ledger (skills.md, agents.md) into structured blueprints,
// renders platform-specific output trees (.claude/, .codex/, .gemini/),
// writes README.md and install.sh, and implements atomic staged writes so
// partial output never survives a mid-write failure. Also handles cross-target
// parity validation when multiple output formats are selected.


package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/op7ic/swarmmaker/internal/output"
	"github.com/op7ic/swarmmaker/internal/swarm"
	"github.com/op7ic/swarmmaker/internal/textutil"
)

var ledgerFiles = []string{
	".tasks/context.md",
	".tasks/tasks.md",
	".tasks/prompts/product.md",
	".tasks/prompts/technical.md",
	".tasks/prompts/tools.md",
	".tasks/prompts/deployment.md",
	".tasks/todo.md",
	".tasks/skills.md",
	".tasks/agents.md",
}

var criticalLedgerFiles = []string{
	".tasks/tasks.md",
	".tasks/prompts/product.md",
	".tasks/prompts/technical.md",
	".tasks/prompts/tools.md",
	".tasks/prompts/deployment.md",
	".tasks/todo.md",
	".tasks/skills.md",
	".tasks/agents.md",
}

var ledgerMinLengths = map[string]int64{
	".tasks/context.md":            200,
	".tasks/tasks.md":              220,
	".tasks/prompts/product.md":    200,
	".tasks/prompts/technical.md":  200,
	".tasks/prompts/tools.md":      160,
	".tasks/prompts/deployment.md": 160,
	".tasks/todo.md":               180,
	".tasks/skills.md":             220,
	".tasks/agents.md":             220,
}

var skillSlugLine = regexp.MustCompile(`(?mi)^(?:-\s*)?Slug:\s*(.+?)\s*$`)
var skillSummaryLine = regexp.MustCompile(`(?mi)^(?:-\s*)?Summary:\s*(.+?)\s*$`)
var agentRoleLine = regexp.MustCompile(`(?mi)^(?:-\s*)?Role:\s*(.+?)\s*$`)

type namedBlock struct {
	Name string
	Body string
}

type renderedOutputBundle struct {
	Blueprint output.Blueprint
	Manifests []output.Manifest
}

func emitOutputSwarms(outputDir, rootDir string, formats []output.Format, projectName, primaryName, criticName string, files []string, customSpecs []string) error {
	bundle, err := renderOutputSwarms(rootDir, formats, projectName, primaryName, criticName, files)
	if err != nil {
		return err
	}
	if parityIssues := validateRenderedOutputParity(bundle); len(parityIssues) > 0 {
		return fmt.Errorf("render parity validation failed with %d error(s)", swarm.ErrorCount(parityIssues))
	}
	return writeRenderedOutputSwarms(outputDir, formats, projectName, primaryName, criticName, bundle, customSpecs)
}

func renderOutputSwarms(rootDir string, formats []output.Format, projectName, primaryName, criticName string, files []string) (renderedOutputBundle, error) {
	if len(formats) == 0 {
		return renderedOutputBundle{}, fmt.Errorf("at least one output format is required")
	}
	formats = canonicalOutputFormats(formats)
	blueprint, err := buildOutputBlueprint(rootDir, projectName, primaryName, criticName, files)
	if err != nil {
		return renderedOutputBundle{}, err
	}
	registry, err := output.NewRegistry()
	if err != nil {
		return renderedOutputBundle{}, err
	}
	manifests := make([]output.Manifest, 0, len(formats))
	for _, format := range formats {
		manifest, err := registry.Render(format, blueprint)
		if err != nil {
			return renderedOutputBundle{}, err
		}
		manifests = append(manifests, manifest)
	}
	return renderedOutputBundle{
		Blueprint: blueprint,
		Manifests: manifests,
	}, nil
}

func writeRenderedOutputSwarms(outputDir string, formats []output.Format, projectName, primaryName, criticName string, bundle renderedOutputBundle, customSpecs []string) error {
	// Stage all files in a temp dir in the same parent as outputDir so that
	// os.Rename works (same mount point). On any failure, clean up staging.
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output dir: %w", err)
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(absOutput), ".staging-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(stagingDir)
		}
	}()

	// Write all manifest artifacts to staging.
	cleanStaging := filepath.Clean(stagingDir)
	for _, manifest := range bundle.Manifests {
		for _, artifact := range manifest.Files {
			path := filepath.Join(stagingDir, filepath.FromSlash(artifact.Path))
			// Guard against path traversal: resolved path must stay under staging dir.
			cleanPath := filepath.Clean(path)
			if !strings.HasPrefix(cleanPath, cleanStaging+string(filepath.Separator)) && cleanPath != cleanStaging {
				return fmt.Errorf("artifact path %q escapes staging directory", artifact.Path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return fmt.Errorf("create output swarm dir %s: %w", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(artifact.Content), 0644); err != nil {
				return fmt.Errorf("write output swarm file %s: %w", path, err)
			}
		}
	}
	// Render custom platform outputs.
	for _, specPath := range customSpecs {
		spec, loadErr := output.LoadCustomSpec(specPath)
		if loadErr != nil {
			return fmt.Errorf("load custom spec: %w", loadErr)
		}
		customFiles, renderErr := output.RenderCustom(spec, bundle.Blueprint.Skills)
		if renderErr != nil {
			return fmt.Errorf("render custom platform %q: %w", spec.Platform, renderErr)
		}
		for _, artifact := range customFiles {
			path := filepath.Join(stagingDir, filepath.FromSlash(artifact.Path))
			cleanPath := filepath.Clean(path)
			if !strings.HasPrefix(cleanPath, cleanStaging+string(filepath.Separator)) && cleanPath != cleanStaging {
				return fmt.Errorf("custom artifact path %q escapes staging directory", artifact.Path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return fmt.Errorf("create custom output dir: %w", err)
			}
			if err := os.WriteFile(path, []byte(artifact.Content), 0644); err != nil {
				return fmt.Errorf("write custom output file %s: %w", path, err)
			}
		}
	}

	// Emit cross-platform .agents/skills/<slug>/SKILL.md for each skill,
	// with progressive disclosure: large skills get a references/ subdirectory.
	for _, skill := range bundle.Blueprint.Skills {
		slug := textutil.Slugify(skill.Slug)
		agentsSkillPath := filepath.Join(stagingDir, ".agents", "skills", slug, "SKILL.md")
		cleanAgentsPath := filepath.Clean(agentsSkillPath)
		if !strings.HasPrefix(cleanAgentsPath, cleanStaging+string(filepath.Separator)) && cleanAgentsPath != cleanStaging {
			return fmt.Errorf("agents skill path %q escapes staging directory", agentsSkillPath)
		}
		if err := os.MkdirAll(filepath.Dir(agentsSkillPath), 0755); err != nil {
			return fmt.Errorf("create .agents/skills dir for %s: %w", skill.Slug, err)
		}
		split := output.BuildSkillSplit(skill)
		if err := os.WriteFile(agentsSkillPath, []byte(split.Main), 0644); err != nil {
			return fmt.Errorf("write .agents/skills/%s/SKILL.md: %w", skill.Slug, err)
		}
		// Emit MCP tool definition alongside SKILL.md
		mcpJSON, mcpErr := output.BuildMCPToolJSON(skill)
		if mcpErr != nil {
			return fmt.Errorf("build MCP tool for %s: %w", skill.Slug, mcpErr)
		}
		mcpPath := filepath.Join(stagingDir, ".agents", "skills", slug, "mcp_tool.json")
		if err := os.WriteFile(mcpPath, mcpJSON, 0644); err != nil {
			return fmt.Errorf("write mcp_tool.json for %s: %w", skill.Slug, err)
		}
		if split.References != "" {
			refPath := filepath.Join(stagingDir, ".agents", "skills", slug, split.RefPath)
			cleanRefPath := filepath.Clean(refPath)
			if !strings.HasPrefix(cleanRefPath, cleanStaging+string(filepath.Separator)) && cleanRefPath != cleanStaging {
				return fmt.Errorf("agents skill ref path %q escapes staging directory", refPath)
			}
			if err := os.MkdirAll(filepath.Dir(refPath), 0755); err != nil {
				return fmt.Errorf("create references dir for %s: %w", skill.Slug, err)
			}
			if err := os.WriteFile(refPath, []byte(split.References), 0644); err != nil {
				return fmt.Errorf("write %s: %w", split.RefPath, err)
			}
		}
	}

	if err := writeRootREADME(stagingDir, formats, projectName, primaryName, criticName, bundle.Blueprint); err != nil {
		return err
	}
	if err := writeReviewChecklist(stagingDir); err != nil {
		return err
	}
	if err := writeInstallScript(stagingDir, formats); err != nil {
		return err
	}
	if err := writeOutputGitignore(stagingDir); err != nil {
		return err
	}

	// All writes succeeded. Move staged files into the final output dir.
	if err := os.MkdirAll(absOutput, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := moveStagedFiles(stagingDir, absOutput); err != nil {
		return fmt.Errorf("move staged files: %w", err)
	}
	success = true
	os.RemoveAll(stagingDir) // clean up empty staging dir
	return nil
}

// moveStagedFiles moves all files from src into dst, preserving directory structure.
func moveStagedFiles(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		// Ensure parent dir exists in destination.
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.Rename(path, target)
	})
}

func validateRenderedOutputParity(bundle renderedOutputBundle) []swarm.Issue {
	parityIssues := output.ValidateManifestParity(bundle.Blueprint, bundle.Manifests)
	issues := make([]swarm.Issue, 0, len(parityIssues))
	for _, issue := range parityIssues {
		file := issue.File
		if strings.TrimSpace(file) == "" {
			file = string(issue.Format)
		}
		issues = append(issues, swarm.Issue{
			File:     file,
			Problem:  issue.Problem,
			Severity: "error",
		})
	}
	return issues
}

func writeRootREADME(outputDir string, formats []output.Format, projectName, primaryName, criticName string, blueprint output.Blueprint) error {
	rootDirs, err := platformRootDirs(formats)
	if err != nil {
		return err
	}
	formats = canonicalOutputFormats(formats)
	usageHint := strings.TrimSpace(blueprint.UsageHint)
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(projectName)
	b.WriteString("\n\n")
	b.WriteString("SwarmMaker generated this SKILL bundle from loose documentation.\n\n")
	b.WriteString("## Output\n\n")
	b.WriteString("- Central ledger: `.tasks/`\n")
	b.WriteString("- Selected platform subtrees: ")
	for i, rootDir := range rootDirs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("`")
		b.WriteString(rootDir)
		b.WriteString("/`")
	}
	b.WriteString("\n")
	b.WriteString("- Installer: `install.sh`\n")
	b.WriteString("- Bundle readme: this file\n\n")
	b.WriteString("## Routing\n\n")
	b.WriteString("- Generator: ")
	b.WriteString(primaryName)
	b.WriteString("\n")
	b.WriteString("- Critic: ")
	b.WriteString(criticName)
	b.WriteString("\n")
	b.WriteString("- Output formats: ")
	for i, format := range formats {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(string(format))
	}
	b.WriteString("\n\n")
	b.WriteString("## Usage\n\n")
	if usageHint == "" {
		usageHint = fmt.Sprintf("Use the generated %s skills for the requested workflow.", projectName)
	}
	b.WriteString("- Ask the target assistant to use the generated skills.\n")
	b.WriteString("- Example intent: `")
	b.WriteString(usageHint)
	b.WriteString("`\n")
	b.WriteString("- Install into the current project: `./install.sh`\n")
	b.WriteString("- Install into another project: `./install.sh --target /path/to/project`\n")
	b.WriteString("- Install into an explicit shared location: `./install.sh --global /path/to/shared/skills`\n\n")
	b.WriteString("## Important\n\n")
	b.WriteString("These skills were generated from source documentation and validated against\n")
	b.WriteString("the source material. Review REVIEW_CHECKLIST.md before deploying to production.\n")
	path := filepath.Join(outputDir, "README.md")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeReviewChecklist(outputDir string) error {
	content := `# Review Checklist

This skill bundle was auto-generated by SwarmMaker. Research (ETH Zurich, 2025) shows that LLM-generated agent instruction files can reduce task success if deployed without human review. Use this checklist before deploying.

## Frontmatter Review
- [ ] Each skill's ` + "`name`" + ` field matches its directory name exactly
- [ ] Each skill's ` + "`description`" + ` is action-oriented and contains trigger conditions
- [ ] Descriptions front-load the most specific keywords for your domain

## Content Review
- [ ] No skill body exceeds 500 lines (split to references/ if needed)
- [ ] Process steps reference specific values from YOUR system, not generic examples
- [ ] UNKNOWN gates list genuine blockers, not filler
- [ ] Constraints (MUST DO / MUST NOT) are derived from real requirements

## Behavioral Validation
- [ ] Test each skill triggers on substantive multi-step requests (not trivially phrased)
- [ ] Test each skill does NOT trigger on out-of-scope requests
- [ ] Verify agent handoff data shapes match actual system data structures
- [ ] Restart the agent after any skill edit (skills are session-snapshotted)

## Anti-Patterns to Fix
- [ ] No first-person voice ("I will..." -> "The agent will..." or imperative)
- [ ] No ALL-CAPS MUST/NEVER without reasoning (reframe as guidance with rationale)
- [ ] No generic descriptions that could match unrelated tasks
- [ ] No invented tool names, CLI commands, or API endpoints not in source material
`
	path := filepath.Join(outputDir, "REVIEW_CHECKLIST.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeInstallScript(outputDir string, formats []output.Format) error {
	rootDirs, err := platformRootDirs(formats)
	if err != nil {
		return err
	}
	formats = canonicalOutputFormats(formats)
	var removeLine strings.Builder
	var copyLines strings.Builder
	removeLine.WriteString("rm -rf \"$TARGET_DIR/.tasks\" \"$TARGET_DIR/.agents\"")
	for _, rootDir := range rootDirs {
		removeLine.WriteString(" \"$TARGET_DIR/")
		removeLine.WriteString(rootDir)
		removeLine.WriteString("\"")
		copyLines.WriteString("cp -R \"$ROOT_DIR/")
		copyLines.WriteString(rootDir)
		copyLines.WriteString("\" \"$TARGET_DIR/\"\n")
	}
	copyLines.WriteString("cp -R \"$ROOT_DIR/.agents\" \"$TARGET_DIR/\"\n")
	var formatNames strings.Builder
	for i, format := range formats {
		if i > 0 {
			formatNames.WriteString(",")
		}
		formatNames.WriteString(string(format))
	}
	content := fmt.Sprintf(`#!/usr/bin/env sh
set -eu

MODE="project"
TARGET_DIR="."

while [ "$#" -gt 0 ]; do
  case "$1" in
    --target)
      [ "$#" -ge 2 ] || { echo "missing path after --target" >&2; exit 2; }
      MODE="project"
      TARGET_DIR="$2"
      shift 2
      ;;
    --global)
      [ "$#" -ge 2 ] || { echo "missing path after --global" >&2; exit 2; }
      MODE="global"
      TARGET_DIR="$2"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
mkdir -p "$TARGET_DIR"
%s
cp -R "$ROOT_DIR/.tasks" "$TARGET_DIR/"
%s
cp "$ROOT_DIR/README.md" "$TARGET_DIR/"
cp "$ROOT_DIR/REVIEW_CHECKLIST.md" "$TARGET_DIR/"

echo "Installed SwarmMaker bundle into $TARGET_DIR (mode=$MODE formats=%s)"
`, removeLine.String(), copyLines.String(), formatNames.String())
	path := filepath.Join(outputDir, "install.sh")
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeOutputGitignore(outputDir string) error {
	content := `# SwarmMaker debug artifacts (not needed for skill execution)
.tasks/ir/
.tasks/evidence.json
.tasks/manifest.json
.tasks/validation-report.md
`
	path := filepath.Join(outputDir, ".gitignore")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func buildOutputBlueprint(rootDir, projectName, primaryName, criticName string, files []string) (output.Blueprint, error) {
	docs := make([]output.Document, 0, len(files))
	contents := make(map[string]string, len(files))
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(rootDir, rel))
		if err != nil {
			return output.Blueprint{}, fmt.Errorf("read generated task doc %s: %w", rel, err)
		}
		text := string(content)
		contents[rel] = text
		docs = append(docs, output.Document{
			Path:    filepath.ToSlash(rel),
			Content: text,
		})
	}
	skills, err := parseSkillsDocument(".tasks/skills.md", contents[".tasks/skills.md"])
	if err != nil {
		return output.Blueprint{}, err
	}
	agents, err := parseAgentsDocument(".tasks/agents.md", contents[".tasks/agents.md"])
	if err != nil {
		return output.Blueprint{}, err
	}
	usageHint := fmt.Sprintf("Use the generated %s skills for the requested workflow.", projectName)
	if len(skills) > 0 {
		summary := strings.TrimSpace(skills[0].Summary)
		summary = strings.TrimRight(summary, ".!?;:")
		if summary != "" {
			usageHint = fmt.Sprintf("Use the %s skill when you need to %s.", skills[0].Slug, strings.ToLower(summary))
		}
	}
	return output.Blueprint{
		Name:      projectName,
		Purpose:   "Installable skill bundle generated from the .tasks ledger.",
		UsageHint: usageHint,
		Metadata: map[string]string{
			"generator": primaryName,
			"critic":    criticName,
		},
		Docs:   docs,
		Agents: agents,
		Skills: skills,
	}, nil
}

func parseSkillsDocument(path, content string) ([]output.Skill, error) {
	blocks, err := parseNamedBlocks(content, []string{"## Skill:", "### Skill:"})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	skills := make([]output.Skill, 0, len(blocks))
	for _, block := range blocks {
		slug, err := extractRequiredField(block.Body, skillSlugLine, "Slug")
		if err != nil {
			return nil, fmt.Errorf("%s skill %q: %w", path, block.Name, err)
		}
		summary, err := extractRequiredField(block.Body, skillSummaryLine, "Summary")
		if err != nil {
			return nil, fmt.Errorf("%s skill %q: %w", path, block.Name, err)
		}
		body := stripMetadataLines(block.Body, skillSlugLine, skillSummaryLine)
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("%s skill %q: instructions body is required", path, block.Name)
		}
		skills = append(skills, output.Skill{
			Name:    block.Name,
			Slug:    bundleSlug(slug),
			Summary: summary,
			Body:    strings.TrimSpace(body),
		})
	}
	return skills, nil
}

func parseAgentsDocument(path, content string) ([]output.Agent, error) {
	blocks, err := parseNamedBlocks(content, []string{"## Agent:", "### Agent:"})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	agents := make([]output.Agent, 0, len(blocks))
	for _, block := range blocks {
		role, err := extractRequiredField(block.Body, agentRoleLine, "Role")
		if err != nil {
			return nil, fmt.Errorf("%s agent %q: %w", path, block.Name, err)
		}
		body := stripMetadataLines(block.Body, agentRoleLine)
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("%s agent %q: instructions body is required", path, block.Name)
		}
		agents = append(agents, output.Agent{
			Name:         block.Name,
			Role:         role,
			Instructions: strings.TrimSpace(body),
		})
	}
	return agents, nil
}

func parseNamedBlocks(content string, prefixes []string) ([]namedBlock, error) {
	lines := strings.Split(content, "\n")
	var blocks []namedBlock
	currentName := ""
	currentBody := make([]string, 0, 16)
	flush := func() error {
		if currentName == "" {
			return nil
		}
		body := strings.TrimSpace(strings.Join(currentBody, "\n"))
		if body == "" {
			return fmt.Errorf("block %q has no body", currentName)
		}
		blocks = append(blocks, namedBlock{Name: currentName, Body: body})
		currentName = ""
		currentBody = currentBody[:0]
		return nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		matchedPrefix := ""
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				matchedPrefix = prefix
				break
			}
		}
		if matchedPrefix != "" {
			if err := flush(); err != nil {
				return nil, err
			}
			name := strings.TrimSpace(trimmed[len(matchedPrefix):])
			if name == "" {
				return nil, fmt.Errorf("block heading %q is missing a name", matchedPrefix)
			}
			currentName = name
			continue
		}
		if currentName != "" {
			currentBody = append(currentBody, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("expected at least one named block using %v", prefixes)
	}
	return blocks, nil
}

func extractRequiredField(body string, re *regexp.Regexp, label string) (string, error) {
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("%s field is required", label)
	}
	value := strings.TrimSpace(matches[1])
	if value == "" {
		return "", fmt.Errorf("%s field is empty", label)
	}
	return value, nil
}

func stripMetadataLines(body string, matchers ...*regexp.Regexp) string {
	lines := strings.Split(body, "\n")
	filtered := make([]string, 0, len(lines))
lineLoop:
	for _, line := range lines {
		for _, matcher := range matchers {
			if matcher.MatchString(line) {
				continue lineLoop
			}
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func bundleSlug(value string) string {
	slug := textutil.Slugify(value)
	if slug == "" {
		return "unknown"
	}
	return slug
}

func parseOutputFormats(formatName string) ([]output.Format, []string, error) {
	normalized := strings.ToLower(strings.TrimSpace(formatName))
	if normalized == "" {
		return nil, nil, fmt.Errorf("output swarm is required")
	}
	names := []string{normalized}
	if normalized == "all" {
		names = []string{"claude", "codex", "gemini"}
	} else if strings.Contains(normalized, ",") {
		names = strings.Split(normalized, ",")
	}
	seen := make(map[output.Format]struct{}, len(names))
	formats := make([]output.Format, 0, len(names))
	var customSpecs []string
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		switch {
		case trimmed == string(output.FormatClaude):
			if _, ok := seen[output.FormatClaude]; !ok {
				formats = append(formats, output.FormatClaude)
				seen[output.FormatClaude] = struct{}{}
			}
		case trimmed == string(output.FormatCodex):
			if _, ok := seen[output.FormatCodex]; !ok {
				formats = append(formats, output.FormatCodex)
				seen[output.FormatCodex] = struct{}{}
			}
		case trimmed == string(output.FormatGemini):
			if _, ok := seen[output.FormatGemini]; !ok {
				formats = append(formats, output.FormatGemini)
				seen[output.FormatGemini] = struct{}{}
			}
		case strings.HasPrefix(trimmed, "custom:"):
			specPath := strings.TrimPrefix(trimmed, "custom:")
			if strings.TrimSpace(specPath) == "" {
				return nil, nil, fmt.Errorf("custom format requires a spec path: custom:/path/to/spec.yaml")
			}
			customSpecs = append(customSpecs, specPath)
		default:
			return nil, nil, fmt.Errorf("unsupported output swarm %q", formatName)
		}
	}
	return canonicalOutputFormats(formats), customSpecs, nil
}

func platformRootDir(format output.Format) (string, error) {
	spec, ok := output.DefaultSpecs()[format]
	if !ok {
		return "", fmt.Errorf("unsupported output swarm %q", format)
	}
	return spec.RootDir, nil
}

func platformRootDirs(formats []output.Format) ([]string, error) {
	rootDirs := make([]string, 0, len(formats))
	for _, format := range canonicalOutputFormats(formats) {
		rootDir, err := platformRootDir(format)
		if err != nil {
			return nil, err
		}
		rootDirs = append(rootDirs, rootDir)
	}
	return rootDirs, nil
}

func prepareOutputTree(outputDir string, formats []output.Format, force bool) error {
	rootDirs, err := platformRootDirs(formats)
	if err != nil {
		return err
	}
	requiredPaths := []string{
		filepath.Join(outputDir, ".swarmmaker"),
		filepath.Join(outputDir, ".tasks"),
		filepath.Join(outputDir, ".agents"),
		filepath.Join(outputDir, ".claude"),
		filepath.Join(outputDir, ".codex"),
		filepath.Join(outputDir, ".gemini"),
		filepath.Join(outputDir, "claude"),
		filepath.Join(outputDir, "codex"),
		filepath.Join(outputDir, "gemini"),
		filepath.Join(outputDir, "README.md"),
		filepath.Join(outputDir, "REVIEW_CHECKLIST.md"),
		filepath.Join(outputDir, "install.sh"),
		filepath.Join(outputDir, ".gitignore"),
		filepath.Join(outputDir, "evidence.json"),
		filepath.Join(outputDir, "evidence-manifest.json"),
		filepath.Join(outputDir, "validation-report.md"),
	}
	for _, rootDir := range rootDirs {
		requiredPaths = append(requiredPaths, filepath.Join(outputDir, rootDir))
	}
	seen := make(map[string]struct{}, len(requiredPaths))
	for _, path := range requiredPaths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if _, err := os.Stat(path); err == nil {
			if !force {
				return fmt.Errorf("output artifact already exists at %s\n  Use --force to overwrite, or -o to write to a different directory", path)
			}
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("removing stale output artifact %q: %w", path, err)
			}
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat output artifact %q: %w", path, err)
		}
	}
	return nil
}

// ReplaceSkill replaces a single skill file in an existing bundle directory.
// It reads the existing skills, replaces the matching slug, and writes the updated SKILL.md.
func ReplaceSkill(bundleDir, slug, newSkillContent string) error {
	slug = textutil.Slugify(slug)
	if slug == "" {
		return fmt.Errorf("skill slug is required")
	}
	skillDir := filepath.Join(bundleDir, ".agents", "skills", slug)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill directory %q does not exist; cannot replace a skill that was not previously generated", skillDir)
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create skill directory: %w", err)
	}
	if err := os.WriteFile(skillPath, []byte(newSkillContent), 0644); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}
	return nil
}

func canonicalOutputFormats(formats []output.Format) []output.Format {
	order := []output.Format{output.FormatClaude, output.FormatCodex, output.FormatGemini}
	seen := make(map[output.Format]struct{}, len(formats))
	for _, format := range formats {
		seen[format] = struct{}{}
	}
	result := make([]output.Format, 0, len(seen))
	for _, format := range order {
		if _, ok := seen[format]; ok {
			result = append(result, format)
		}
	}
	return result
}
