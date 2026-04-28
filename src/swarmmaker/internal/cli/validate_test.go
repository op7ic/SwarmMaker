// validate_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateCommandExists(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "validate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("validate subcommand not registered on rootCmd")
	}
}

func TestValidateRequiresFlags(t *testing.T) {
	cmd := rootCmd
	// Find the validate subcommand
	var valCmd *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Use == "validate" {
			valCmd = c
			break
		}
	}
	if valCmd == nil {
		t.Fatal("validate subcommand not found")
	}

	bundleFlag := valCmd.Flags().Lookup("bundle")
	if bundleFlag == nil {
		t.Fatal("--bundle flag not registered")
	}

	targetFlag := valCmd.Flags().Lookup("target")
	if targetFlag == nil {
		t.Fatal("--target flag not registered")
	}

	// Verify flags exist and have correct annotations indicating required
	bundleAnnotations := bundleFlag.Annotations
	if _, ok := bundleAnnotations["cobra_annotation_bash_completion_one_required_flag"]; !ok {
		t.Error("--bundle flag should be marked as required")
	}
	targetAnnotations := targetFlag.Annotations
	if _, ok := targetAnnotations["cobra_annotation_bash_completion_one_required_flag"]; !ok {
		t.Error("--target flag should be marked as required")
	}
}

func TestParseSkillFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		slug        string
		content     string
		wantName    string
		wantDesc    string
	}{
		{
			name: "full frontmatter",
			slug: "alert-ingestion",
			content: `---
name: Alert Ingestion
description: Ingest and normalize security alerts from multiple sources
---

## Instructions
Handle alert ingestion workflow.
`,
			wantName: "Alert Ingestion",
			wantDesc: "Ingest and normalize security alerts from multiple sources",
		},
		{
			name:     "no frontmatter",
			slug:     "my-skill",
			content:  "Just some instructions without frontmatter.",
			wantName: "my-skill",
			wantDesc: "",
		},
		{
			name: "name only",
			slug: "runbook-gen",
			content: `---
name: Runbook Generator
---

Generate runbooks.
`,
			wantName: "Runbook Generator",
			wantDesc: "",
		},
		{
			name: "description only",
			slug: "classify",
			content: `---
description: Classify incoming alerts by severity
---

Classify alerts.
`,
			wantName: "classify",
			wantDesc: "Classify incoming alerts by severity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := parseSkillFrontmatter(tt.slug, tt.content)
			if info.Slug != tt.slug {
				t.Errorf("Slug = %q, want %q", info.Slug, tt.slug)
			}
			if info.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", info.Name, tt.wantName)
			}
			if info.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", info.Description, tt.wantDesc)
			}
		})
	}
}

func TestContainsSkillName(t *testing.T) {
	tests := []struct {
		name   string
		output string
		slug   string
		skill  string
		want   bool
	}{
		{"slug match", "I found alert-ingestion in the list", "alert-ingestion", "Alert Ingestion", true},
		{"name match", "I found Alert Ingestion in the list", "alert-ingestion-x", "Alert Ingestion", true},
		{"case insensitive slug", "ALERT-INGESTION is available", "alert-ingestion", "Alert Ingestion", true},
		{"no match", "There are no matching skills", "alert-ingestion", "Alert Ingestion", false},
		{"empty output", "", "alert-ingestion", "Alert Ingestion", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsSkillName(tt.output, tt.slug, tt.skill)
			if got != tt.want {
				t.Errorf("containsSkillName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadBundleSkills(t *testing.T) {
	// Set up a temp bundle directory with skills.
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".agents", "skills")

	// Create two skill directories with SKILL.md files.
	skill1Dir := filepath.Join(skillsDir, "alert-ingestion")
	if err := os.MkdirAll(skill1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(`---
name: Alert Ingestion
description: Ingest alerts from monitoring systems
---

Handle alert ingestion.
`), 0644); err != nil {
		t.Fatal(err)
	}

	skill2Dir := filepath.Join(skillsDir, "runbook-gen")
	if err := os.MkdirAll(skill2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(`---
name: Runbook Generator
description: Generate operational runbooks
---

Create runbooks.
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a directory without SKILL.md (should be skipped).
	emptyDir := filepath.Join(skillsDir, "empty-skill")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}

	skills, err := readBundleSkills(tmp)
	if err != nil {
		t.Fatalf("readBundleSkills() error: %v", err)
	}

	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}

	// Skills are returned in directory order; find by slug.
	found := make(map[string]skillInfo)
	for _, s := range skills {
		found[s.Slug] = s
	}

	s1, ok := found["alert-ingestion"]
	if !ok {
		t.Fatal("alert-ingestion skill not found")
	}
	if s1.Name != "Alert Ingestion" {
		t.Errorf("alert-ingestion Name = %q, want %q", s1.Name, "Alert Ingestion")
	}
	if s1.Description != "Ingest alerts from monitoring systems" {
		t.Errorf("alert-ingestion Description = %q", s1.Description)
	}

	s2, ok := found["runbook-gen"]
	if !ok {
		t.Fatal("runbook-gen skill not found")
	}
	if s2.Name != "Runbook Generator" {
		t.Errorf("runbook-gen Name = %q, want %q", s2.Name, "Runbook Generator")
	}
}

func TestBuildSkillCatalog(t *testing.T) {
	skills := []skillInfo{
		{Slug: "alert-triage", Name: "Alert Triage", Description: "Classify incoming alerts by severity"},
		{Slug: "hash-hunt", Name: "hash-hunt", Description: "Hunt for known malicious hashes"},
		{Slug: "no-desc", Name: "No Description", Description: ""},
	}
	catalog := buildSkillCatalog(skills)
	if !strings.Contains(catalog, "**alert-triage** (Alert Triage): Classify incoming alerts") {
		t.Errorf("catalog missing alert-triage with name and description, got:\n%s", catalog)
	}
	// When name == slug, name should not be repeated in parentheses
	if strings.Contains(catalog, "(hash-hunt)") {
		t.Errorf("catalog should not repeat slug as name in parentheses, got:\n%s", catalog)
	}
	if !strings.Contains(catalog, "**hash-hunt**: Hunt for known") {
		t.Errorf("catalog missing hash-hunt, got:\n%s", catalog)
	}
	if !strings.Contains(catalog, "**no-desc** (No Description)") {
		t.Errorf("catalog missing no-desc, got:\n%s", catalog)
	}
}

func TestBuildSkillCatalogEmpty(t *testing.T) {
	catalog := buildSkillCatalog(nil)
	if catalog != "" {
		t.Errorf("expected empty catalog for nil skills, got %q", catalog)
	}
}

func TestReadBundleSkillsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	_, err := readBundleSkills(tmp)
	if err == nil {
		t.Fatal("expected error for missing skills directory")
	}
}
