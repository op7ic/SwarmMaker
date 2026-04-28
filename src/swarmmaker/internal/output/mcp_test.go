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

func TestBuildMCPToolFencedJSON(t *testing.T) {
	// Primary path: skill has a fenced JSON block in MCP Input Schema section
	skill := Skill{
		Name:    "Alert Triage",
		Slug:    "alert-triage",
		Summary: "Classifies incoming alerts",
		Body: `### When to Invoke
- Alert arrives from upstream

### Inputs Required
- config_path (str): Path to config file
- alert_data (object): Raw alert payload with nested fields

### MCP Input Schema
` + "```json" + `
{"type": "object", "properties": {"config_path": {"type": "string", "description": "Path to AMP config file"}, "alert_data": {"type": "object", "description": "Raw alert payload"}, "severity": {"type": "integer", "description": "Alert severity 0-4"}, "tags": {"type": "array", "description": "Associated IOC tags"}}, "required": ["config_path", "alert_data"]}
` + "```" + `

### Process
1. Parse alert
`,
	}
	def := BuildMCPTool(skill)
	if len(def.InputSchema.Properties) != 4 {
		t.Fatalf("expected 4 properties from fenced JSON, got %d: %+v",
			len(def.InputSchema.Properties), def.InputSchema.Properties)
	}
	if p := def.InputSchema.Properties["config_path"]; p.Type != "string" || p.Description != "Path to AMP config file" {
		t.Errorf("config_path mismatch: %+v", p)
	}
	if p := def.InputSchema.Properties["severity"]; p.Type != "integer" {
		t.Errorf("severity should be integer, got %+v", p)
	}
	if p := def.InputSchema.Properties["tags"]; p.Type != "array" {
		t.Errorf("tags should be array, got %+v", p)
	}
	if len(def.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d: %v", len(def.InputSchema.Required), def.InputSchema.Required)
	}
}

func TestBuildMCPToolFencedJSONH2(t *testing.T) {
	// Fenced JSON with ## heading (non-nested skills)
	skill := Skill{
		Name:    "Simple",
		Slug:    "simple",
		Summary: "Simple skill",
		Body: `## MCP Input Schema
` + "```json" + `
{"type": "object", "properties": {"name": {"type": "string", "description": "User name"}}}
` + "```" + `
`,
	}
	def := BuildMCPTool(skill)
	if len(def.InputSchema.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(def.InputSchema.Properties))
	}
	if p := def.InputSchema.Properties["name"]; p.Type != "string" {
		t.Errorf("name should be string, got %+v", p)
	}
}

func TestBuildMCPToolFencedJSONTakesPrecedence(t *testing.T) {
	// When both fenced JSON and bullet list exist, fenced JSON wins
	skill := Skill{
		Name:    "Both",
		Slug:    "both",
		Summary: "Has both formats",
		Body: `### Inputs Required
- name (string): The name
- age (integer): The age

### MCP Input Schema
` + "```json" + `
{"type": "object", "properties": {"full_name": {"type": "string", "description": "Full name"}, "birth_year": {"type": "integer", "description": "Year of birth"}, "active": {"type": "boolean", "description": "Is active"}}}
` + "```" + `
`,
	}
	def := BuildMCPTool(skill)
	// Should have 3 from fenced JSON, not 2 from bullets
	if len(def.InputSchema.Properties) != 3 {
		t.Fatalf("expected 3 properties from fenced JSON (not 2 from bullets), got %d: %+v",
			len(def.InputSchema.Properties), def.InputSchema.Properties)
	}
	if _, ok := def.InputSchema.Properties["full_name"]; !ok {
		t.Error("expected full_name from fenced JSON, not name from bullets")
	}
}

func TestBuildMCPToolInvalidFencedJSONFallsBack(t *testing.T) {
	// If fenced JSON is malformed, fall back to bullet parsing
	skill := Skill{
		Name:    "Bad JSON",
		Slug:    "bad-json",
		Summary: "Has bad JSON but good bullets",
		Body: `### Inputs Required
- name (string): The name

### MCP Input Schema
` + "```json" + `
{invalid json here}
` + "```" + `
`,
	}
	def := BuildMCPTool(skill)
	if len(def.InputSchema.Properties) != 1 {
		t.Fatalf("expected 1 property from bullet fallback, got %d", len(def.InputSchema.Properties))
	}
	if _, ok := def.InputSchema.Properties["name"]; !ok {
		t.Error("expected name from bullet fallback")
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
