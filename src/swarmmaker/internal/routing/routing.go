// routing.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Deterministic provider routing.
// Routes generation, critique, rendering, and tool-building roles to available
// LLM providers based on user preferences and provider capabilities. Implements
// explicit fallback accounting: when only one provider is available and must
// serve both generator and critic roles, the fallback is counted and recorded
// in the routing decision artifact.


// Package routing implements deterministic, capability-based provider routing
// for four swarm roles (generator, critic, output_renderer, tool_builder).
//
// NOTE: The CLI currently uses cli.selectLLMs for provider selection because it
// only needs two roles (primary/critic) and relies on cobra flags rather than
// the capability metadata that Route requires. Route is the intended long-term
// replacement once the CLI adopts capability-aware discovery.
package routing

import (
	"errors"
	"fmt"

	"github.com/op7ic/swarmmaker/internal/discovery"
)

// Role identifies the swarm responsibility assigned to a provider.
type Role string

const (
	RoleGenerator      Role = "generator"
	RoleCritic         Role = "critic"
	RoleOutputRenderer Role = "output_renderer"
	RoleToolBuilder    Role = "tool_builder"
)

// Request captures the desired routing policy for a single plan.
type Request struct {
	Providers []discovery.LLMTool

	// Requested maps a role to an explicit provider name. If set, that provider
	// must exist and support the role capability or routing fails.
	Requested map[Role]string

	// Explicit fallback policy. When disabled for a role, routing fails instead
	// of reusing the generator as a same-model fallback.
	AllowSameModelCritic         bool
	AllowSameModelOutputRenderer bool
	AllowSameModelToolBuilder    bool
}

// DefaultRequest enables the conservative same-provider reuse policy for callers
// that explicitly opt into automatic fallback accounting.
func DefaultRequest(providers []discovery.LLMTool) Request {
	return Request{
		Providers:                    providers,
		AllowSameModelCritic:         true,
		AllowSameModelOutputRenderer: true,
		AllowSameModelToolBuilder:    true,
	}
}

// Assignment describes the provider selected for a role.
type Assignment struct {
	Role           Role
	Provider       discovery.LLMTool
	Fallback       bool
	FallbackReason string
}

// FallbackEvent records a fallback that was explicitly used.
type FallbackEvent struct {
	Role     Role
	Provider string
	Reason   string
}

// Plan is the deterministic routing result for a request.
type Plan struct {
	Assignments map[Role]Assignment
	Fallbacks   []FallbackEvent
	Issues      []error
}

// FallbackCount returns the number of explicit fallback events.
func (p Plan) FallbackCount() int {
	return len(p.Fallbacks)
}

// Route selects providers for the four core swarm roles.
func Route(req Request) (Plan, error) {
	plan := Plan{
		Assignments: make(map[Role]Assignment),
	}

	validProviders, issues := validateProviders(req.Providers)
	plan.Issues = append(plan.Issues, issues...)

	if len(validProviders) == 0 {
		plan.Issues = append(plan.Issues, errors.New("no valid providers available"))
		return plan, errors.Join(plan.Issues...)
	}

	used := make(map[string]struct{})

	if assignment, issue := selectGenerator(validProviders, req, used); issue != nil {
		plan.Issues = append(plan.Issues, issue)
	} else {
		plan.Assignments[RoleGenerator] = assignment
		used[assignment.Provider.Name] = struct{}{}
	}

	for _, role := range []Role{RoleCritic, RoleOutputRenderer, RoleToolBuilder} {
		assignment, issue := selectRole(role, validProviders, req, used, plan.Assignments[RoleGenerator])
		if issue != nil {
			plan.Issues = append(plan.Issues, issue)
			continue
		}
		plan.Assignments[role] = assignment
		used[assignment.Provider.Name] = struct{}{}
		if assignment.Fallback {
			plan.Fallbacks = append(plan.Fallbacks, FallbackEvent{
				Role:     role,
				Provider: assignment.Provider.Name,
				Reason:   assignment.FallbackReason,
			})
		}
	}

	if len(plan.Issues) > 0 {
		return plan, errors.Join(plan.Issues...)
	}

	return plan, nil
}

