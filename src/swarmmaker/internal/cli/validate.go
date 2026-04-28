// validate.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Runtime validation subcommand for swarm-maker.
// Loads an installed skill bundle, sends smoke-test prompts to the target
// LLM CLI, and verifies that skills are discoverable and triggerable.


package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/op7ic/swarmmaker/internal/discovery"
	"github.com/op7ic/swarmmaker/internal/executor"
)

var (
	validateBundleDir string
	validateTarget    string
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate an installed skill bundle against the target LLM CLI",
	Long: strings.TrimSpace(`
Validate sends smoke-test prompts to the target LLM CLI to verify that an
installed skill bundle is discoverable and triggerable.

It reads skill names and descriptions from the bundle's .agents/skills/
directory, then asks the target LLM to list and trigger each skill.
`),
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().StringVar(&validateBundleDir, "bundle", "", "Path to the bundle output directory (required)")
	validateCmd.Flags().StringVar(&validateTarget, "target", "", "Which LLM CLI to test against: claude, codex, gemini (required)")
	_ = validateCmd.MarkFlagRequired("bundle")
	_ = validateCmd.MarkFlagRequired("target")
	rootCmd.AddCommand(validateCmd)
}

// skillInfo holds extracted frontmatter from a SKILL.md file.
type skillInfo struct {
	Slug        string
	Name        string
	Description string
}

// validateTimeout is the per-call timeout for validation LLM calls.
const validateTimeout = 30 * time.Second

func runValidate(cmd *cobra.Command, args []string) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	yellow := color.New(color.FgYellow)

	// 1. Read skills from the bundle.
	skills, err := readBundleSkills(validateBundleDir)
	if err != nil {
		return fmt.Errorf("reading bundle skills: %w", err)
	}
	if len(skills) == 0 {
		return fmt.Errorf("no skills found in bundle directory %s", validateBundleDir)
	}

	bold.Printf("Found %d skill(s) in bundle %s\n", len(skills), validateBundleDir)
	fmt.Println()

	// 2. Discover the target LLM CLI.
	targetTool, err := findTargetTool(validateTarget)
	if err != nil {
		return err
	}

	bold.Printf("Using %s (%s)\n\n", targetTool.Name, targetTool.Path)

	// 3. Set up executor for smoke-test calls.
	exec := executor.New(targetTool, targetTool, false)
	exec.Timeout = validateTimeout
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Ctx = ctx

	if err := exec.SetStagingDir(validateBundleDir); err != nil {
		return fmt.Errorf("setting up staging dir: %w", err)
	}
	defer exec.Cleanup()

	// 4. Build skill catalog to inject into prompts so the LLM has context.
	// Without this, the LLM has no way to know what skills exist — it would
	// be guessing blind. The catalog lists every skill with its slug,
	// description, and trigger conditions.
	catalog := buildSkillCatalog(skills)

	bold.Println("Phase 1: Skill discovery")
	fmt.Printf("  Injecting %d skill definitions into discovery prompt...\n", len(skills))
	discoveryPrompt := fmt.Sprintf(
		"You have the following skills installed:\n\n%s\n\n"+
			"List every installed skill by its exact slug name and give a one-sentence description of what each does. "+
			"Use the slug names exactly as shown above.",
		catalog)
	discoveryResp, err := exec.RunPreFlight(discoveryPrompt)
	discoveryOutput := ""
	if discoveryResp != nil {
		discoveryOutput = discoveryResp.Output
	}
	if err != nil && discoveryOutput == "" {
		yellow.Printf("  Discovery prompt failed: %v\n", err)
	}

	// 5. Check which skills appear in the discovery response.
	type skillResult struct {
		Slug      string
		Found     bool
		Triggered bool
	}
	results := make([]skillResult, len(skills))
	for i, s := range skills {
		results[i].Slug = s.Slug
		if discoveryOutput != "" {
			results[i].Found = containsSkillName(discoveryOutput, s.Slug, s.Name)
		}
	}

	fmt.Println()
	bold.Println("Phase 2: Skill triggering")

	// 6. For each skill, send a trigger prompt with the full skill catalog.
	// The LLM must pick the correct skill from the catalog given a use-case.
	for i, s := range skills {
		triggerDesc := s.Description
		if triggerDesc == "" {
			triggerDesc = s.Name
		}
		triggerPrompt := fmt.Sprintf(
			"You have the following skills installed:\n\n%s\n\n"+
				"A user says: \"I need to %s.\"\n\n"+
				"Which single skill slug from the list above best matches this request? "+
				"Reply with ONLY the slug name, nothing else. Example: my-skill-slug",
			catalog, strings.ToLower(triggerDesc))
		triggerResp, triggerErr := exec.RunPreFlight(triggerPrompt)
		triggerOutput := ""
		if triggerResp != nil {
			triggerOutput = triggerResp.Output
		}
		if triggerErr != nil && triggerOutput == "" {
			yellow.Printf("  Trigger prompt for %s failed: %v\n", s.Slug, triggerErr)
			continue
		}
		results[i].Triggered = containsSkillName(triggerOutput, s.Slug, s.Name)
	}

	// 7. Print report.
	fmt.Println()
	bold.Println("Skill Validation Report")
	validated := 0
	for _, r := range results {
		foundStr := "FOUND"
		trigStr := "TRIGGERED"
		mark := " [PASS]"
		printer := green

		if !r.Found {
			foundStr = "NOT FOUND"
		}
		if !r.Triggered {
			trigStr = "NOT TRIGGERED"
		}
		if r.Found && r.Triggered {
			validated++
		} else {
			mark = " [FAIL]"
			printer = red
		}

		printer.Printf("  %s %s: %s in skill list, %s on test prompt\n",
			mark, r.Slug, foundStr, trigStr)
	}

	fmt.Println()
	if validated == len(results) {
		green.Printf("Result: %d/%d skills validated\n", validated, len(results))
	} else {
		yellow.Printf("Result: %d/%d skills validated\n", validated, len(results))
	}

	return nil
}

