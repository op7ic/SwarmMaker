// review_prompts.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Adversarial review and revision prompt assembly.
// Builds the adversarial review prompt from file snapshots and pre-screen
// findings, and the targeted revision prompt for specific flagged files.
// The review prompt includes the full content of all generated files so the
// critic can check cross-file consistency, citation integrity, and source
// fidelity in one pass.


package prompts

import (
	"fmt"
	"strings"
)

// RevisionPrompt constructs a targeted revision prompt for a .tasks file that
// failed validation or adversarial review.
func RevisionPrompt(ir PromptIR, file PromptFileSnapshot, criticFindings string) (string, error) {
	pack, err := DefaultPack()
	if err != nil {
		return "", err
	}
	return RevisionPromptWithPack(ir, pack, file, criticFindings)
}

func RevisionPromptWithPack(ir PromptIR, pack Pack, file PromptFileSnapshot, criticFindings string) (string, error) {
	if err := ir.Validate(); err != nil {
		return "", err
	}
	if err := pack.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(file.AbsPath) == "" {
		return "", fmt.Errorf("generated file path is required")
	}
	if strings.TrimSpace(file.RelPath) == "" {
		return "", fmt.Errorf("file name is required")
	}
	if strings.TrimSpace(file.Content) == "" {
		return "", fmt.Errorf("generated file content is required")
	}
	if strings.TrimSpace(criticFindings) == "" {
		return "", fmt.Errorf("critic findings are required")
	}
	data := newPromptTemplateData(ir)
	data.GeneratedFilePath = file.AbsPath
	data.FileName = file.RelPath
	data.GeneratedFileContent = file.Content
	data.CriticFindings = criticFindings
	rendered, err := pack.Revision.render(data)
	if err != nil {
		return "", fmt.Errorf("render revision prompt: %w", err)
	}
	return promptHeader(ir, pack.Revision.Title, pack.Revision.Planning || strings.Contains(file.RelPath, "todo")) + artifactOutputContract(draftKindForPath(file.RelPath)) + revisionContract(file.RelPath) + rendered, nil
}

// AdversarialReviewPrompt asks the critic model for one strict review result.
func AdversarialReviewPrompt(ir PromptIR, allFiles []PromptFileSnapshot, flaggedFiles []string, preScreenFindings []string) (string, error) {
	pack, err := DefaultPack()
	if err != nil {
		return "", err
	}
	return AdversarialReviewPromptWithPack(ir, pack, allFiles, flaggedFiles, preScreenFindings)
}

func AdversarialReviewPromptWithPack(ir PromptIR, pack Pack, allFiles []PromptFileSnapshot, flaggedFiles []string, preScreenFindings []string) (string, error) {
	if err := ir.Validate(); err != nil {
		return "", err
	}
	if err := pack.Validate(); err != nil {
		return "", err
	}
	if len(flaggedFiles) == 0 {
		return "", fmt.Errorf("flagged files are required")
	}
	if len(allFiles) == 0 {
		return "", fmt.Errorf("all files are required")
	}
	data := newPromptTemplateData(ir)
	data.FlaggedFilesList = flaggedPathList(allFiles, flaggedFiles)
	data.AllFilesList = snapshotPathList(allFiles)
	data.AllFilesContent = promptFileBlocks(allFiles)
	data.PreScreenFindingsList = findingsList(preScreenFindings)
	rendered, err := pack.Review.render(data)
	if err != nil {
		return "", fmt.Errorf("render adversarial review prompt: %w", err)
	}
	return promptHeader(ir, pack.Review.Title, pack.Review.Planning) + reviewContract() + rendered, nil
}

