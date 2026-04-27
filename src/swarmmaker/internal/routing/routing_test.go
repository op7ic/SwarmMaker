// routing_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for provider routing.
// Covers 0/1/2/3-provider scenarios, requested-but-unavailable providers,
// same-model critique fallback counting, and malformed metadata rejection.


package routing

import (
	"strings"
	"testing"

	"github.com/op7ic/swarmmaker/internal/discovery"
)

func TestRouteCoversProviderCountAndFallbackCases(t *testing.T) {
	t.Run("zero providers", func(t *testing.T) {
		plan, err := Route(DefaultRequest(nil))
		if err == nil {
			t.Fatal("expected error for empty provider list")
		}
		if len(plan.Assignments) != 0 {
			t.Fatalf("assignments = %#v, want none", plan.Assignments)
		}
		if !strings.Contains(err.Error(), "no valid providers available") {
			t.Fatalf("error = %v, want no valid providers available", err)
		}
	})

	t.Run("one provider", func(t *testing.T) {
		claude := provider("claude", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityRenderOutput, discovery.CapabilityBuildTools)
		plan, err := Route(DefaultRequest([]discovery.LLMTool{claude}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRoleProvider(t, plan, RoleGenerator, "claude")
		assertRoleProvider(t, plan, RoleCritic, "claude")
		assertRoleProvider(t, plan, RoleOutputRenderer, "claude")
		assertRoleProvider(t, plan, RoleToolBuilder, "claude")
		if got, want := plan.FallbackCount(), 3; got != want {
			t.Fatalf("fallback count = %d, want %d", got, want)
		}
	})

	t.Run("two providers same-model critic fallback", func(t *testing.T) {
		alpha := provider("alpha", discovery.CapabilityGenerate)
		beta := provider("beta", discovery.CapabilityRenderOutput)
		req := DefaultRequest([]discovery.LLMTool{alpha, beta})
		plan, err := Route(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRoleProvider(t, plan, RoleGenerator, "alpha")
		assertRoleProvider(t, plan, RoleCritic, "alpha")
		assertRoleProvider(t, plan, RoleOutputRenderer, "beta")
		assertRoleProvider(t, plan, RoleToolBuilder, "alpha")
		if got, want := plan.FallbackCount(), 2; got != want {
			t.Fatalf("fallback count = %d, want %d", got, want)
		}
		if len(plan.Fallbacks) == 0 || plan.Fallbacks[0].Role != RoleCritic {
			t.Fatalf("expected critic fallback recorded, got %#v", plan.Fallbacks)
		}
	})

	t.Run("three providers deterministic routing", func(t *testing.T) {
		claude := provider("claude", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityRenderOutput, discovery.CapabilityBuildTools)
		codex := provider("codex", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityBuildTools)
		gemini := provider("gemini", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityRenderOutput)
		plan, err := Route(DefaultRequest([]discovery.LLMTool{claude, codex, gemini}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRoleProvider(t, plan, RoleGenerator, "claude")
		assertRoleProvider(t, plan, RoleCritic, "codex")
		assertRoleProvider(t, plan, RoleOutputRenderer, "gemini")
		assertRoleProvider(t, plan, RoleToolBuilder, "claude")
		if got, want := plan.FallbackCount(), 1; got != want {
			t.Fatalf("fallback count = %d, want %d", got, want)
		}
	})
}

func TestRouteFailsForRequestedUnavailableAndUnsupportedRoles(t *testing.T) {
	claude := provider("claude", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityRenderOutput, discovery.CapabilityBuildTools)
	codex := provider("codex", discovery.CapabilityGenerate, discovery.CapabilityCritique, discovery.CapabilityBuildTools)

	t.Run("requested-but-unavailable", func(t *testing.T) {
		req := DefaultRequest([]discovery.LLMTool{claude, codex})
		req.Requested = map[Role]string{RoleOutputRenderer: "missing"}
		_, err := Route(req)
		if err == nil {
			t.Fatal("expected error for unavailable requested provider")
		}
		if !strings.Contains(err.Error(), "requested provider \"missing\" is unavailable") {
			t.Fatalf("error = %v, want unavailable provider failure", err)
		}
	})

	t.Run("unsupported role combination", func(t *testing.T) {
		req := DefaultRequest([]discovery.LLMTool{claude, codex})
		req.Requested = map[Role]string{RoleOutputRenderer: "codex"}
		_, err := Route(req)
		if err == nil {
			t.Fatal("expected error for unsupported role combination")
		}
		if !strings.Contains(err.Error(), "does not support render_output") {
			t.Fatalf("error = %v, want render capability failure", err)
		}
	})

	t.Run("malformed metadata", func(t *testing.T) {
		bad := discovery.LLMTool{
			Name:         "bad",
			Path:         "/tmp/bad",
			Available:    true,
			Capabilities: []discovery.Capability{discovery.CapabilityGenerate, discovery.Capability("bogus")},
		}
		req := DefaultRequest([]discovery.LLMTool{bad, claude})
		_, err := Route(req)
		if err == nil {
			t.Fatal("expected error for malformed provider metadata")
		}
		if !strings.Contains(err.Error(), "unsupported capability") {
			t.Fatalf("error = %v, want malformed capability failure", err)
		}
	})
}

func provider(name string, caps ...discovery.Capability) discovery.LLMTool {
	return discovery.LLMTool{
		Name:         name,
		Path:         "/bin/" + name,
		Available:    true,
		Capabilities: append([]discovery.Capability(nil), caps...),
	}
}

func assertRoleProvider(t *testing.T, plan Plan, role Role, providerName string) {
	t.Helper()
	assignment, ok := plan.Assignments[role]
	if !ok {
		t.Fatalf("missing assignment for role %q", role)
	}
	if assignment.Provider.Name != providerName {
		t.Fatalf("role %q provider = %q, want %q", role, assignment.Provider.Name, providerName)
	}
}