func validateProviders(providers []discovery.LLMTool) ([]discovery.LLMTool, []error) {
	valid := make([]discovery.LLMTool, 0, len(providers))
	var issues []error

	for _, provider := range providers {
		if err := provider.ValidateMetadata(); err != nil {
			issues = append(issues, fmt.Errorf("provider %q metadata invalid: %w", provider.Name, err))
			continue
		}
		valid = append(valid, provider)
	}

	return valid, issues
}

func selectGenerator(providers []discovery.LLMTool, req Request, used map[string]struct{}) (Assignment, error) {
	if requested, ok := req.Requested[RoleGenerator]; ok && requested != "" {
		provider, ok := findProviderByName(providers, requested)
		if !ok {
			return Assignment{}, fmt.Errorf("generator requested provider %q is unavailable", requested)
		}
		if !provider.Supports(discovery.CapabilityGenerate) {
			return Assignment{}, fmt.Errorf("generator provider %q does not support generate", requested)
		}
		return Assignment{
			Role:     RoleGenerator,
			Provider: provider,
		}, nil
	}

	provider, ok := firstEligibleProvider(providers, discovery.CapabilityGenerate, used)
	if !ok {
		return Assignment{}, errors.New("no provider supports generator role")
	}

	return Assignment{
		Role:     RoleGenerator,
		Provider: provider,
	}, nil
}

func selectRole(role Role, providers []discovery.LLMTool, req Request, used map[string]struct{}, generator Assignment) (Assignment, error) {
	capability, err := capabilityForRole(role)
	if err != nil {
		return Assignment{}, err
	}

	if requested, ok := req.Requested[role]; ok && requested != "" {
		provider, ok := findProviderByName(providers, requested)
		if !ok {
			return Assignment{}, fmt.Errorf("%s requested provider %q is unavailable", role, requested)
		}
		if !provider.Supports(capability) {
			return Assignment{}, fmt.Errorf("%s provider %q does not support %s", role, requested, capability)
		}
		return Assignment{
			Role:     role,
			Provider: provider,
		}, nil
	}

	if provider, ok := firstEligibleProvider(providers, capability, used); ok {
		return Assignment{
			Role:     role,
			Provider: provider,
		}, nil
	}

	if generator.Provider.Name == "" {
		return Assignment{}, fmt.Errorf("%s requires a generator assignment before fallback can be used", role)
	}

	if !allowSameModelFallback(role, req) {
		return Assignment{}, fmt.Errorf("%s requires a distinct provider and no eligible provider was available", role)
	}

	return Assignment{
		Role:           role,
		Provider:       generator.Provider,
		Fallback:       true,
		FallbackReason: fmt.Sprintf("reused generator provider %q for %s", generator.Provider.Name, role),
	}, nil
}

func allowSameModelFallback(role Role, req Request) bool {
	switch role {
	case RoleCritic:
		return req.AllowSameModelCritic
	case RoleOutputRenderer:
		return req.AllowSameModelOutputRenderer
	case RoleToolBuilder:
		return req.AllowSameModelToolBuilder
	default:
		return false
	}
}

func capabilityForRole(role Role) (discovery.Capability, error) {
	switch role {
	case RoleGenerator:
		return discovery.CapabilityGenerate, nil
	case RoleCritic:
		return discovery.CapabilityCritique, nil
	case RoleOutputRenderer:
		return discovery.CapabilityRenderOutput, nil
	case RoleToolBuilder:
		return discovery.CapabilityBuildTools, nil
	default:
		return "", fmt.Errorf("unsupported role %q", role)
	}
}

func firstEligibleProvider(providers []discovery.LLMTool, capability discovery.Capability, used map[string]struct{}) (discovery.LLMTool, bool) {
	for _, provider := range providers {
		if _, ok := used[provider.Name]; ok {
			continue
		}
		if provider.Supports(capability) {
			return provider, true
		}
	}
	return discovery.LLMTool{}, false
}

func findProviderByName(providers []discovery.LLMTool, name string) (discovery.LLMTool, bool) {
	for _, provider := range providers {
		if provider.Name == name {
			return provider, true
		}
	}
	return discovery.LLMTool{}, false
}
