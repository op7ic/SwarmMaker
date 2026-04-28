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

	// 4. Send discovery prompt: ask the LLM to list all skills.
	skillNames := make([]string, 0, len(skills))
	for _, s := range skills {
		skillNames = append(skillNames, s.Slug)
	}

	bold.Println("Phase 1: Skill discovery")
	discoveryPrompt := "You have skill files installed. List all available skills by name and briefly describe what each does."
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

	// 6. For each skill, send a trigger prompt.
	for i, s := range skills {
		triggerDesc := s.Description
		if triggerDesc == "" {
			triggerDesc = s.Name
		}
		triggerPrompt := fmt.Sprintf("I need to %s. What skill would you use?", strings.ToLower(triggerDesc))
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
// YAML-like frontmatter block (delimited by ---).
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
	if m := frontmatterDescLine.FindStringSubmatch(fmContent); len(m) >= 2 {
		info.Description = strings.TrimSpace(m[1])
	}
	return info
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
