// mappers.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// IR type mappers.
// Converts between discovery, routing, and ingestion types and their
// corresponding contract types for IR persistence. Handles provider ID
// generation, source kind classification, evidence kind mapping, execution
// role assignment, routing policy detection, and tool language normalization.


package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/op7ic/swarmmaker/internal/contracts"
	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/ingestion"
	"github.com/op7ic/swarmmaker/internal/textutil"
)

func providerID(name string) string {
	return stableID("provider", name)
}

func evidenceID(n int) string {
	return fmt.Sprintf("evidence-%04d", n)
}

func stableID(prefix string, parts ...string) string {
	joined := strings.Join(parts, "-")
	var b strings.Builder
	for _, r := range strings.ToLower(joined) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		default:
			if b.Len() > 0 {
				b.WriteByte('-')
			}
		}
	}
	slug := strings.Trim(b.String(), "-_")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	if slug == "" {
		slug = shortDigest(strings.Join(parts, "|"))
	}
	return prefix + "-" + slug
}

func digestString(value string) string {
	return textutil.DigestString(value)
}

func digestBytes(value []byte) string {
	return textutil.DigestBytes(value)
}

func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func sourceKind(file ingestion.FileEntry) contracts.SourceKind {
	switch file.FileType {
	case "markdown", "text":
		return contracts.SourceKindNotes
	case "code":
		return contracts.SourceKindCode
	case "data":
		return contracts.SourceKindData
	default:
		return contracts.SourceKindUnknown
	}
}

func evidencePath(entry ingestion.EvidenceEntry) string {
	if strings.TrimSpace(entry.RelPath) != "" {
		return entry.RelPath
	}
	return entry.AbsPath
}

func evidenceKind(category ingestion.EvidenceCategory) contracts.EvidenceKind {
	switch category {
	case ingestion.EvidenceCategoryBinaryFile:
		return contracts.EvidenceKindBinaryFile
	case ingestion.EvidenceCategoryUnreadableFile, ingestion.EvidenceCategoryMixedEncoding:
		return contracts.EvidenceKindUnreadableFile
	default:
		return contracts.EvidenceKindSkippedPath
	}
}

func evidenceStatus(category ingestion.EvidenceCategory) contracts.EvidenceStatus {
	switch category {
	case ingestion.EvidenceCategoryUnreadableFile, ingestion.EvidenceCategoryMixedEncoding:
		return contracts.EvidenceStatusRejected
	default:
		return contracts.EvidenceStatusSkipped
	}
}

func evidenceDigestSeed(entry ingestion.EvidenceEntry, path string) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%s",
		entry.Phase, entry.Category, path, entry.Detail, entry.Size,
		entry.BytesRead, entry.BytesShown, entry.Err)
}

func binaryName(tool discovery.LLMTool) string {
	if strings.TrimSpace(tool.Path) == "" {
		return tool.Name
	}
	return filepath.Base(tool.Path)
}

func executionRoles(capabilities []discovery.Capability) []contracts.ExecutionRole {
	seen := make(map[contracts.ExecutionRole]struct{}, len(capabilities))
	roles := make([]contracts.ExecutionRole, 0, len(capabilities))
	for _, capability := range capabilities {
		role, ok := executionRole(capability)
		if !ok {
			continue
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	return roles
}

func executionRole(capability discovery.Capability) (contracts.ExecutionRole, bool) {
	switch capability {
	case discovery.CapabilityGenerate:
		return contracts.ExecutionRoleGenerator, true
	case discovery.CapabilityCritique:
		return contracts.ExecutionRoleCritic, true
	case discovery.CapabilityRenderOutput:
		return contracts.ExecutionRoleOutputRenderer, true
	case discovery.CapabilityBuildTools:
		return contracts.ExecutionRoleToolBuilder, true
	default:
		return "", false
	}
}

func routingPolicy(generatorID, criticID string, outputIDs []string) contracts.RoutingPolicy {
	if len(outputIDs) == 1 && generatorID == criticID && generatorID == outputIDs[0] {
		return contracts.RoutingPolicySingleModel
	}
	if generatorID == criticID {
		return contracts.RoutingPolicySameModelCritique
	}
	return contracts.RoutingPolicyCapabilityBased
}

func candidateProviderIDs(tools []discovery.LLMTool) []string {
	ids := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Available {
			ids = append(ids, providerID(tool.Name))
		}
	}
	sort.Strings(ids)
	return ids
}

func routingReason(input ArtifactInput, spec contracts.OutputTreeSpec) string {
	formats := make([]string, 0, len(spec.Targets))
	for _, target := range spec.Targets {
		formats = append(formats, string(target.Format))
	}
	sort.Strings(formats)
	return fmt.Sprintf(
		"generator=%s critic=%s output_formats=%s; output renderers are internal and do not require the target provider CLIs to be installed",
		input.Generator.Name, input.Critic.Name, strings.Join(formats, ","),
	)
}

func fallbackEvents(events []string) []contracts.FallbackEvent {
	out := make([]contracts.FallbackEvent, 0, len(events))
	for i, event := range events {
		reason := strings.TrimSpace(event)
		if reason == "" {
			continue
		}
		out = append(out, contracts.FallbackEvent{
			SchemaVersion:       contracts.SchemaVersionV1,
			FallbackID:          fmt.Sprintf("fallback-%04d", i+1),
			Category:            fallbackCategory(reason),
			AuthorizationStatus: contracts.FallbackAuthorizationAuthorized,
			AuthorizedBy:        "swarm-me-cli-routing",
			SourceRole:          contracts.ExecutionRoleCritic,
			Reason:              reason,
			UserVisibleEffect:   reason,
		})
	}
	return out
}

func fallbackCategory(reason string) contracts.FallbackCategory {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "same-model"):
		return contracts.FallbackCategoryCriticUnavailable
	case strings.Contains(lower, "critic"):
		return contracts.FallbackCategoryProviderUnavailable
	default:
		return contracts.FallbackCategoryValidationRetry
	}
}

func toolLanguage(languages []string) contracts.ToolLanguage {
	seen := make(map[contracts.ToolLanguage]struct{}, len(languages))
	for _, language := range languages {
		switch strings.ToLower(strings.TrimSpace(language)) {
		case "go":
			seen[contracts.ToolLanguageGo] = struct{}{}
		case "python":
			seen[contracts.ToolLanguagePython] = struct{}{}
		case "shell", "sh", "bash":
			seen[contracts.ToolLanguageShell] = struct{}{}
		}
	}
	if len(seen) != 1 {
		return contracts.ToolLanguageUnknown
	}
	for language := range seen {
		return language
	}
	return contracts.ToolLanguageUnknown
}
