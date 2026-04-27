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
			"- `.tasks/skills.md` is compiler input for the final model-specific bundle.",
			"- Return only repeated `## Skill:` blocks. Each block must contain these sections in order:",
			"  - Slug: (kebab-case, unique, renderer-safe)",
			"  - Summary: (one sentence describing what the skill does)",
			"  - When to Invoke: (specific trigger conditions -- when does this skill activate? what preconditions must be met? list 3-5 concrete triggers derived from source material)",
			"  - Inputs Required: (specific data, formats, and preconditions -- not generic references to .tasks/ files. Name the actual data structures, fields, and states needed)",
			"  - Process: (numbered step-by-step procedure. Each step must be concrete enough for an engineer to implement without access to source material. Include specific thresholds, formulas, field names, and decision rules from source. Typically 5-15 steps.)",
			"  - Output Format: (what the skill produces -- name the data structure, its fields, and their types/constraints)",
			"  - Constraints: (split into MUST DO and MUST NOT sections. Derive from source material constraints, non-functional requirements, and security boundaries. Typically 3-8 rules each.)",
			"  - UNKNOWN Gates: (what blocks this skill from full execution -- missing data, unresolved decisions, unspecified formulas)",
			"  - Evidence: (what must be provably true before claiming this skill executed successfully)",
			"- Every slug must be unique. Do not emit duplicate or placeholder slugs.",
			"- The skill set must be the minimum complete set required by source material.",
			"- Do not invent catch-all filler skills.",
			"- Process steps must reference specific values from source (thresholds, field names, API endpoints, rules) -- not generic descriptions.",
			"- If the source material does not contain enough detail for a complete Process section, include the steps that ARE supported and mark remaining steps as UNKNOWN with the specific gap.",
			"",
		)
	case DraftAgents:
		lines = append(lines,
			"AGENT COMPILER CONTRACT:",
			"- `.tasks/agents.md` is compiler input for the final model-specific bundle.",
			"- Return only repeated `## Agent:` blocks with exactly one `Role:` line per agent.",
			"- Every agent name must be unique and every role must be explicit.",
			"- The agent set must collectively cover Observe, Orient, Decide, and Act responsibilities.",
			"- Multiple agents MAY share the same OODA role when the domain requires distinct execution responsibilities within that phase.",
			"- Each agent block must contain these sections:",
			"  - Role: (Observe | Orient | Decide | Act or source-backed specialized role)",
			"  - Owned Skills: (list the skill slugs this agent executes, by slug name from skills.md)",
			"  - Coordination Protocol: (how this agent receives work and delivers results -- name the upstream and downstream agents, the data shape exchanged, and the handoff trigger)",
			"  - Error Handling: (what happens when this agent's phase fails -- retry policy, fallback behavior, escalation path. Derive from source material failure modes.)",
			"  - Validation Gates: (what must pass before this agent hands off to the next -- specific checks, not generic statements)",
			"  - UNKNOWN Gates: (unresolved dependencies that block full execution)",
			"- The total agent count should be the minimum needed to cover all source-backed responsibilities. Do not split artificially to pad the count, and do not merge artificially to force exactly 4 agents.",
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
