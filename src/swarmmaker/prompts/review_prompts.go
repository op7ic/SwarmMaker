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

func promptFileBlocks(files []PromptFileSnapshot) string {
	var b strings.Builder
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
		b.WriteString(file.Content)
		if !strings.HasSuffix(file.Content, "\n") {
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