// readBundleSkills reads SKILL.md files from <bundleDir>/.agents/skills/*/.
func readBundleSkills(bundleDir string) ([]skillInfo, error) {
	skillsDir := filepath.Join(bundleDir, ".agents", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("skills directory does not exist: %s", skillsDir)
		}
		return nil, fmt.Errorf("reading skills directory: %w", err)
	}

	var skills []skillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		skillFile := filepath.Join(skillsDir, slug, "SKILL.md")
		content, err := os.ReadFile(skillFile)
		if err != nil {
			continue // skip directories without SKILL.md
		}
		info := parseSkillFrontmatter(slug, string(content))
		skills = append(skills, info)
	}
	return skills, nil
}

var frontmatterNameLine = regexp.MustCompile(`(?mi)^name:\s*(.+?)\s*$`)
var frontmatterDescLine = regexp.MustCompile(`(?mi)^description:\s*(.+?)\s*$`)

// parseSkillFrontmatter extracts name and description from a SKILL.md file's
// YAML-like frontmatter block (delimited by ---). Handles both inline values
// (description: some text) and YAML folded scalars (description: >-\n  text).
func parseSkillFrontmatter(slug, content string) skillInfo {
	info := skillInfo{Slug: slug, Name: slug}

	// Try to extract from frontmatter block
	fmContent := content
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		trimmed := strings.TrimSpace(content)
		// Find the closing ---
		rest := trimmed[3:]
		if idx := strings.Index(rest, "---"); idx >= 0 {
			fmContent = rest[:idx]
		}
	}

	if m := frontmatterNameLine.FindStringSubmatch(fmContent); len(m) >= 2 {
		info.Name = strings.TrimSpace(m[1])
	}

	// Try inline description first
	if m := frontmatterDescLine.FindStringSubmatch(fmContent); len(m) >= 2 {
		desc := strings.TrimSpace(m[1])
		if desc != "" && desc != ">-" && desc != ">" && desc != "|" {
			info.Description = desc
		}
	}

	// If description is empty or was a folded scalar marker, parse the
	// indented continuation lines that follow "description: >-"
	if info.Description == "" {
		info.Description = parseFoldedDescription(fmContent)
	}

	return info
}

// parseFoldedDescription extracts a YAML folded scalar (>- or >) description.
// Collects indented lines after the "description:" key until a non-indented
// line or end of frontmatter.
func parseFoldedDescription(fmContent string) string {
	lines := strings.Split(fmContent, "\n")
	inDesc := false
	var descParts []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description:") {
			// Check if it's a folded scalar marker
			after := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			if after == ">-" || after == ">" || after == "|" || after == "|-" {
				inDesc = true
				continue
			}
			// Inline value — already handled by regex
			continue
		}
		if inDesc {
			// Continuation lines are indented (2+ spaces)
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && trimmed != "" {
				descParts = append(descParts, trimmed)
			} else if trimmed == "" {
				// Blank line within folded scalar — continue
				continue
			} else {
				// Non-indented, non-empty line — end of description
				break
			}
		}
	}
	return strings.Join(descParts, " ")
}

// containsSkillName checks if the LLM output references a skill by slug or name.
func containsSkillName(output, slug, name string) bool {
	lower := strings.ToLower(output)
	if strings.Contains(lower, strings.ToLower(slug)) {
		return true
	}
	if name != slug && strings.Contains(lower, strings.ToLower(name)) {
		return true
	}
	return false
}

func truncateOutput(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// buildSkillCatalog formats all skills into a text block that can be injected
// into LLM prompts. Each skill is listed with its slug, name, and description
// so the LLM has the full context needed for discovery and triggering.
func buildSkillCatalog(skills []skillInfo) string {
	var b strings.Builder
	for i, s := range skills {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("- **%s**", s.Slug))
		if s.Name != "" && s.Name != s.Slug {
			b.WriteString(fmt.Sprintf(" (%s)", s.Name))
		}
		if s.Description != "" {
			b.WriteString(fmt.Sprintf(": %s", s.Description))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// findTargetTool locates the requested LLM CLI tool.
func findTargetTool(target string) (discovery.LLMTool, error) {
	normalized := strings.ToLower(strings.TrimSpace(target))
	if !discovery.IsKnownToolName(normalized) {
		return discovery.LLMTool{}, fmt.Errorf("unsupported target %q: use claude, codex, or gemini", target)
	}

	tools := discovery.FindAllLLMs()
	for _, tool := range tools {
		if tool.Name == normalized {
			if !tool.Available {
				return discovery.LLMTool{}, fmt.Errorf("target %q is not installed or not in PATH", normalized)
			}
			return tool, nil
		}
	}
	return discovery.LLMTool{}, fmt.Errorf("target %q not found", target)
}
