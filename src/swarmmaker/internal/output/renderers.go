// renderers.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Platform-specific output renderers.
// Implements the Renderer interface for Claude, Codex, and Gemini output
// trees. Each renderer validates the blueprint, generates the entry file,
// skill index, and per-skill instruction files in the format-specific
// directory structure. Renderers are deterministic -- same blueprint in,
// same file tree out.


package output

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/op7ic/swarmmaker/internal/textutil"
)

type treeRenderer struct {
	spec TreeSpec
}

func newTreeRenderer(spec TreeSpec) Renderer {
	return treeRenderer{spec: spec}
}

func (r treeRenderer) Format() Format {
	return r.spec.Format
}

func (r treeRenderer) Spec() TreeSpec {
	return r.spec
}

func (r treeRenderer) Render(blueprint Blueprint) (Manifest, error) {
	if err := validateTreeSpec(r.spec); err != nil {
		return Manifest{}, err
	}
	if err := validateBlueprint(blueprint); err != nil {
		return Manifest{}, err
	}

	metadata := cloneMetadata(blueprint.Metadata)
	metadata["format"] = string(r.spec.Format)
	metadata["name"] = blueprint.Name
	metadata["purpose"] = blueprint.Purpose
	metadata["document_count"] = fmt.Sprintf("%d", len(blueprint.Docs))
	metadata["source_count"] = fmt.Sprintf("%d", len(blueprint.Docs))
	metadata["agent_count"] = fmt.Sprintf("%d", len(blueprint.Agents))
	metadata["skill_count"] = fmt.Sprintf("%d", len(blueprint.Skills))

	files := make([]FileArtifact, 0, len(r.spec.RequiredFiles)+len(blueprint.Skills))
	readmePath, err := joinRoot(r.spec.RootDir, r.spec.ReadmeFile)
	if err != nil {
		return Manifest{}, fmt.Errorf("join root for readme: %w", err)
	}
	entryPath, err := joinRoot(r.spec.RootDir, r.spec.EntryFile)
	if err != nil {
		return Manifest{}, fmt.Errorf("join root for entry: %w", err)
	}
	var skillIndexPath string
	if strings.TrimSpace(r.spec.SkillIndexFile) != "" {
		skillIndexPath, err = joinRoot(r.spec.RootDir, r.spec.SkillIndexFile)
		if err != nil {
			return Manifest{}, fmt.Errorf("join root for skill index: %w", err)
		}
	}

	files = append(files, FileArtifact{
		Path:    readmePath,
		Content: buildReadmeContent(r.spec, blueprint, metadata, skillIndexPath),
	})
	files = append(files, FileArtifact{
		Path:    entryPath,
		Content: buildEntryContent(r.spec, blueprint, metadata, entryPath, skillIndexPath),
	})
	if skillIndexPath != "" {
		files = append(files, FileArtifact{
			Path:    skillIndexPath,
			Content: buildSkillIndexContent(r.spec, blueprint, skillIndexPath, entryPath),
		})
	}
	for _, skill := range sortedSkills(blueprint.Skills) {
		sp, err := joinRoot(r.spec.RootDir, skillFilePath(r.spec, skill))
		if err != nil {
			return Manifest{}, fmt.Errorf("join root for skill %q: %w", skill.Slug, err)
		}
		files = append(files, FileArtifact{
			Path:    sp,
			Content: buildSkillContent(skill, r.spec.Format),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	return Manifest{
		Format:   r.spec.Format,
		RootDir:  r.spec.RootDir,
		Metadata: metadata,
		Files:    files,
	}, nil
}

func validateTreeSpec(spec TreeSpec) error {
	switch spec.Format {
	case FormatClaude, FormatCodex, FormatGemini:
	default:
		return fmt.Errorf("unsupported output format %q", spec.Format)
	}
	if strings.TrimSpace(spec.RootDir) == "" {
		return fmt.Errorf("tree spec root dir is required")
	}
	if !strings.HasPrefix(spec.RootDir, ".") {
		return fmt.Errorf("tree spec root dir must use a hidden platform root")
	}
	if strings.TrimSpace(spec.EntryFile) == "" {
		return fmt.Errorf("tree spec entry file is required")
	}
	if strings.TrimSpace(spec.SkillDir) == "" {
		return fmt.Errorf("tree spec skill dir is required")
	}
	if strings.TrimSpace(spec.ReadmeFile) == "" {
		return fmt.Errorf("tree spec readme file is required")
	}
	if strings.Contains(spec.SkillDir, "/") || strings.Contains(spec.SkillDir, "\\") {
		return fmt.Errorf("tree spec skill dir must be a single directory name")
	}
	if strings.TrimSpace(spec.SkillIndexFile) != "" {
		if _, err := normalizeRelativePath(spec.SkillIndexFile); err != nil {
			return fmt.Errorf("invalid skill index file %q: %w", spec.SkillIndexFile, err)
		}
	}
	return nil
}

func validateBlueprint(blueprint Blueprint) error {
	if strings.TrimSpace(blueprint.Name) == "" {
		return fmt.Errorf("blueprint name is required")
	}
	if strings.TrimSpace(blueprint.Purpose) == "" {
		return fmt.Errorf("blueprint purpose is required")
	}
	if len(blueprint.Metadata) == 0 {
		return fmt.Errorf("blueprint metadata is required")
	}
	for _, doc := range blueprint.Docs {
		if _, err := normalizeRelativePath(doc.Path); err != nil {
			return fmt.Errorf("invalid document path %q: %w", doc.Path, err)
		}
	}
	if len(blueprint.Agents) == 0 {
		return fmt.Errorf("blueprint agents are required")
	}
	seenAgents := make(map[string]struct{}, len(blueprint.Agents))
	for _, agent := range blueprint.Agents {
		if strings.TrimSpace(agent.Name) == "" {
			return fmt.Errorf("agent name is required")
		}
		if strings.TrimSpace(agent.Role) == "" {
			return fmt.Errorf("agent %q role is required", agent.Name)
		}
		if strings.TrimSpace(agent.Instructions) == "" {
			return fmt.Errorf("agent %q instructions are required", agent.Name)
		}
		slug := agentSlug(agent.Name)
		if slug == "" {
			return fmt.Errorf("agent %q slug is empty", agent.Name)
		}
		if _, ok := seenAgents[slug]; ok {
			return fmt.Errorf("duplicate agent name %q", agent.Name)
		}
		seenAgents[slug] = struct{}{}
	}
	if len(blueprint.Skills) == 0 {
		return fmt.Errorf("blueprint skills are required")
	}
	seenSkills := make(map[string]struct{}, len(blueprint.Skills))
	for _, skill := range blueprint.Skills {
		if strings.TrimSpace(skill.Name) == "" {
			return fmt.Errorf("skill name is required")
		}
		if strings.TrimSpace(skill.Slug) == "" {
			return fmt.Errorf("skill %q slug is required", skill.Name)
		}
		if strings.TrimSpace(skill.Summary) == "" {
			return fmt.Errorf("skill %q summary is required", skill.Name)
		}
		if strings.TrimSpace(skill.Body) == "" {
			return fmt.Errorf("skill %q body is required", skill.Name)
		}
		slug := skillSlug(skill.Slug)
		if slug == "" {
			return fmt.Errorf("skill %q slug is empty", skill.Name)
		}
		if _, ok := seenSkills[slug]; ok {
			return fmt.Errorf("duplicate skill slug %q", skill.Slug)
		}
		seenSkills[slug] = struct{}{}
	}
	return nil
}

func buildReadmeContent(spec TreeSpec, blueprint Blueprint, metadata map[string]string, skillIndexPath string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(blueprint.Name)
	b.WriteString("\n\n")
	b.WriteString(blueprint.Purpose)
	b.WriteString("\n\n")
	b.WriteString("## Selected Platform\n\n")
	b.WriteString("- Format: ")
	b.WriteString(string(spec.Format))
	b.WriteString("\n")
	b.WriteString("- Root: `")
	b.WriteString(spec.RootDir)
	b.WriteString("`\n")
	b.WriteString("- Skill subtree: `")
	b.WriteString(path.Join(spec.RootDir, spec.SkillDir))
	b.WriteString("`\n")
	b.WriteString("- Router: `")
	b.WriteString(spec.EntryFile)
	b.WriteString("`\n")
	b.WriteString("- Readme: `")
	b.WriteString(spec.ReadmeFile)
	b.WriteString("`\n")
	if strings.TrimSpace(blueprint.UsageHint) != "" {
		b.WriteString("- Invocation hint: `")
		b.WriteString(blueprint.UsageHint)
		b.WriteString("`\n")
	}
	b.WriteString("\n## Skill Index\n\n")
	if skillIndexPath != "" {
		b.WriteString("- [Skill index](")
		b.WriteString(relativeLink(mustJoinRoot(spec.RootDir, spec.ReadmeFile), skillIndexPath))
		b.WriteString(")\n")
	}
	b.WriteString("\n## Source Materials\n\n")
	writeSourceList(&b, blueprint.Docs)
	b.WriteString("\n## Coordination Roles\n\n")
	writeAgentList(&b, blueprint.Agents)
	b.WriteString("\n## Metadata\n\n")
	b.WriteString("format: ")
	b.WriteString(metadata["format"])
	b.WriteString("\n")
	b.WriteString("skill_count: ")
	b.WriteString(metadata["skill_count"])
	b.WriteString("\n")
	return b.String()
}

func buildEntryContent(spec TreeSpec, blueprint Blueprint, metadata map[string]string, entryPath, skillIndexPath string) string {
	var b strings.Builder
	switch spec.Format {
	case FormatClaude:
		b.WriteString("# ")
		b.WriteString(blueprint.Name)
		b.WriteString("\n\n")
		b.WriteString("Claude skill routing for the selected swarm.\n\n")
	case FormatCodex:
		b.WriteString("# Codex Router\n\n")
		b.WriteString("OpenAI Codex instructions for the selected swarm.\n\n")
	case FormatGemini:
		b.WriteString("# Gemini Router\n\n")
		b.WriteString("Gemini playbooks for the selected swarm.\n\n")
	}
	b.WriteString("## Entry Points\n\n")
	b.WriteString("- [Readme](./")
	b.WriteString(spec.ReadmeFile)
	b.WriteString(")\n")
	if skillIndexPath != "" {
		b.WriteString("- [Skill Index](./")
		b.WriteString(strings.TrimPrefix(skillIndexPath, spec.RootDir+"/"))
		b.WriteString(")\n")
	}
	if strings.TrimSpace(blueprint.UsageHint) != "" {
		b.WriteString("- Invocation: `")
		b.WriteString(blueprint.UsageHint)
		b.WriteString("`\n")
	}
	b.WriteString("\n## Coordination Roles\n\n")
	writeAgentList(&b, blueprint.Agents)
	b.WriteString("\n## Metadata\n\n")
	b.WriteString("format: ")
	b.WriteString(metadata["format"])
	b.WriteString("\n")
	return b.String()
}

func buildSkillIndexContent(spec TreeSpec, blueprint Blueprint, indexPath, entryPath string) string {
	var b strings.Builder
	switch spec.Format {
	case FormatClaude:
		b.WriteString("# Skills\n\n")
	case FormatCodex:
		b.WriteString("# Instructions\n\n")
	case FormatGemini:
		b.WriteString("# Playbooks\n\n")
	}
	b.WriteString("- [Readme](")
	b.WriteString(relativeLink(indexPath, mustJoinRoot(spec.RootDir, spec.ReadmeFile)))
	b.WriteString(")\n")
	b.WriteString("- [Entry](")
	b.WriteString(relativeLink(indexPath, entryPath))
	b.WriteString(")\n")
	for _, skill := range sortedSkills(blueprint.Skills) {
		agentPath := mustJoinRoot(spec.RootDir, skillFilePath(spec, skill))
		b.WriteString("- [")
		b.WriteString(skill.Name)
		b.WriteString("](")
		b.WriteString(relativeLink(indexPath, agentPath))
		b.WriteString(") - ")
		b.WriteString(skill.Summary)
		b.WriteString("\n")
	}
	b.WriteString("\n## Source Materials\n\n")
	writeSourceList(&b, blueprint.Docs)
	return b.String()
}

// BuildSkillContent renders a skill file with YAML frontmatter. Exported for
// use by the cross-platform .agents/skills/ emitter in the CLI package.
func BuildSkillContent(skill Skill) string {
	return buildSkillContent(skill, "")
}

func buildSkillContent(skill Skill, _ Format) string {
	var b strings.Builder
	b.WriteString("---\nname: ")
	b.WriteString(skillSlug(skill.Slug))
	b.WriteString("\ndescription: >-\n  ")
	b.WriteString(buildPushyDescription(skill))
	b.WriteString("\n---\n")
	b.WriteString("# ")
	b.WriteString(skill.Name)
	b.WriteString("\n\n")
	b.WriteString(skill.Summary)
	b.WriteString("\n\n")
	b.WriteString("## Instructions\n\n")
	b.WriteString(skill.Body)
	if !strings.HasSuffix(skill.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// buildPushyDescription builds a frontmatter description that starts with an
// action verb from the summary and appends "Use when" triggers extracted from
// the "When to Invoke" section. The result is kept under 200 characters.
func buildPushyDescription(skill Skill) string {
	summary := strings.TrimSpace(skill.Summary)
	summary = strings.TrimRight(summary, ".!?;:")

	triggers := extractWhenToInvokeTriggers(skill.Body)
	if len(triggers) == 0 {
		desc := summary + "."
		if len(desc) > 200 {
			desc = desc[:197] + "..."
		}
		return desc
	}

	useWhen := "Use when " + strings.Join(triggers, ", ") + "."
	desc := summary + ". " + useWhen
	if len(desc) <= 200 {
		return desc
	}
	// Try with fewer triggers.
	for n := len(triggers) - 1; n >= 1; n-- {
		useWhen = "Use when " + strings.Join(triggers[:n], ", ") + "."
		desc = summary + ". " + useWhen
		if len(desc) <= 200 {
			return desc
		}
	}
	// Fall back to summary only.
	desc = summary + "."
	if len(desc) > 200 {
		desc = desc[:197] + "..."
	}
	return desc
}

// extractWhenToInvokeTriggers parses bullet items from a "When to Invoke"
// section in the skill body, returning up to 3 trigger conditions.
func extractWhenToInvokeTriggers(body string) []string {
	lines := strings.Split(body, "\n")
	inSection := false
	var triggers []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "## When to Invoke") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if inSection && strings.HasPrefix(trimmed, "- ") {
			trigger := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			trigger = strings.TrimRight(trigger, ".!?;:")
			trigger = strings.ToLower(trigger[:1]) + trigger[1:]
			if trigger != "" {
				triggers = append(triggers, trigger)
			}
			if len(triggers) >= 3 {
				break
			}
		}
	}
	return triggers
}

func sortedDocs(docs []Document) []Document {
	out := append([]Document(nil), docs...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func sortedAgents(agents []Agent) []Agent {
	out := append([]Agent(nil), agents...)
	sort.Slice(out, func(i, j int) bool {
		return agentSlug(out[i].Name) < agentSlug(out[j].Name)
	})
	return out
}

func sortedSkills(skills []Skill) []Skill {
	out := append([]Skill(nil), skills...)
	sort.Slice(out, func(i, j int) bool {
		return skillSlug(out[i].Slug) < skillSlug(out[j].Slug)
	})
	return out
}

func writeSourceList(b *strings.Builder, docs []Document) {
	if len(docs) == 0 {
		b.WriteString("- No source documents provided.\n")
		return
	}
	for _, doc := range sortedDocs(docs) {
		b.WriteString("- `")
		b.WriteString(doc.Path)
		b.WriteString("`\n")
	}
}

func writeAgentList(b *strings.Builder, agents []Agent) {
	if len(agents) == 0 {
		b.WriteString("- No coordination roles provided.\n")
		return
	}
	for _, agent := range sortedAgents(agents) {
		b.WriteString("- ")
		b.WriteString(agent.Name)
		b.WriteString(": ")
		b.WriteString(agent.Role)
		b.WriteString("\n")
	}
}

func skillFilePath(spec TreeSpec, skill Skill) string {
	slug := skillSlug(skill.Slug)
	switch spec.Format {
	case FormatClaude:
		return path.Join(spec.SkillDir, slug, "SKILL.md")
	case FormatCodex, FormatGemini:
		return path.Join(spec.SkillDir, slug+".md")
	default:
		return ""
	}
}

func skillSlug(slug string) string {
	return textutil.Slugify(slug)
}

func agentSlug(name string) string {
	return textutil.Slugify(name)
}

func relativeLink(fromPath, toPath string) string {
	rel, err := filepath.Rel(path.Dir(fromPath), toPath)
	if err != nil {
		return toPath
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return "./"
	}
	if !strings.HasPrefix(rel, ".") {
		return "./" + rel
	}
	return rel
}

// mustJoinRoot joins rootDir and rel, returning an empty string on error.
// Callers in content-building helpers rely on paths that were already validated
// by the Render method, so errors here indicate a programming bug rather than
// user input problems. The empty-string fallback produces visibly broken output
// instead of crashing the process.
func mustJoinRoot(rootDir, rel string) string {
	joined, err := joinRoot(rootDir, rel)
	if err != nil {
		return ""
	}
	return joined
}