func snapshotPathList(files []PromptFileSnapshot) string {
	var b strings.Builder
	for _, file := range files {
		b.WriteString("- ")
		b.WriteString(file.AbsPath)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func flaggedPathList(files []PromptFileSnapshot, flagged []string) string {
	index := make(map[string]PromptFileSnapshot, len(files))
	for _, file := range files {
		index[file.RelPath] = file
	}
	var b strings.Builder
	for _, rel := range flagged {
		if file, ok := index[rel]; ok {
			b.WriteString("- ")
			b.WriteString(file.AbsPath)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

const (
	maxPerFileChars  = 5000
	maxTotalFileChars = 30000
)

func promptFileBlocks(files []PromptFileSnapshot) string {
	var b strings.Builder
	totalChars := 0
	for _, file := range files {
		if strings.TrimSpace(file.RelPath) == "" || strings.TrimSpace(file.AbsPath) == "" {
			continue
		}
		b.WriteString("### File Snapshot: ")
		b.WriteString(file.RelPath)
		b.WriteString("\n")
		b.WriteString("Absolute path: ")
		b.WriteString(file.AbsPath)
		b.WriteString("\n")
		b.WriteString("```md\n")
		content := file.Content
		contentLen := len(content)
		if contentLen > maxPerFileChars {
			content = content[:maxPerFileChars]
			content += fmt.Sprintf("\n[TRUNCATED -- showing first %d of %d chars. Review full file at: %s]", maxPerFileChars, contentLen, file.AbsPath)
		}
		if totalChars+len(content) > maxTotalFileChars && totalChars > 0 {
			remaining := maxTotalFileChars - totalChars
			if remaining > 0 {
				content = content[:remaining]
				content += fmt.Sprintf("\n[TRUNCATED -- total review size cap reached. Review full file at: %s]", file.AbsPath)
			} else {
				content = fmt.Sprintf("[TRUNCATED -- total review size cap reached. Review full file at: %s]", file.AbsPath)
			}
		}
		totalChars += len(content)
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func findingsList(findings []string) string {
	if len(findings) == 0 {
		return "- None recorded. Still perform the full review."
	}
	var b strings.Builder
	for _, finding := range findings {
		b.WriteString("- ")
		b.WriteString(finding)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func reviewContract() string {
	return strings.Join([]string{
		"REVIEW CONTRACT:",
		"- This review prompt already contains the authoritative file snapshots. Do not try to read workspace files or use tools.",
		"- Treat prompt-pack heading names and artifact schema headings as allowed structure, not as fabricated domain facts.",
		"- Flag fabricated claims, unsupported counts, unsupported APIs/providers, bad citations, broken links, and status-note prose.",
		"- A file that talks about what was created instead of containing the .tasks artifact body is a hard failure.",
		"- Implementation decisions in planning artifacts are allowed when explicitly marked and when they do not invent source facts.",
		"- Treat `.tasks/skills.md` and `.tasks/agents.md` as compiler inputs. Hard-fail duplicate slugs, duplicate agent names, missing `Slug:`/`Summary:`/`Role:` metadata, empty blocks, or commentary outside repeated blocks.",
		"- Cross-check that `tasks.md`, `todo.md`, `skills.md`, and `agents.md` describe one coherent bundle. Missing handoffs, missing consumers, or decomposition that cannot be rendered into the selected platform subtree must trigger REVISE.",
		"- A skill or agent not justified by source material, PromptIR, or an explicit implementation decision is a hard failure.",
		"",
	}, "\n")
}

func revisionContract(relPath string) string {
	switch draftKindForPath(relPath) {
	case DraftSkills:
		return strings.Join([]string{
			"REVISION PARSEABILITY CONTRACT:",
			"- Preserve the repeated `## Skill:` block contract exactly.",
			"- Keep exactly one `Slug:` line and exactly one `Summary:` line per skill, with unique renderer-safe slugs.",
			"- Do not add preambles, summaries, or trailing commentary outside the skill blocks.",
			"- Fix decomposition drift so each skill remains renderable into the selected platform subtree.",
			"",
		}, "\n")
	case DraftAgents:
		return strings.Join([]string{
			"REVISION PARSEABILITY CONTRACT:",
			"- Preserve the repeated `## Agent:` block contract exactly.",
			"- Keep exactly one `Role:` line per agent, with unique agent names and explicit handoffs.",
			"- Do not add preambles, summaries, or trailing commentary outside the agent blocks.",
			"- Fix decomposition drift so the agent set still covers bundle routing and critique responsibilities.",
			"",
		}, "\n")
	default:
		return ""
	}
}

// HolisticRevisionFileDelimiter is the delimiter used to separate files in a
// holistic revision response. The LLM is instructed to use this exact format.
const HolisticRevisionFileDelimiter = "--- FILE: "

const maxHolisticPerFileChars = 3000

// BuildHolisticRevisionPrompt constructs a single prompt that asks the LLM to
// revise ALL flagged files at once, using file delimiters to separate output.
func BuildHolisticRevisionPrompt(ir PromptIR, pack Pack, flaggedFiles []PromptFileSnapshot, reviewFindings string, sourceHints string) (string, error) {
	if err := ir.Validate(); err != nil {
		return "", err
	}
	if err := pack.Validate(); err != nil {
		return "", err
	}
	if len(flaggedFiles) == 0 {
		return "", fmt.Errorf("flagged files are required for holistic revision")
	}
	if strings.TrimSpace(reviewFindings) == "" {
		return "", fmt.Errorf("review findings are required for holistic revision")
	}

	var b strings.Builder

	// Source hints (if any) go first
	if sourceHints != "" {
		b.WriteString(sourceHints)
	}

	// Prompt header
	b.WriteString(promptHeader(ir, pack.Revision.Title, pack.Revision.Planning))

	// Holistic revision contract
	b.WriteString("HOLISTIC REVISION CONTRACT:\n")
	b.WriteString("- You are revising ALL flagged files in one pass. This allows you to fix cross-file inconsistencies.\n")
	b.WriteString("- For each file, output a delimiter line followed by the complete revised content.\n")
	b.WriteString("- Delimiter format: --- FILE: <relative-path> ---\n")
	b.WriteString("- Example:\n")
	b.WriteString("  --- FILE: .tasks/skills.md ---\n")
	b.WriteString("  (complete revised content for skills.md)\n")
	b.WriteString("  --- FILE: .tasks/agents.md ---\n")
	b.WriteString("  (complete revised content for agents.md)\n")
	b.WriteString("- Output ONLY the delimiters and revised file contents. No preamble, no commentary outside of file sections.\n")
	b.WriteString("- Each file section must contain the COMPLETE revised markdown, not a diff or summary.\n\n")

	// Reviewer findings
	b.WriteString("REVIEWER FINDINGS:\n")
	b.WriteString(reviewFindings)
	b.WriteString("\n\n")

	// Flagged file snapshots
	b.WriteString("FILES TO REVISE:\n\n")
	for _, f := range flaggedFiles {
		b.WriteString("### Current Content: ")
		b.WriteString(f.RelPath)
		b.WriteString("\n")
		b.WriteString("Absolute path: ")
		b.WriteString(f.AbsPath)
		b.WriteString("\n")
		b.WriteString("```md\n")
		content := f.Content
		if len(content) > maxHolisticPerFileChars {
			content = content[:maxHolisticPerFileChars]
			content += fmt.Sprintf("\n[TRUNCATED -- showing first %d of %d chars]", maxHolisticPerFileChars, len(f.Content))
		}
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")

		// Add per-file revision contracts where applicable
		contract := revisionContract(f.RelPath)
		if contract != "" {
			b.WriteString(contract)
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

// ParseHolisticRevisionResponse parses a multi-file LLM response that uses
// "--- FILE: <path> ---" delimiters. Returns a map from relative path to
// revised content. Returns an error if no valid file sections are found.
func ParseHolisticRevisionResponse(response string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(response, "\n")
	var currentFile string
	var currentContent strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, HolisticRevisionFileDelimiter) {
			// Save previous file if any
			if currentFile != "" {
				result[currentFile] = strings.TrimSpace(currentContent.String())
			}
			// Parse new file path: "--- FILE: .tasks/skills.md ---"
			path := strings.TrimPrefix(line, HolisticRevisionFileDelimiter)
			path = strings.TrimSuffix(path, " ---")
			path = strings.TrimSuffix(path, "---")
			path = strings.TrimSpace(path)
			if path != "" {
				currentFile = path
				currentContent.Reset()
			}
		} else if currentFile != "" {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}

	// Save last file
	if currentFile != "" {
		result[currentFile] = strings.TrimSpace(currentContent.String())
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no file sections found in holistic revision response")
	}

	// Remove entries with empty content
	for path, content := range result {
		if strings.TrimSpace(content) == "" {
			delete(result, path)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("all file sections in holistic revision response were empty")
	}

	return result, nil
}

func draftKindForPath(relPath string) DraftKind {
	switch relPath {
	case ".tasks/context.md":
		return DraftContext
	case ".tasks/tasks.md":
		return DraftTasks
	case ".tasks/prompts/product.md":
		return DraftPromptProduct
	case ".tasks/prompts/technical.md":
		return DraftPromptTechnical
	case ".tasks/prompts/tools.md":
		return DraftPromptTools
	case ".tasks/prompts/deployment.md":
		return DraftPromptDeployment
	case ".tasks/todo.md":
		return DraftTodo
	case ".tasks/skills.md":
		return DraftSkills
	case ".tasks/agents.md":
		return DraftAgents
	default:
		return ""
	}
}
