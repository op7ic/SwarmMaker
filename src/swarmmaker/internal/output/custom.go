// custom.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// YAML-based custom platform renderer.
// Allows users to define custom output platforms via YAML config files
// instead of hardcoded Go renderers. Parses the spec, applies path templates,
// and generates platform-specific output trees.


package output

import (
	"fmt"
	"os"
	"strings"

	"github.com/op7ic/swarmmaker/internal/textutil"
	"gopkg.in/yaml.v3"
)

// CustomSpec defines a user-configurable output platform via YAML.
type CustomSpec struct {
	Platform          string   `yaml:"platform"`
	SkillPath         string   `yaml:"skill_path"`
	Frontmatter       bool     `yaml:"frontmatter"`
	FrontmatterFields []string `yaml:"frontmatter_fields"`
	Sections          []string `yaml:"sections"`
}

// LoadCustomSpec reads and validates a custom renderer spec from a YAML file.
func LoadCustomSpec(path string) (*CustomSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read custom spec %q: %w", path, err)
	}
	var spec CustomSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse custom spec %q: %w", path, err)
	}
	if err := validateCustomSpec(&spec); err != nil {
		return nil, fmt.Errorf("validate custom spec %q: %w", path, err)
	}
	return &spec, nil
}

func validateCustomSpec(spec *CustomSpec) error {
	if strings.TrimSpace(spec.Platform) == "" {
		return fmt.Errorf("platform name is required")
	}
	if strings.TrimSpace(spec.SkillPath) == "" {
		return fmt.Errorf("skill_path is required")
	}
	if !strings.Contains(spec.SkillPath, "{slug}") {
		return fmt.Errorf("skill_path must contain {slug} placeholder")
	}
	return nil
}

// RenderCustom generates output files for all skills using the custom spec.
func RenderCustom(spec *CustomSpec, skills []Skill) ([]FileArtifact, error) {
	if spec == nil {
		return nil, fmt.Errorf("custom spec is nil")
	}
	files := make([]FileArtifact, 0, len(skills))
	for _, skill := range skills {
		slug := textutil.Slugify(skill.Slug)
		path := strings.ReplaceAll(spec.SkillPath, "{slug}", slug)
		content := BuildCustomSkillContent(skill, spec)
		files = append(files, FileArtifact{
			Path:    path,
			Content: content,
		})
	}
	return files, nil
}

// BuildCustomSkillContent renders a skill with the custom spec's configuration.
func BuildCustomSkillContent(skill Skill, spec *CustomSpec) string {
	var b strings.Builder

	if spec.Frontmatter {
		b.WriteString("---\n")
		for _, field := range spec.FrontmatterFields {
			switch strings.ToLower(field) {
			case "name":
				b.WriteString("name: ")
				b.WriteString(textutil.Slugify(skill.Slug))
				b.WriteString("\n")
			case "description":
				b.WriteString("description: >-\n  ")
				b.WriteString(strings.TrimSpace(skill.Summary))
				b.WriteString("\n")
			case "version":
				b.WriteString("version: \"1.0\"\n")
			case "slug":
				b.WriteString("slug: ")
				b.WriteString(textutil.Slugify(skill.Slug))
				b.WriteString("\n")
			default:
				b.WriteString(field)
				b.WriteString(": \"\"\n")
			}
		}
		b.WriteString("---\n")
	}

	b.WriteString("# ")
	b.WriteString(skill.Name)
	b.WriteString("\n\n")

	if len(spec.Sections) == 0 {
		b.WriteString(skill.Summary)
		b.WriteString("\n\n")
		b.WriteString(skill.Body)
		if !strings.HasSuffix(skill.Body, "\n") {
			b.WriteString("\n")
		}
		return b.String()
	}

	sections := parseBodySections(skill.Body)
	for _, requested := range spec.Sections {
		lower := strings.ToLower(requested)
		if lower == "summary" {
			b.WriteString(skill.Summary)
			b.WriteString("\n\n")
			continue
		}
		for _, sec := range sections {
			if strings.ToLower(sec.name) == lower ||
				strings.Contains(strings.ToLower(sec.name), lower) {
				b.WriteString("## ")
				b.WriteString(sec.name)
				b.WriteString("\n\n")
				b.WriteString(sec.content)
				if !strings.HasSuffix(sec.content, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("\n")
				break
			}
		}
	}

	return b.String()
}

type bodySection struct {
	name    string
	content string
}

func parseBodySections(body string) []bodySection {
	lines := strings.Split(body, "\n")
	var sections []bodySection
	currentName := ""
	var currentLines []string

	flush := func() {
		if currentName != "" {
			sections = append(sections, bodySection{
				name:    currentName,
				content: strings.TrimSpace(strings.Join(currentLines, "\n")),
			})
		}
		currentLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			currentName = strings.TrimPrefix(trimmed, "## ")
			continue
		}
		if currentName != "" {
			currentLines = append(currentLines, line)
		}
	}
	flush()
	return sections
}
