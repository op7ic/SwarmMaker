// draft_prompts.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Draft prompt compiler for Stage 1 artifacts.
// Compiles the 9 draft prompts by combining the prompt header (from types.go),
// artifact-specific output contracts (skill compiler, agent compiler, cross-file
// consistency), and the rendered prompt pack body. Each compiled prompt is a
// self-contained instruction set that an LLM can execute to produce one .tasks/
// ledger file.


package prompts

import (
	"fmt"
	"strings"
)

// DraftKind identifies the stage-1 .tasks artifact being compiled.
type DraftKind string

const (
	DraftContext          DraftKind = "context"
	DraftTasks            DraftKind = "tasks"
	DraftPromptProduct    DraftKind = "prompt_product"
	DraftPromptTechnical  DraftKind = "prompt_technical"
	DraftPromptTools      DraftKind = "prompt_tools"
	DraftPromptDeployment DraftKind = "prompt_deployment"
	DraftTodo             DraftKind = "todo"
	DraftSkills           DraftKind = "skills"
	DraftAgents           DraftKind = "agents"
)

// CompileDraftPrompt returns a SwarmMaker-specific prompt for one .tasks artifact.
func CompileDraftPrompt(kind DraftKind, ir PromptIR) (string, error) {
	pack, err := DefaultPack()
	if err != nil {
		return "", err
	}
	return CompileDraftPromptWithPack(kind, ir, pack)
}

func CompileDraftPromptWithPack(kind DraftKind, ir PromptIR, pack Pack) (string, error) {
	if err := ir.Validate(); err != nil {
		return "", err
	}
	if err := pack.Validate(); err != nil {
		return "", err
	}
	body, ok := pack.Drafts[kind]
	if !ok {
		return "", fmt.Errorf("unsupported draft kind %q", kind)
	}
	rendered, err := body.render(newPromptTemplateData(ir))
	if err != nil {
		return "", fmt.Errorf("render draft prompt %q: %w", kind, err)
	}
	return promptHeader(ir, body.Title, body.Planning) + artifactOutputContract(kind) + rendered, nil
}

func promptHeader(ir PromptIR, title string, planning bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You are compiling %s for AI Swarm Maker.\n", title))
	b.WriteString("This is not a generic app plan. It is a stage-1 .tasks ledger artifact used to render a model-specific skill bundle.\n\n")
	b.WriteString(selfContainedExecutionContract())
	b.WriteString(ir.contextBlock())
	b.WriteString(citationContract(ir, planning))
	b.WriteString(sourceBlock(ir.SourceMaterial))
	b.WriteString(strictRules(planning))
	b.WriteString(concisenessContract())
	b.WriteString(outputOnlyMarkdown())
	return b.String()
}

func artifactOutputContract(kind DraftKind) string {
	lines := []string{
		"ARTIFACT OUTPUT CONTRACT:",
		"- Write the actual .tasks artifact body, not a status note, summary, changelog, or explanation of what you created.",
		"- Do not write phrases like `Created`, `Added`, `Rewrote`, `What changed`, `I did`, `I also`, `If you want`, or `No tests were needed`.",
		"- Required headings from the prompt are allowed artifact structure. Fill them with source-grounded content instead of commenting on them.",
		"- Do not use shell commands, apply_patch, or any other tool. Return the final markdown directly.",
		"- The caller writes your final response to the target file. Do not attempt to create sibling files or modify the workspace.",
		"- The file itself must be reviewable as the final artifact content with citations and decision gates included directly in the markdown.",
		"- The first non-empty line should be the document heading, not a progress sentence.",
		"- When citing local manifests or source files, use markdown links that resolve correctly from the generated file location or use the exact absolute paths provided in PromptIR/source context.",
		"- WRONG: `Created .tasks/tasks.md with the required sections.`",
		"- RIGHT: `# Product Prompt` followed by the actual sections and `Source:` citations inside the document body.",
		"",
	}
	// Add cross-file consistency contract for dependent kinds (not context or tasks).
	switch kind {
	case DraftContext, DraftTasks:
		// Foundational artifacts — no cross-file consistency block.
	default:
		lines = append(lines,
			"CROSS-FILE CONSISTENCY:",
			"- This .tasks artifact must be consistent with the other ledger files, especially .tasks/context.md and .tasks/tasks.md.",
			"- Do not invent CLI commands, binary names, tool names, or role structures not grounded in the source material.",
			"- If you reference agent roles, they must match roles that the source material justifies. Do not assume other .tasks files define roles you haven't seen.",
			"- If you reference skills, they must be derivable from the source material's capability requirements.",
			"- When the source is silent on a cross-file dependency, write UNKNOWN rather than inventing a plausible-looking fact.",
			"- Do not claim consistency with files you cannot see -- state what the source supports and let the renderer validate consistency.",
			"",
		)
	}
	switch kind {
	case DraftSkills:
		lines = append(lines,
			"SKILL COMPILER CONTRACT:",
			"- `.tasks/skills.md` is compiler input for the final model-specific bundle, not a brainstorming note.",
			"- Return only repeated `## Skill:` blocks with exactly one `Slug:` line and exactly one `Summary:` line per skill.",
			"- Every slug must be unique, kebab-case, and renderer-safe. Do not emit duplicate or placeholder slugs.",
			"- The skill set must be the minimum complete set required by the source, PromptIR, and explicit implementation decisions. Do not invent catch-all filler skills.",
			"- Every skill body must name consumers, coordination boundaries, required `.tasks` inputs, downstream render responsibilities, and any UNKNOWN gates that block completion.",
			"",
		)
	case DraftAgents:
		lines = append(lines,
			"AGENT COMPILER CONTRACT:",
			"- `.tasks/agents.md` is compiler input for the final model-specific bundle, not a brainstorming note.",
			"- Return only repeated `## Agent:` blocks with exactly one `Role:` line per agent.",
			"- Every agent name must be unique and every role must be explicit. Do not emit duplicate or placeholder agents.",
			"- The agent set must collectively cover Observe, Orient, Decide, and Act responsibilities unless a specialized role is explicitly justified by source-backed decomposition.",
		"- Multiple agents MAY share the same OODA role when the domain requires distinct execution responsibilities within that phase (e.g., two Act agents for separate execution concerns).",
		"- When multiple agents share a role, each must have a unique name, distinct owned responsibilities, and explicit handoff relationships.",
		"- The total agent count should be the minimum needed to cover all source-backed responsibilities. Do not split artificially to pad the count, and do not merge artificially to force exactly 4 agents.",
			"- Every agent body must name owned skills or ledger responsibilities, handoffs, critique/revision duties, required `.tasks` inputs, and any UNKNOWN gates that block completion.",
			"",
		)
	}
	return strings.Join(lines, "\n")
}

func requiredDraftKinds() []DraftKind {
	return []DraftKind{
		DraftContext,
		DraftTasks,
		DraftPromptProduct,
		DraftPromptTechnical,
		DraftPromptTools,
		DraftPromptDeployment,
		DraftTodo,
		DraftSkills,
		DraftAgents,
	}
}

type promptTemplateData struct {
	PromptIR
	ToolchainConventions  string
	FlaggedFilesList      string
	AllFilesList          string
	PreScreenFindingsList string
	GeneratedFilePath     string
	FileName              string
	GeneratedFileContent  string
	CriticFindings        string
	AllFilesContent       string
}

func newPromptTemplateData(ir PromptIR) promptTemplateData {
	return promptTemplateData{
		PromptIR:             ir,
		ToolchainConventions: toolchainConventions(ir.ToolLanguages),
	}
}
