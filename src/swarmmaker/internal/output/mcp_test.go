// mcp_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for MCP tool definition builder.


package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildMCPTool(t *testing.T) {
	skill := Skill{
		Name:    "Test Skill",
		Slug:    "test-skill",
		Summary: "Processes incoming alerts",
		Body: `## When to Invoke

- Alert data arrives from upstream
- New detection rules are deployed

## Inputs Required

- alert_id (string): Unique identifier for the alert
- severity (integer): Alert severity level 0-4
- payload (object): Raw alert payload
- tags (array): Associated tags

## Process

1. Parse the alert
2. Classify it
`,
	}

	def := BuildMCPTool(skill)

	if def.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", def.Name)
	}
	if !strings.Contains(def.Description, "Processes incoming alerts") {
		t.Errorf("description missing summary: %q", def.Description)
	}
	if def.InputSchema.Type != "object" {
		t.Errorf("expected schema type 'object', got %q", def.InputSchema.Type)
	}
	if len(def.InputSchema.Properties) != 4 {
		t.Errorf("expected 4 properties, got %d", len(def.InputSchema.Properties))
	}
	if p, ok := def.InputSchema.Properties["alert_id"]; !ok || p.Type != "string" {
		t.Errorf("expected alert_id as string, got %+v", def.InputSchema.Properties["alert_id"])
	}
	if p, ok := def.InputSchema.Properties["severity"]; !ok || p.Type != "integer" {
		t.Errorf("expected severity as integer, got %+v", def.InputSchema.Properties["severity"])
	}
	if p, ok := def.InputSchema.Properties["payload"]; !ok || p.Type != "object" {
		t.Errorf("expected payload as object, got %+v", def.InputSchema.Properties["payload"])
	}
	if p, ok := def.InputSchema.Properties["tags"]; !ok || p.Type != "array" {
		t.Errorf("expected tags as array, got %+v", def.InputSchema.Properties["tags"])
	}
}

func TestBuildMCPToolJSON(t *testing.T) {
	skill := Skill{
		Name:    "Simple Skill",
		Slug:    "simple-skill",
		Summary: "Does something simple",
		Body:    "## When to Invoke\n\n- Something happens\n\n## Inputs Required\n\n- name (string): The name\n",
	}
	data, err := BuildMCPToolJSON(skill)
	if err != nil {
		t.Fatalf("BuildMCPToolJSON failed: %v", err)
	}
	var def MCPToolDef
	if err := json.Unmarshal(data, &def); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if def.Name != "simple-skill" {
		t.Errorf("expected name 'simple-skill', got %q", def.Name)
	}
}

func TestBuildMCPToolNoInputs(t *testing.T) {
	skill := Skill{
		Name:    "No Inputs",
		Slug:    "no-inputs",
		Summary: "Has no inputs section",
		Body:    "## Process\n\n1. Do something\n",
	}
	def := BuildMCPTool(skill)
	if len(def.InputSchema.Properties) != 0 {
		t.Errorf("expected 0 properties, got %d", len(def.InputSchema.Properties))
	}
}

func TestBuildMCPToolH3Section(t *testing.T) {
	// Real-world format: skills use ### (h3) under ## Instructions
	skill := Skill{
		Name:    "Hash Hunt",
		Slug:    "hash-hunt",
		Summary: "Hunts for hashes",
		Body: `### When to Invoke
- Hash list is provided

### Inputs Required
- ` + "`config_path: str`" + ` with required credentials
- ` + "`hash_input_path: str`" + ` containing SHA256 values
- ` + "`csv_output: str`" + `. Optional output path.

### Process
1. Validate hashes
`,
	}
	def := BuildMCPTool(skill)
	if len(def.InputSchema.Properties) != 3 {
		t.Errorf("expected 3 properties from h3+backtick format, got %d: %+v",
			len(def.InputSchema.Properties), def.InputSchema.Properties)
	}
	if p, ok := def.InputSchema.Properties["config_path"]; !ok || p.Type != "string" {
		t.Errorf("expected config_path as string, got %+v", def.InputSchema.Properties["config_path"])
	}
	if p, ok := def.InputSchema.Properties["hash_input_path"]; !ok || p.Type != "string" {
		t.Errorf("expected hash_input_path as string, got %+v", def.InputSchema.Properties["hash_input_path"])
	}
}

func TestNormalizeMCPType(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"string", "string"},
		{"str", "string"},
		{"int", "integer"},
		{"integer", "integer"},
		{"float", "number"},
		{"bool", "boolean"},
		{"array", "array"},
		{"list", "array"},
		{"[]string", "array"},
		{"object", "object"},
		{"map", "object"},
		{"SomeCustomType", "string"},
	}
	for _, tt := range tests {
		got := normalizeMCPType(tt.input)
		if got != tt.want {
			t.Errorf("normalizeMCPType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
