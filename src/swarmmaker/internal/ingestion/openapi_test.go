// openapi_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for OpenAPI/Swagger spec structured parser.


package ingestion

import (
	"strings"
	"testing"
)

func TestIsOpenAPISpec(t *testing.T) {
	tests := []struct {
		name    string
		content string
		ext     string
		want    bool
	}{
		{"yaml openapi 3", "openapi: '3.0.0'\ninfo:\n  title: Test\n", ".yaml", true},
		{"json openapi", `{"openapi": "3.0.0", "info": {}}`, ".json", true},
		{"swagger 2", "swagger: '2.0'\ninfo:\n  title: Test\n", ".yml", true},
		{"not openapi yaml", "name: test\nversion: 1\n", ".yaml", false},
		{"wrong extension", "openapi: '3.0.0'\n", ".txt", false},
		{"markdown file", "# API\nopenapi: mentioned in text\n", ".md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOpenAPISpec(tt.content, tt.ext)
			if got != tt.want {
				t.Errorf("isOpenAPISpec() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseOpenAPISpec(t *testing.T) {
	spec := `
openapi: "3.0.0"
info:
  title: Pet Store
  version: "1.0.0"
paths:
  /pets:
    get:
      summary: List all pets
      parameters:
        - name: limit
          in: query
          required: false
          schema:
            type: integer
    post:
      summary: Create a pet
  /pets/{petId}:
    get:
      summary: Get a pet by ID
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
`
	result, err := parseOpenAPISpec(spec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec failed: %v", err)
	}
	if result.Title != "Pet Store" {
		t.Errorf("expected title 'Pet Store', got %q", result.Title)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", result.Version)
	}
	if len(result.Endpoints) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(result.Endpoints))
	}

	// Check first endpoint
	ep := result.Endpoints[0]
	if ep.Method != "GET" || ep.Path != "/pets" {
		t.Errorf("expected GET /pets, got %s %s", ep.Method, ep.Path)
	}
	if ep.Summary != "List all pets" {
		t.Errorf("expected summary 'List all pets', got %q", ep.Summary)
	}
	if len(ep.Parameters) != 1 {
		t.Fatalf("expected 1 parameter, got %d", len(ep.Parameters))
	}
	if ep.Parameters[0].Name != "limit" || ep.Parameters[0].Type != "integer" {
		t.Errorf("unexpected parameter: %+v", ep.Parameters[0])
	}
}

func TestFormatOpenAPIAsStructured(t *testing.T) {
	result := &OpenAPIResult{
		Title:   "Test API",
		Version: "2.0.0",
		Endpoints: []OpenAPIEndpoint{
			{
				Path:    "/users",
				Method:  "GET",
				Summary: "List users",
				Parameters: []OpenAPIParam{
					{Name: "page", In: "query", Type: "integer"},
				},
			},
		},
	}
	output := formatOpenAPIAsStructured(result)
	if !strings.Contains(output, "Test API") {
		t.Error("output missing title")
	}
	if !strings.Contains(output, "GET /users") {
		t.Error("output missing endpoint")
	}
	if !strings.Contains(output, "`page`") {
		t.Error("output missing parameter")
	}
}

func TestParseOpenAPISpecSwagger2(t *testing.T) {
	spec := `
swagger: "2.0"
info:
  title: Legacy API
  version: "1.0"
basePath: /api/v1
paths:
  /items:
    get:
      summary: List items
      parameters:
        - name: q
          in: query
          type: string
          description: Search query
`
	result, err := parseOpenAPISpec(spec)
	if err != nil {
		t.Fatalf("parseOpenAPISpec failed: %v", err)
	}
	if result.Title != "Legacy API" {
		t.Errorf("expected title 'Legacy API', got %q", result.Title)
	}
	if result.BasePath != "/api/v1" {
		t.Errorf("expected basePath '/api/v1', got %q", result.BasePath)
	}
	if len(result.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(result.Endpoints))
	}
	if result.Endpoints[0].Parameters[0].Type != "string" {
		t.Errorf("expected param type 'string', got %q", result.Endpoints[0].Parameters[0].Type)
	}
}
