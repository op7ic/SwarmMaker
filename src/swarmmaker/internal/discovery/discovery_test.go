// discovery_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for LLM CLI discovery.
// Uses function-variable mocking for PATH lookup and version detection.
// Covers multi-tool discovery, capability annotation, and metadata validation.


package discovery

import (
	"errors"
	"strings"
	"testing"
)

func TestFindAllLLMsAnnotatesCapabilitiesAndVersionMetadata(t *testing.T) {
	oldLookPath := lookPathFunc
	oldVersion := versionOutputFunc
	t.Cleanup(func() {
		lookPathFunc = oldLookPath
		versionOutputFunc = oldVersion
	})

	lookPathFunc = func(name string) (string, error) {
		switch name {
		case "claude":
			return "/opt/bin/claude", nil
		case "codex":
			return "/opt/bin/codex", nil
		default:
			return "", errors.New("missing binary")
		}
	}

	versionOutputFunc = func(cmd []string) ([]byte, error) {
		switch cmd[0] {
		case "claude":
			return []byte("claude 1.2.3\nignored diagnostics"), nil
		case "codex":
			return []byte("codex 4.5.6"), nil
		default:
			return []byte(""), nil
		}
	}

	tools := FindAllLLMs()
	if got, want := len(tools), 3; got != want {
		t.Fatalf("tool count = %d, want %d", got, want)
	}

	claude := tools[0]
	if !claude.Available {
		t.Fatalf("claude should be available")
	}
	if claude.Path != "/opt/bin/claude" {
		t.Fatalf("claude path = %q, want %q", claude.Path, "/opt/bin/claude")
	}
	if claude.Version != "claude 1.2.3" {
		t.Fatalf("claude version = %q, want %q", claude.Version, "claude 1.2.3")
	}
	if !claude.Supports(CapabilityRenderOutput) || !claude.Supports(CapabilityBuildTools) {
		t.Fatalf("claude capabilities missing expected roles: %#v", claude.Capabilities)
	}

	gemini := tools[2]
	if gemini.Available {
		t.Fatalf("gemini should be unavailable in this fixture")
	}
	if gemini.Path != "" {
		t.Fatalf("gemini path = %q, want empty", gemini.Path)
	}
	if !gemini.Supports(CapabilityRenderOutput) {
		t.Fatalf("gemini should still advertise output rendering capability")
	}
}

func TestValidateMetadataRejectsMalformedCapabilities(t *testing.T) {
	tool := LLMTool{
		Name:         "alpha",
		Path:         "/tmp/alpha",
		Available:    true,
		Capabilities: []Capability{CapabilityGenerate, Capability("bogus")},
	}

	err := tool.ValidateMetadata()
	if err == nil {
		t.Fatal("expected validation error for malformed capability metadata")
	}
	if !strings.Contains(err.Error(), "unsupported capability") {
		t.Fatalf("error = %v, want unsupported capability", err)
	}
}

func TestValidateKnownMetadataRejectsUnknownProvider(t *testing.T) {
	tool := LLMTool{
		Name:         "alpha",
		Path:         "/tmp/alpha",
		Available:    true,
		Capabilities: CapabilitiesForTool("claude"),
	}

	err := tool.ValidateKnownMetadata()
	if err == nil {
		t.Fatal("expected validation error for unknown provider")
	}
	if !strings.Contains(err.Error(), "not a known CLI tool") {
		t.Fatalf("error = %v, want unknown provider failure", err)
	}
}
