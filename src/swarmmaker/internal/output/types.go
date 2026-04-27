// types.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Output format type definitions.
// Defines Format (claude/codex/gemini), Document, Agent, Skill, Blueprint,
// FileArtifact, Manifest, and TreeSpec types. Includes default tree
// specifications for each platform: Claude uses .claude/SKILL.md, Codex
// uses .codex/AGENTS.md, Gemini uses .gemini/GEMINI.md.


package output

import "sort"

// Format identifies a supported swarm output tree.
type Format string

const (
	FormatClaude Format = "claude"
	FormatCodex  Format = "codex"
	FormatGemini Format = "gemini"
)

// Document is shared source input for bundle generation.
type Document struct {
	Path    string
	Content string
}

// Agent is a concrete swarm coordination role used by the platform entrypoint.
type Agent struct {
	Name         string
	Role         string
	Instructions string
}

// Skill is a generated installable unit for a target platform.
type Skill struct {
	Name    string
	Slug    string
	Summary string
	Body    string
}

// Blueprint is the shared representation that renderers consume.
type Blueprint struct {
	Name      string
	Purpose   string
	UsageHint string
	Metadata  map[string]string
	Docs      []Document
	Agents    []Agent
	Skills    []Skill
}

// FileArtifact is a rendered file within the final output tree.
type FileArtifact struct {
	Path    string
	Content string
}

// Manifest is the canonical renderer output.
type Manifest struct {
	Format   Format
	RootDir  string
	Metadata map[string]string
	Files    []FileArtifact
}

// TreeSpec declares the required shape for a given output format.
type TreeSpec struct {
	Format               Format
	RootDir              string
	EntryFile            string
	ReadmeFile           string
	SkillIndexFile       string
	SkillDir             string
	RequiredFiles        []string
	RequiredPrefixCounts map[string]int
	RequiredMetadataKeys []string
}

// DefaultSpecs returns the built-in renderer contracts.
func DefaultSpecs() map[Format]TreeSpec {
	return map[Format]TreeSpec{
		FormatClaude: {
			Format:       FormatClaude,
			RootDir:      ".claude",
			EntryFile:    "SKILL.md",
			ReadmeFile:   "README.md",
			SkillDir:     "skills",
			RequiredFiles: []string{
				"SKILL.md",
				"README.md",
			},
			RequiredPrefixCounts: map[string]int{
				".claude/skills/": 1,
			},
			RequiredMetadataKeys: []string{"name", "purpose", "format", "skill_count", "agent_count"},
		},
		FormatCodex: {
			Format:         FormatCodex,
			RootDir:        ".codex",
			EntryFile:      "AGENTS.md",
			ReadmeFile:     "README.md",
			SkillIndexFile: "instructions/index.md",
			SkillDir:       "instructions",
			RequiredFiles: []string{
				"AGENTS.md",
				"README.md",
				"instructions/index.md",
			},
			RequiredPrefixCounts: map[string]int{
				".codex/instructions/": 2,
			},
			RequiredMetadataKeys: []string{"name", "purpose", "format", "skill_count", "agent_count"},
		},
		FormatGemini: {
			Format:         FormatGemini,
			RootDir:        ".gemini",
			EntryFile:      "GEMINI.md",
			ReadmeFile:     "README.md",
			SkillIndexFile: "playbooks/index.md",
			SkillDir:       "playbooks",
			RequiredFiles: []string{
				"GEMINI.md",
				"README.md",
				"playbooks/index.md",
			},
			RequiredPrefixCounts: map[string]int{
				".gemini/playbooks/": 2,
			},
			RequiredMetadataKeys: []string{"name", "purpose", "format", "skill_count", "agent_count"},
		},
	}
}

func sortedFormats(specs map[Format]TreeSpec) []Format {
	formats := make([]Format, 0, len(specs))
	for format := range specs {
		formats = append(formats, format)
	}
	sort.Slice(formats, func(i, j int) bool { return formats[i] < formats[j] })
	return formats
}
