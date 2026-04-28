// openapi.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// OpenAPI/Swagger spec structured parser.
// Detects and parses OpenAPI/Swagger YAML/JSON specs, extracting endpoints,
// methods, parameters, and schemas into structured summaries for the prompt.


package ingestion

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// OpenAPIEndpoint represents a parsed API endpoint.
type OpenAPIEndpoint struct {
	Path        string
	Method      string
	Summary     string
	Description string
	Parameters  []OpenAPIParam
	RequestBody string
	Responses   string
}

// OpenAPIParam represents an API parameter.
type OpenAPIParam struct {
	Name        string
	In          string // query, path, header, cookie
	Type        string
	Description string
	Required    bool
}

// OpenAPIResult holds the parsed OpenAPI spec.
type OpenAPIResult struct {
	Title     string
	Version   string
	BasePath  string
	Endpoints []OpenAPIEndpoint
}

// isOpenAPISpec detects whether file content is an OpenAPI or Swagger spec.
func isOpenAPISpec(content string, ext string) bool {
	ext = strings.ToLower(ext)
	if ext != ".yaml" && ext != ".yml" && ext != ".json" {
		return false
	}

	// Quick string check before attempting parse
	limit := len(content)
	if limit > 500 {
		limit = 500
	}
	lower := strings.ToLower(content[:limit])
	return strings.Contains(lower, "openapi:") || strings.Contains(lower, "\"openapi\"") ||
		strings.Contains(lower, "swagger:") || strings.Contains(lower, "\"swagger\"")
}

// parseOpenAPISpec parses an OpenAPI/Swagger spec and returns structured data.
func parseOpenAPISpec(content string) (*OpenAPIResult, error) {
	var raw map[string]interface{}

	// Try YAML first (also handles JSON since JSON is valid YAML)
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		// Try pure JSON
		if jsonErr := json.Unmarshal([]byte(content), &raw); jsonErr != nil {
			return nil, fmt.Errorf("parse OpenAPI spec: %w", err)
		}
	}

	result := &OpenAPIResult{}

	// Extract info
	if info, ok := raw["info"].(map[string]interface{}); ok {
		if title, ok := info["title"].(string); ok {
			result.Title = title
		}
		if version, ok := info["version"].(string); ok {
			result.Version = version
		}
	}

	// Extract basePath (Swagger 2.0)
	if basePath, ok := raw["basePath"].(string); ok {
		result.BasePath = basePath
	}

	// Extract paths
	paths, ok := raw["paths"].(map[string]interface{})
	if !ok {
		return result, nil
	}

	sortedPaths := make([]string, 0, len(paths))
	for p := range paths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, pathStr := range sortedPaths {
		methods, ok := paths[pathStr].(map[string]interface{})
		if !ok {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
			opRaw, ok := methods[method]
			if !ok {
				continue
			}
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}
			endpoint := OpenAPIEndpoint{
				Path:   pathStr,
				Method: strings.ToUpper(method),
			}
			if summary, ok := op["summary"].(string); ok {
				endpoint.Summary = summary
			}
			if desc, ok := op["description"].(string); ok {
				endpoint.Description = desc
			}

			// Parameters
			if params, ok := op["parameters"].([]interface{}); ok {
				for _, pRaw := range params {
					if p, ok := pRaw.(map[string]interface{}); ok {
						param := OpenAPIParam{}
						if name, ok := p["name"].(string); ok {
							param.Name = name
						}
						if in, ok := p["in"].(string); ok {
							param.In = in
						}
						if desc, ok := p["description"].(string); ok {
							param.Description = desc
						}
						if req, ok := p["required"].(bool); ok {
							param.Required = req
						}
						// Type from schema or direct
						if schema, ok := p["schema"].(map[string]interface{}); ok {
							if t, ok := schema["type"].(string); ok {
								param.Type = t
							}
						} else if t, ok := p["type"].(string); ok {
							param.Type = t
						}
						endpoint.Parameters = append(endpoint.Parameters, param)
					}
				}
			}

			result.Endpoints = append(result.Endpoints, endpoint)
		}
	}

	return result, nil
}

// formatOpenAPIAsStructured formats parsed OpenAPI data as a structured summary
// suitable for LLM consumption as FileEntry content.
func formatOpenAPIAsStructured(result *OpenAPIResult) string {
	var b strings.Builder

	b.WriteString("# OpenAPI Specification\n\n")
	if result.Title != "" {
		b.WriteString("**Title**: ")
		b.WriteString(result.Title)
		b.WriteString("\n")
	}
	if result.Version != "" {
		b.WriteString("**Version**: ")
		b.WriteString(result.Version)
		b.WriteString("\n")
	}
	if result.BasePath != "" {
		b.WriteString("**Base Path**: ")
		b.WriteString(result.BasePath)
		b.WriteString("\n")
	}
	b.WriteString("\n## Endpoints\n\n")

	if len(result.Endpoints) == 0 {
		b.WriteString("No endpoints found.\n")
		return b.String()
	}

	for _, ep := range result.Endpoints {
		b.WriteString("### ")
		b.WriteString(ep.Method)
		b.WriteString(" ")
		b.WriteString(ep.Path)
		b.WriteString("\n\n")
		if ep.Summary != "" {
			b.WriteString(ep.Summary)
			b.WriteString("\n\n")
		}
		if ep.Description != "" && ep.Description != ep.Summary {
			b.WriteString(ep.Description)
			b.WriteString("\n\n")
		}
		if len(ep.Parameters) > 0 {
			b.WriteString("**Parameters**:\n")
			for _, p := range ep.Parameters {
				b.WriteString("- `")
				b.WriteString(p.Name)
				b.WriteString("`")
				if p.In != "" {
					b.WriteString(" (")
					b.WriteString(p.In)
					b.WriteString(")")
				}
				if p.Type != "" {
					b.WriteString(" [")
					b.WriteString(p.Type)
					b.WriteString("]")
				}
				if p.Required {
					b.WriteString(" *required*")
				}
				if p.Description != "" {
					b.WriteString(": ")
					b.WriteString(p.Description)
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}
