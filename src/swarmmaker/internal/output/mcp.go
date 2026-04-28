// mcp.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// MCP tool definition builder.
// Extracts MCP-compatible tool definitions from generated skills. The primary
// extraction path reads the fenced JSON block from the "MCP Input Schema"
// section that the skill compiler contract mandates. A regex fallback handles
// legacy skills that pre-date the contract addition.


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

// buildMCPInputSchema extracts the input schema from a skill body.
// Primary path: parse the fenced JSON block from "MCP Input Schema" section.
// Fallback: regex-parse "Inputs Required" bullet lines for legacy skills.
func buildMCPInputSchema(skill Skill) MCPInputSchema {
	// Primary: extract fenced JSON block from MCP Input Schema section
	if schema, ok := extractFencedMCPSchema(skill.Body); ok {
		return schema
	}
	// Fallback: regex-parse Inputs Required section for legacy skills
	return extractInputsFromBullets(skill.Body)
}

// fencedJSONRe matches ```json ... ``` blocks, capturing the JSON content.
var fencedJSONRe = regexp.MustCompile("(?s)```json\\s*\n(.*?)```")

// extractFencedMCPSchema looks for a fenced JSON block in or after the
// "MCP Input Schema" section heading and parses it as MCPInputSchema.
func extractFencedMCPSchema(body string) (MCPInputSchema, bool) {
	lines := strings.Split(body, "\n")
	inSection := false
	var sectionContent strings.Builder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if lower == "## mcp input schema" || lower == "### mcp input schema" {
			inSection = true
			continue
		}
		if inSection && (strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")) {
			break
		}
		if inSection {
			sectionContent.WriteString(line)
			sectionContent.WriteString("\n")
		}
	}

	if !inSection {
		// No dedicated section — scan the entire body for any fenced JSON block
		// that looks like a JSON Schema (has "type" and "properties" keys).
		return extractSchemaFromAnyFencedBlock(body)
	}

	content := sectionContent.String()
	matches := fencedJSONRe.FindStringSubmatch(content)
	if len(matches) < 2 {
		return MCPInputSchema{Type: "object"}, false
	}

	return parseMCPSchemaJSON(matches[1])
}

// extractSchemaFromAnyFencedBlock scans the full body for a fenced JSON block
// that contains "properties" — indicating it's a JSON Schema for MCP.
func extractSchemaFromAnyFencedBlock(body string) (MCPInputSchema, bool) {
	allMatches := fencedJSONRe.FindAllStringSubmatch(body, -1)
	for _, matches := range allMatches {
		if len(matches) < 2 {
			continue
		}
		jsonStr := strings.TrimSpace(matches[1])
		if strings.Contains(jsonStr, "\"properties\"") {
			if schema, ok := parseMCPSchemaJSON(jsonStr); ok {
				return schema, true
			}
		}
	}
	return MCPInputSchema{Type: "object"}, false
}

// parseMCPSchemaJSON parses a JSON string into MCPInputSchema.
func parseMCPSchemaJSON(jsonStr string) (MCPInputSchema, bool) {
	jsonStr = strings.TrimSpace(jsonStr)
	var schema MCPInputSchema
	if err := json.Unmarshal([]byte(jsonStr), &schema); err != nil {
		return MCPInputSchema{Type: "object"}, false
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	if schema.Properties == nil {
		return MCPInputSchema{Type: "object"}, false
	}
	return schema, true
}

// --- Fallback: regex-based extraction for legacy skills ---

// inputParamRe matches lines like "- field_name (type): description"
var inputParamRe = regexp.MustCompile(`^-\s+` + "`?" + `(\w[\w._-]*)` + "`?" + `\s*(?:\(([^)]+)\))?\s*(?::\s*(.+))?$`)

// inputParamBacktickRe matches "- `field_name: type`" patterns
var inputParamBacktickRe = regexp.MustCompile("^-\\s+`(\\w[\\w._-]*)(?::\\s*(\\w+))?`\\s*(.*)")

func extractInputsFromBullets(body string) MCPInputSchema {
	schema := MCPInputSchema{
		Type:       "object",
		Properties: make(map[string]MCPProperty),
	}

	lines := strings.Split(body, "\n")
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if lower == "## inputs required" || lower == "### inputs required" {
			inSection = true
			continue
		}
		if inSection && (strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ")) {
			break
		}
		if !inSection || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		if matches := inputParamBacktickRe.FindStringSubmatch(trimmed); len(matches) >= 2 {
			name := matches[1]
			propType := "string"
			desc := ""
			if len(matches) >= 3 && matches[2] != "" {
				propType = normalizeMCPType(matches[2])
			}
			if len(matches) >= 4 {
				desc = strings.TrimSpace(matches[3])
			}
			schema.Properties[name] = MCPProperty{Type: propType, Description: desc}
			continue
		}
		if matches := inputParamRe.FindStringSubmatch(trimmed); len(matches) >= 2 {
			name := matches[1]
			propType := "string"
			desc := ""
			if len(matches) >= 3 && matches[2] != "" {
				propType = normalizeMCPType(matches[2])
			}
			if len(matches) >= 4 {
				desc = strings.TrimSpace(matches[3])
			}
			schema.Properties[name] = MCPProperty{Type: propType, Description: desc}
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
