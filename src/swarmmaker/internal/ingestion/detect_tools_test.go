// detect_tools_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for source code tool detection during ingestion.

package ingestion

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectsGoTools(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "webhook.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes"), 0644)

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}
	if len(ctx.DetectedTools) != 2 {
		t.Fatalf("DetectedTools = %d, want 2", len(ctx.DetectedTools))
	}
	for _, tool := range ctx.DetectedTools {
		if tool.Language != "go" {
			t.Errorf("tool %s language = %q, want go", tool.Path, tool.Language)
		}
	}
	// Check purpose inference
	purposes := map[string]string{}
	for _, tool := range ctx.DetectedTools {
		purposes[tool.Path] = tool.Purpose
	}
	if purposes["main.go"] != "entry point" {
		t.Errorf("main.go purpose = %q, want %q", purposes["main.go"], "entry point")
	}
	if purposes["webhook.go"] != "webhook handler" {
		t.Errorf("webhook.go purpose = %q, want %q", purposes["webhook.go"], "webhook handler")
	}
}

func TestDetectsPythonTools(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "validate.py"), []byte("def validate(): pass"), 0644)
	os.WriteFile(filepath.Join(dir, "deploy.py"), []byte("def deploy(): pass"), 0644)

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}
	if len(ctx.DetectedTools) != 2 {
		t.Fatalf("DetectedTools = %d, want 2", len(ctx.DetectedTools))
	}
	for _, tool := range ctx.DetectedTools {
		if tool.Language != "python" {
			t.Errorf("tool %s language = %q, want python", tool.Path, tool.Language)
		}
	}
}

func TestNoToolsForDocsOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Readme"), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("Some notes"), 0644)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("key: val"), 0644)

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}
	if len(ctx.DetectedTools) != 0 {
		t.Errorf("DetectedTools = %d, want 0 for docs-only input", len(ctx.DetectedTools))
	}
}

func TestDetectsMultiLanguageTools(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "client.ts"), []byte("export {}"), 0644)
	os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh"), 0644)

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}
	if len(ctx.DetectedTools) != 3 {
		t.Fatalf("DetectedTools = %d, want 3", len(ctx.DetectedTools))
	}
	langs := map[string]bool{}
	for _, tool := range ctx.DetectedTools {
		langs[tool.Language] = true
	}
	for _, want := range []string{"go", "typescript", "shell"} {
		if !langs[want] {
			t.Errorf("missing detected language %q", want)
		}
	}
}

func TestInferPurpose(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "entry point"},
		{"webhook.go", "webhook handler"},
		{"validate.py", "validator"},
		{"deploy.sh", "deployment script"},
		{"config.py", "config loader"},
		{"test_api.py", "test file"},
		{"utils.go", "utility"},
		{"server.ts", "server"},
		{"client.js", "client"},
		{"handler.go", "request handler"},
		{"model.py", "data model"},
		{"router.ts", "router"},
		{"middleware.js", "middleware"},
		{"process.go", "source file"},
	}
	for _, tc := range cases {
		got := inferPurpose(tc.path)
		if got != tc.want {
			t.Errorf("inferPurpose(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
