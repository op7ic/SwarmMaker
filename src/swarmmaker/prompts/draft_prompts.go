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
	b.WriteString(constraintReminder())
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
			"  - When to Invoke: (3-5 specific trigger conditions derived from source. Not generic -- name the actual event, data state, or upstream output that activates this skill.)",
			"  - Inputs Required: (name every data structure, field, config, and precondition needed. Include field types and enums when the source specifies them. If the source has a JSON payload, table, or schema, embed it inline -- skills are the operational reference and must be self-contained.)",
			"  - Process: (numbered steps, typically 6-12. Each step must follow this pattern:)",
			"    1. State the action concretely, referencing specific field names, thresholds, or rules from source.",
			"    2. Include decision branches: 'If X then Y. If X fails, Z.' Not every step branches, but every step where the source implies a condition must show both paths.",
			"    3. Include failure handling: what happens when this step cannot complete? Retry, reject, escalate, or mark UNKNOWN.",
			"    4. For steps that produce intermediate state (e.g., classified alert, correlated group), mark as CHECKPOINT -- the agent should persist this state before proceeding so recovery can resume from here.",
			"    5. End with the citation to the source that justifies this step.",
			"  - Output Format: (name the data structure produced. List its fields with types. If the source provides an example payload, include it inline.)",
			"  - Constraints: (split into Required and Prohibited, with a brief rationale for each rule. Derive each rule from a specific source requirement. Every constraint must cite its source. Include non-functional requirements like latency budgets, precision targets, availability, and security boundaries.)",
			"  - UNKNOWN Gates: (for each gate: name the missing data, state which Process step(s) it blocks, and cite the source that implies the need but doesn't provide the answer.)",
			"  - Evidence: (testable acceptance criteria -- not 'the output should be correct' but specific assertions like 'every emitted record has a non-empty reasoning_chain' or 'if zero contradictions found, flag as suspicious'. Each criterion must be verifiable without human judgment.)",
			"",
			"SKILL GRANULARITY RULES:",
			"- A skill is ONE coherent capability that maps to a recognizable pipeline stage, component, or responsibility from the source material.",
			"- A skill is NOT a single function call, lookup, or config load -- those are steps within a skill.",
			"- A skill is NOT an entire pipeline stage bundling 5+ unrelated responsibilities -- split when the source material describes distinct components.",
			"- Test: if a skill has fewer than 4 Process steps, it is probably too granular (merge it into its parent skill). If it has more than 15, it is probably too broad (split along source-backed component boundaries).",
			"- Target: 8-12 skills for a typical source set. Fewer for simple sources, more for complex multi-component systems.",
			"- Every slug must be unique. Do not emit duplicate or placeholder slugs.",
			"- Do not invent catch-all filler skills.",
			"",
			"SKILL CONCISENESS OVERRIDE:",
			"- Skills are the operational reference for the agent swarm. Unlike other .tasks files, skills need to embed data schemas, JSON payloads, field tables, and config structures inline when the source provides them, because the installed skill file is the agent's only reference at runtime.",
			"- Do NOT reference these by citation alone -- the installed skill file must be self-contained so an agent can execute it without reading the original source.",
			"- The conciseness contract still applies to prose: do not repeat the same claim across sections. But data structures belong inline in the Inputs Required or Output Format sections.",
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
			"  - Owned Skills: (list each skill slug this agent executes. These must match slugs from skills.md exactly.)",
			"  - Coordination Protocol: (specify: 1. What triggers this agent to start. 2. What upstream agent or event provides input. 3. The exact data shape received (name the structure and key fields). 4. The exact data shape emitted. 5. What downstream agent consumes the output. 6. The handoff trigger condition.)",
			"  - Error Handling: (for each failure mode the source material implies: state what can go wrong, what the agent does about it, and whether it retries, rejects, escalates, or falls back. Reference specific source constraints like latency budgets or availability targets that bound error recovery.)",
			"  - Operational Limits: (derive operational budgets from source constraints that DO exist. For example: if the source specifies a 30-second end-to-end latency target and the pipeline has 5 agents, each agent's budget is approximately 6 seconds. If the source specifies 99.9% availability, derive the maximum acceptable failure rate per agent. If no source constraints exist for a dimension, omit that dimension rather than writing UNKNOWN -- not every agent needs explicit limits on every axis.)",
			"  - Validation Gates: (specific testable checks that must pass before handoff. Not 'validate the output' but 'verify ClassifiedAlertGroup.reasoning_chain is non-empty and priority is in {P0,P1,P2,P3}'. Each gate must be mechanically checkable.)",
			"  - UNKNOWN Gates: (unresolved dependencies that block full execution, with which owned skills they affect)",
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
