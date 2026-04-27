// validate.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tool synthesis plan validation.
// Validates deterministic ordering, decision-specific rules (mandatory tools
// must have evidence, forbidden tools cannot be generated, unknown tools must
// keep UNKNOWN language), and path normalization (no backslashes, no absolute
// paths, no directory escapes).


package toolsynthesis

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// ValidatePlan enforces proof-of-use, consumer-path, and deterministic-order rules.
func ValidatePlan(plan Plan) error {
	ordered := append([]ToolPlan(nil), plan.Tools...)
	for i, tool := range ordered {
		if err := validateToolPlan(tool); err != nil {
			return fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		if i > 0 && ordered[i-1].Name > tool.Name {
			return fmt.Errorf("tool plans are not deterministically ordered")
		}
	}
	return nil
}

func validateToolPlan(tool ToolPlan) error {
	if strings.TrimSpace(tool.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if err := validateRelativePaths(tool.ConsumerPaths); err != nil {
		return fmt.Errorf("consumer paths: %w", err)
	}
	if err := validateRelativePaths(tool.EvidencePaths); err != nil {
		return fmt.Errorf("evidence paths: %w", err)
	}
	switch tool.Decision {
	case DecisionMandatory:
		if !tool.Generated {
			return fmt.Errorf("mandatory tools must be marked generated")
		}
		if tool.Language == LanguageUnknown {
			return fmt.Errorf("mandatory tools cannot keep UNKNOWN language")
		}
		if len(tool.ConsumerPaths) == 0 {
			return fmt.Errorf("mandatory tools require at least one consumer path")
		}
		if len(tool.EvidencePaths) == 0 {
			return fmt.Errorf("mandatory tools require proof-of-use evidence")
		}
	case DecisionForbidden:
		if tool.Generated {
			return fmt.Errorf("forbidden tools cannot be generated")
		}
	case DecisionUnknown:
		if tool.Generated {
			return fmt.Errorf("unknown decision cannot produce generated tools")
		}
		if tool.Language != LanguageUnknown {
			return fmt.Errorf("unknown decision must keep UNKNOWN language")
		}
	default:
		return fmt.Errorf("unknown decision %q", tool.Decision)
	}
	return nil
}

func validateRelativePaths(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized, err := normalizeRelativePath(value)
		if err != nil {
			return err
		}
		if _, ok := seen[normalized]; ok {
			return fmt.Errorf("duplicate path %q", normalized)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func normalizePaths(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeRelativePath(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeRelativePath(rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.Contains(rel, "\\") {
		return "", fmt.Errorf("path must use forward slashes")
	}
	if path.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned == "/" {
		return "", fmt.Errorf("path escapes tree root")
	}
	return cleaned, nil
}

// StablePlanNames returns the names in deterministic order for testing/reporting.
func StablePlanNames(plan Plan) []string {
	names := make([]string, 0, len(plan.Tools))
	for _, tool := range plan.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}
