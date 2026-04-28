// mcp.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// MCP tool definition builder.
// Parses skill bodies to extract parameter definitions and trigger conditions,
// then builds MCP-compatible tool definition JSON for each skill.


package output

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MCPToolDef represents an MCP-compatible tool definition.
type MCPToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema MCPInputSchema `json:"input_schema"`
}

// MCPInputSchema is the JSON Schema for tool inputs.
type MCPInputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]MCPProperty `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// MCPProperty describes a single input parameter.
type MCPProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// BuildMCPTool constructs an MCP tool definition from a skill.
func BuildMCPTool(skill Skill) MCPToolDef {
	return MCPToolDef{
		Name:        skillSlug(skill.Slug),
		Description: buildMCPDescription(skill),
		InputSchema: buildMCPInputSchema(skill),
	}
}

// BuildMCPToolJSON returns indented JSON for the MCP tool definition.
func BuildMCPToolJSON(skill Skill) ([]byte, error) {
	def := BuildMCPTool(skill)
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal MCP tool for %q: %w", skill.Slug, err)
	}
	return append(data, '\n'), nil
}

func buildMCPDescription(skill Skill) string {
	// Use summary + "When to Invoke" triggers
	desc := strings.TrimSpace(skill.Summary)
	triggers := extractWhenToInvokeTriggers(skill.Body)
	if len(triggers) > 0 {
		desc += ". Use when " + strings.Join(triggers, ", ") + "."
	}
	if len(desc) > 200 {
		desc = desc[:197] + "..."
	}
	return desc
}

// inputParamRe matches lines like "- field_name (type): description" or "- field_name: description"
var inputParamRe = regexp.MustCompile(`(?m)^-\s+(\w[\w._-]*)\s*(?:\(([^)]+)\))?\s*(?::\s*(.+))?$`)

func buildMCPInputSchema(skill Skill) MCPInputSchema {
	schema := MCPInputSchema{
		Type:       "object",
		Properties: make(map[string]MCPProperty),
	}

	// Extract parameters from "Inputs Required" section
	lines := strings.Split(skill.Body, "\n")
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "## Inputs Required") {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !inSection {
			continue
		}
		matches := inputParamRe.FindStringSubmatch(trimmed)
		if len(matches) >= 2 {
			name := matches[1]
			propType := "string"
			desc := ""
			if len(matches) >= 3 && matches[2] != "" {
				propType = normalizeMCPType(matches[2])
			}
			if len(matches) >= 4 {
				desc = strings.TrimSpace(matches[3])
			}
			schema.Properties[name] = MCPProperty{
				Type:        propType,
				Description: desc,
			}
		}
	}

	return schema
}

func normalizeMCPType(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case lower == "string" || lower == "str" || lower == "text":
		return "string"
	case lower == "int" || lower == "integer" || lower == "int64" || lower == "int32":
		return "integer"
	case lower == "float" || lower == "float64" || lower == "double" || lower == "number":
		return "number"
	case lower == "bool" || lower == "boolean":
		return "boolean"
	case lower == "array" || lower == "list" || lower == "[]string" || strings.HasPrefix(lower, "[]"):
		return "array"
	case lower == "object" || lower == "map" || lower == "dict":
		return "object"
	default:
		return "string"
	}
}
