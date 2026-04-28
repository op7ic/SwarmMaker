package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	content := `platform: my-agent-framework
skill_path: "plugins/{slug}/prompt.md"
frontmatter: true
frontmatter_fields: [name, description, version]
sections: [summary, process, constraints]
`
	if err := os.WriteFile(specPath, []byte(content), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	spec, err := LoadCustomSpec(specPath)
	if err != nil {
		t.Fatalf("LoadCustomSpec failed: %v", err)
	}
	if spec.Platform != "my-agent-framework" {
		t.Errorf("expected platform 'my-agent-framework', got %q", spec.Platform)
	}
	if spec.SkillPath != "plugins/{slug}/prompt.md" {
		t.Errorf("expected skill_path template, got %q", spec.SkillPath)
	}
	if !spec.Frontmatter {
		t.Error("expected frontmatter=true")
	}
	if len(spec.FrontmatterFields) != 3 {
		t.Errorf("expected 3 frontmatter fields, got %d", len(spec.FrontmatterFields))
	}
	if len(spec.Sections) != 3 {
		t.Errorf("expected 3 sections, got %d", len(spec.Sections))
	}
}

func TestLoadCustomSpecMissingSlug(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "bad.yaml")
	content := `platform: test
skill_path: "plugins/prompt.md"
`
	if err := os.WriteFile(specPath, []byte(content), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_, err := LoadCustomSpec(specPath)
	if err == nil {
		t.Error("expected error for missing {slug} placeholder")
	}
}

func TestRenderCustom(t *testing.T) {
	spec := &CustomSpec{
		Platform:          "test-platform",
		SkillPath:         "skills/{slug}/skill.md",
		Frontmatter:       true,
		FrontmatterFields: []string{"name", "description"},
		Sections:          []string{"summary", "process"},
	}
	skills := []Skill{
		{
			Name:    "Alert Triage",
			Slug:    "alert-triage",
			Summary: "Triages incoming alerts",
			Body:    "## When to Invoke\n\n- Alert arrives\n\n## Process\n\n1. Parse alert\n2. Classify\n\n## Constraints\n\n- Must be fast\n",
		},
	}
	files, err := RenderCustom(spec, skills)
	if err != nil {
		t.Fatalf("RenderCustom failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "skills/alert-triage/skill.md" {
		t.Errorf("expected path 'skills/alert-triage/skill.md', got %q", files[0].Path)
	}
	content := files[0].Content
	if !strings.Contains(content, "---\nname: alert-triage") {
		t.Error("missing frontmatter name")
	}
	if !strings.Contains(content, "Triages incoming alerts") {
		t.Error("missing summary")
	}
	if !strings.Contains(content, "## Process") {
		t.Error("missing process section")
	}
	if strings.Contains(content, "## Constraints") {
		t.Error("constraints should not be present (not in sections list)")
	}
}

func TestRenderCustomNoSections(t *testing.T) {
	spec := &CustomSpec{
		Platform:  "full-output",
		SkillPath: "{slug}.md",
	}
	skills := []Skill{
		{
			Name:    "Test",
			Slug:    "test",
			Summary: "Test skill",
			Body:    "## Process\n\n1. Do stuff\n",
		},
	}
	files, err := RenderCustom(spec, skills)
	if err != nil {
		t.Fatalf("RenderCustom failed: %v", err)
	}
	if !strings.Contains(files[0].Content, "Test skill") {
		t.Error("missing summary in full output")
	}
	if !strings.Contains(files[0].Content, "## Process") {
		t.Error("missing process in full output")
	}
}

func TestBuildCustomSkillContentWithFrontmatter(t *testing.T) {
	spec := &CustomSpec{
		Frontmatter:       true,
		FrontmatterFields: []string{"name", "version"},
		Sections:          []string{"summary"},
		SkillPath:         "{slug}.md",
	}
	skill := Skill{
		Name:    "My Skill",
		Slug:    "my-skill",
		Summary: "Does things",
		Body:    "## Process\n\n1. Step one\n",
	}
	content := BuildCustomSkillContent(skill, spec)
	if !strings.HasPrefix(content, "---\n") {
		t.Error("expected frontmatter start")
	}
	if !strings.Contains(content, "name: my-skill") {
		t.Error("missing name in frontmatter")
	}
	if !strings.Contains(content, "version: \"1.0\"") {
		t.Error("missing version in frontmatter")
	}
	if !strings.Contains(content, "Does things") {
		t.Error("missing summary")
	}
	if strings.Contains(content, "## Process") {
		t.Error("process should not be present")
	}
}

func TestParseBodySections(t *testing.T) {
	body := "## When to Invoke\n\n- Alert arrives\n\n## Process\n\n1. Parse\n2. Act\n\n## Constraints\n\n- Be fast\n"
	sections := parseBodySections(body)
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	if sections[0].name != "When to Invoke" {
		t.Errorf("expected 'When to Invoke', got %q", sections[0].name)
	}
	if sections[1].name != "Process" {
		t.Errorf("expected 'Process', got %q", sections[1].name)
	}
	if sections[2].name != "Constraints" {
		t.Errorf("expected 'Constraints', got %q", sections[2].name)
	}
}
