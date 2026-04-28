// Package main implements a test harness that masquerades as the codex CLI
// for E2E testing. It accepts codex-style flags (exec, -m, -o, -s, -C) and
// returns realistic content based on the detected task type in the prompt.
//
// The harness detects which ledger task is being requested by scanning the
// prompt for the "You are compiling <target>" header, then returns pre-built
// content with source citations referencing the fixture files.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	// Handle --version for LLM tool discovery
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Println("codex 0.1.0-e2e-harness")
			return
		}
	}

	prompt := extractPrompt(os.Args[1:])
	outputFile := os.Getenv("SWARMAKER_OUTPUT_FILE")

	behavior := os.Getenv("SWARMAKER_E2E_BEHAVIOR")

	// Support codex -o flag for output file.
	if oFile := extractFlag(os.Args[1:], "-o"); oFile != "" && outputFile == "" {
		outputFile = oFile
	}


	var content string
	switch behavior {
	case "malformed":
		content = "xyz garbage output"
	case "timeout":
		select {} // block forever
	case "short-files":
		// Return content that passes the executor's minOutputLen (200) but
		// is too short for the pre-screen's deep-source threshold (1500).
		// This triggers concrete pre-screen flags while the review still
		// returns APPROVE, testing the B2 safety gate.
		content = generateShortContent(prompt)
	case "":
		content = generateContent(prompt)
	default:
		fmt.Fprintf(os.Stderr, "unknown E2E behavior %q\n", behavior)
		os.Exit(2)
	}

	if outputFile != "" {
		if err := os.MkdirAll(dirOf(outputFile), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir for output: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write output file: %v\n", err)
			os.Exit(2)
		}
	}
	fmt.Print(content)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

// extractPrompt parses the -p flag from CLI args. If -p value is "-", reads
// from stdin. Also handles the case where the prompt is a positional arg (codex style).
func extractPrompt(args []string) string {
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) {
			val := args[i+1]
			if val == "-" {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
					os.Exit(2)
				}
				return string(data)
			}
			return val
		}
	}
	// Fall back to last positional arg (codex exec style)
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.HasPrefix(args[i], "-") {
			prev := ""
			if i > 0 {
				prev = args[i-1]
			}
			// Skip values that belong to flags
			if prev == "-m" || prev == "-o" || prev == "-s" || prev == "-C" || prev == "--model" || prev == "--output-format" {
				continue
			}
			// Skip codex subcommand "exec" -- it's not a prompt
			if args[i] == "exec" {
				continue
			}
			return args[i]
		}
	}
	// Try stdin as last resort
	data, _ := io.ReadAll(os.Stdin)
	return string(data)
}

func generateContent(prompt string) string {
	lower := strings.ToLower(prompt)

	// Detect task type from the unique prompt header line:
	// "You are compiling <target> for AI Swarm Maker."
	// This is more reliable than keyword matching because cross-references
	// in citation contracts cause false positives with simple Contains checks.
	// NOTE: The adversarial review check must come AFTER isCompilingTarget checks
	// because draft prompts may contain "adversarial review" in cross-references.
	switch {
	case isCompilingTarget(lower, ".tasks/context.md"):
		return generateContext()
	case isCompilingTarget(lower, ".tasks/tasks.md"):
		return generateTasks()
	case isCompilingTarget(lower, ".tasks/prompts/product.md"):
		return generateProductPrompt()
	case isCompilingTarget(lower, ".tasks/prompts/technical.md"):
		return generateTechnicalPrompt()
	case isCompilingTarget(lower, ".tasks/prompts/tools.md"):
		return generateToolsPrompt()
	case isCompilingTarget(lower, ".tasks/prompts/deployment.md"):
		return generateDeploymentPrompt()
	case isCompilingTarget(lower, ".tasks/todo.md"):
		return generateTodo()
	case isCompilingTarget(lower, ".tasks/skills.md"):
		return generateSkills()
	case isCompilingTarget(lower, ".tasks/agents.md"):
		return generateAgents()
	case isCompilingTarget(lower, "adversarial review") || strings.Contains(lower, "review the following"):
		return generateReviewApproval()
	default:
		// Generic fallback with sufficient length
		return generateGenericContent()
	}
}

// isCompilingTarget checks for the unique prompt header "you are compiling <target>"
// which appears exactly once per prompt, identifying the artifact being generated.
func isCompilingTarget(lower, target string) bool {
	return strings.Contains(lower, "you are compiling "+target)
}

func sourceRef() string {
	inputRoot := os.Getenv("SWARMAKER_E2E_INPUT_ROOT")
	if inputRoot != "" {
		return "Source: [notes.md](" + inputRoot + "/notes.md)"
	}
	return "Source: [notes.md](notes.md)"
}

func generateContext() string {
	return strings.Join([]string{
		"# SupportOps - .tasks Context",
		"",
		"## Ledger Objective",
		"- Build a skill bundle for support operations ticket triage and escalation. " + sourceRef(),
		"",
		"## Source Evidence Map",
		"- Ticket triage procedures from notes.md define P0-P3 classification. " + sourceRef(),
		"- API endpoints from api_docs.md provide ticket CRUD and SLA metrics. " + sourceRef(),
		"- Architecture from spec.md describes the three-service platform. " + sourceRef(),
		"",
		"## Target Bundle Tree",
		"- Emit `.tasks/`, one hidden platform root, `README.md`, and `install.sh`. " + sourceRef(),
		"",
		"## OODA Agent Roles",
		"- Observe inventories evidence, Orient decomposes work, Decide applies validation gates, Act renders the bundle. " + sourceRef(),
		"",
		rep("Evidence-backed context for support ops ticket management. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateTasks() string {
	return strings.Join([]string{
		"# SupportOps - .tasks Decomposition",
		"",
		"## Source-Derived User Goals",
		"- Automate ticket classification using P0-P3 priority rules. " + sourceRef(),
		"- Track SLA compliance across all priority levels. " + sourceRef(),
		"- Provide real-time SLA breach alerting for P0 and P1 tickets. " + sourceRef(),
		"",
		"## Skill And Agent Capabilities",
		"- Skills decompose triage, escalation, and SLA monitoring work. " + sourceRef(),
		"- Agents coordinate observe/orient/decide/act phases. " + sourceRef(),
		"- Each agent owns a specific phase of the pipeline lifecycle. " + sourceRef(),
		"",
		"## Validation Requirements",
		"- Validate links, manifests, routing, and rendered bundle shape. " + sourceRef(),
		"- Ensure citation density meets minimum thresholds per file. " + sourceRef(),
		"",
		rep("Task decomposition detail for ticket operations including priority classification, SLA tracking, and escalation workflows. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateProductPrompt() string {
	return strings.Join([]string{
		"# SupportOps - Product Prompt",
		"",
		"## Product Goal",
		"- Produce a SKILL bundle rooted in `.tasks/` for support ops workflows. " + sourceRef(),
		"- Enable automated ticket triage using P0-P3 priority classification rules. " + sourceRef(),
		"",
		"## Output Expectations",
		"- Final output includes `.tasks/`, one selected platform root, `README.md`, and `install.sh`. " + sourceRef(),
		"- All generated skills must include source citations and evidence references. " + sourceRef(),
		"",
		"## Acceptance Criteria",
		"- Bundle must pass programmatic validation and adversarial review. " + sourceRef(),
		"- Install script must correctly copy `.tasks/` and platform root to target directory. " + sourceRef(),
		"",
		rep("Product prompt detail for support operations including ticket management, SLA compliance, and escalation procedures. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateTechnicalPrompt() string {
	return strings.Join([]string{
		"# SupportOps - Technical Prompt",
		"",
		"## Pipeline",
		"- Ingest support docs, normalize, decompose into skills, validate, and render. " + sourceRef(),
		"- Pipeline phases: observe, orient, decide, act with validation gates between each phase. " + sourceRef(),
		"",
		"## Output Renderer Contract",
		"- Render from `.tasks/` into a hidden selected-platform subtree. " + sourceRef(),
		"- Support multiple output formats: claude, codex, gemini. " + sourceRef(),
		"",
		"## Technical Requirements",
		"- REST API endpoints for ticket CRUD operations with Bearer token auth. " + sourceRef(),
		"- PostgreSQL for ticket storage, ClickHouse for analytics, Redis for queue. " + sourceRef(),
		"",
		rep("Technical prompt detail for the support platform architecture and implementation requirements. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateToolsPrompt() string {
	return strings.Join([]string{
		"# Tool Synthesis Prompt",
		"",
		"## Tool Requests",
		"- Decision: UNKNOWN until the source justifies a generated helper tool. " + sourceRef(),
		"- Evaluate whether ticket classification rules warrant a standalone tool. " + sourceRef(),
		"",
		"## No-Tool Cases",
		"- Keep prompt-only behavior when no helper tool is justified. " + sourceRef(),
		"- SLA monitoring can be handled through prompt instructions without a dedicated tool. " + sourceRef(),
		"",
		"## Tool Evaluation Criteria",
		"- Tools must be justified by explicit source material or implementation requirements. " + sourceRef(),
		"- Do not generate speculative tools for hypothetical use cases. " + sourceRef(),
		"",
		rep("Tool synthesis detail for support ops evaluating whether helper tools are justified by source material. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateDeploymentPrompt() string {
	return strings.Join([]string{
		"# SupportOps - Operational Validation And Packaging",
		"",
		"## Output Tree Acceptance",
		"- Bundle must include `.tasks/`, one hidden platform root, `README.md`, and `install.sh`. " + sourceRef(),
		"- Platform root directory must match the selected output format (e.g. `.codex/`). " + sourceRef(),
		"",
		"## Release Checks",
		"- Run validation, check links, verify manifests, and confirm bundle shape. " + sourceRef(),
		"- Ensure all ledger files meet minimum length requirements for deep source. " + sourceRef(),
		"",
		"## Deployment Requirements",
		"- Install script supports --target and --global modes for flexible deployment. " + sourceRef(),
		"- Kubernetes on AWS EKS with Helm charts for service deployment. " + sourceRef(),
		"",
		rep("Deployment prompt detail for support ops bundle packaging, validation, and release procedures. "+sourceRef()+"\n", 12),
	}, "\n")
}

func generateTodo() string {
	return strings.Join([]string{
		"# SupportOps - .tasks Delivery Todo",
		"",
		"## Queue Rules",
		"- [ ] **Observe: Inventory sources** - Build: capture readable files Verify: compare counts Evidence: `.tasks/evidence.json` Source: " + sourceRef(),
		"- [ ] **Orient: Build decomposition** - Build: write `.tasks/skills.md` and `.tasks/agents.md` Verify: parse blocks Evidence: `.tasks/manifest.json` Source: " + sourceRef(),
		"- [ ] **Decide: Validate bundle contract** - Build: check selected renderer Verify: hidden root only Evidence: `.tasks/validation-report.md` Source: " + sourceRef(),
		"- [ ] **Act: Render bundle** - Build: write platform tree Verify: install script copies `.tasks/` and hidden root Evidence: `README.md` Source: " + sourceRef(),
		"",
		rep("- [ ] **Iterate** - Build: refine ledger Verify: rerun checks Evidence: `.tasks/validation-report.md` Source: "+sourceRef()+"\n", 10),
	}, "\n")
}

func generateSkills() string {
	return strings.Join([]string{
		"# SupportOps - Skill Decomposition",
		"",
		"## Skill: Ticket Triage",
		"- Slug: ticket-triage",
		"- Summary: Classify and prioritize incoming support tickets using P0-P3 rules.",
		"",
		"Use this skill to automate ticket classification based on severity rules from the support ops notes. " + sourceRef(),
		"Consumers: final selected-platform playbooks and instructions. " + sourceRef(),
		"Boundaries: do not invent unsupported APIs, credentials, or model capabilities. " + sourceRef(),
		"Shared capabilities: routing, critique, revision, validation, and tool-use decisions must remain evidence-backed. " + sourceRef(),
		"Final skill tree inputs: `.tasks/context.md`, `.tasks/tasks.md`, `.tasks/prompts/product.md`, `.tasks/prompts/technical.md`. " + sourceRef(),
		"UNKNOWN gate: any missing data-provider contract remains UNKNOWN and blocks generated tool claims. " + sourceRef(),
		"",
		"### Inputs Required",
		"- " + "`" + "alert_id: str" + "`" + " Unique identifier for the incoming alert",
		"- " + "`" + "severity: integer" + "`" + " Alert severity level from 0 to 4",
		"- " + "`" + "source_system: str" + "`" + " Name of the upstream system",
		"- " + "`" + "payload: object" + "`" + " Raw event data structure",
		"",
		"### MCP Input Schema",
		"```json",
		`{"type": "object", "properties": {"alert_id": {"type": "string", "description": "Unique identifier for the incoming alert"}, "severity": {"type": "integer", "description": "Alert severity level from 0 to 4"}, "source_system": {"type": "string", "description": "Name of the upstream system"}, "payload": {"type": "object", "description": "Raw event data structure"}}, "required": ["alert_id", "severity"]}`,
		"```",
		"",
		"## Skill: SLA Monitoring",
		"- Slug: sla-monitoring",
		"- Summary: Track and report SLA compliance metrics across all ticket priority levels.",
		"",
		"Use this skill to monitor SLA adherence for P0-P3 tickets and generate compliance reports. " + sourceRef(),
		"Consumers: analytics dashboards and escalation workflows. " + sourceRef(),
		"Boundaries: relies on metrics API endpoint GET /api/v1/metrics/sla for compliance data. " + sourceRef(),
		"Shared capabilities: evidence collection, threshold alerting, and trend analysis. " + sourceRef(),
		"Final skill tree inputs: `.tasks/context.md`, `.tasks/prompts/technical.md`, `.tasks/prompts/deployment.md`. " + sourceRef(),
		"UNKNOWN gate: alerting thresholds not specified in source material - implementation decision. " + sourceRef(),
		"",
		"### Inputs Required",
		"- " + "`" + "alert_id: str" + "`" + " Unique identifier for the incoming alert",
		"- " + "`" + "severity: integer" + "`" + " Alert severity level from 0 to 4",
		"- " + "`" + "source_system: str" + "`" + " Name of the upstream system",
		"- " + "`" + "payload: object" + "`" + " Raw event data structure",
		"",
		"### MCP Input Schema",
		"```json",
		`{"type": "object", "properties": {"alert_id": {"type": "string", "description": "Unique alert identifier"}, "severity": {"type": "integer", "description": "Alert severity level 0-4"}, "source_system": {"type": "string", "description": "Name of upstream system"}, "payload": {"type": "object", "description": "Raw event data"}}, "required": ["alert_id"]}`,
		"```",
	}, "\n")
}

func generateAgents() string {
	return strings.Join([]string{
		"# SupportOps - Agent Decomposition",
		"",
		"## Agent: Observe Intake",
		"- Role: Observe",
		"",
		"This agent inventories support documentation, evidence ledgers, and blocked assumptions before render. " + sourceRef(),
		"OODA handoff: send normalized facts to Orient and blocked UNKNOWNs to Decide. " + sourceRef(),
		"Coordination rules: critique findings flow back through Decide before Act writes output. " + sourceRef(),
		"Final agent tree inputs: `.tasks/context.md`, `.tasks/tasks.md`, `.tasks/skills.md`, `.tasks/todo.md`. " + sourceRef(),
		"UNKNOWN gate: missing install targets remain UNKNOWN until deployment rules are explicit. " + sourceRef(),
		"",
		"## Agent: Orient Decomposer",
		"- Role: Orient",
		"",
		"This agent decomposes observed evidence into structured skill and agent definitions. " + sourceRef(),
		"OODA handoff: send decomposition artifacts to Decide for validation gate checks. " + sourceRef(),
		"Coordination rules: must produce well-formed skill blocks with Slug and Summary fields. " + sourceRef(),
		"Owned artifacts: `.tasks/skills.md`, `.tasks/agents.md`, `.tasks/tasks.md`. " + sourceRef(),
		"UNKNOWN gate: skill boundaries remain UNKNOWN until source material provides explicit scope. " + sourceRef(),
	}, "\n")
}

func generateReviewApproval() string {
	return strings.Join([]string{
		"## Issues (must fix)",
		"- None",
		"",
		"## Issues (should fix)",
		"- None",
		"",
		"## Verdict",
		"APPROVE",
		"",
		"All files pass adversarial review. Source citations are present and reference actual input files.",
		rep("Review detail confirms evidence-backed content throughout the ledger. ", 8),
	}, "\n")
}

func generateGenericContent() string {
	return strings.Join([]string{
		"# Generic Output",
		"",
		"This content was generated by the E2E test harness. " + sourceRef(),
		"",
		rep("Evidence-backed generic content for testing. "+sourceRef()+"\n", 10),
	}, "\n")
}

// generateShortContent returns structurally valid but short content (200-500 chars)
// for each task type. This passes the executor's minOutputLen check but triggers
// the pre-screen's "suspiciously short for deep source" flag (< 1500 chars).
func generateShortContent(prompt string) string {
	lower := strings.ToLower(prompt)

	if isCompilingTarget(lower, "adversarial review") || strings.Contains(lower, "review the following") {
		return generateReviewApproval()
	}

	switch {
	case isCompilingTarget(lower, ".tasks/skills.md"):
		return strings.Join([]string{
			"# SupportOps - Skill Decomposition",
			"",
			"## Skill: Ticket Triage",
			"- Slug: ticket-triage",
			"- Summary: Classify tickets using P0-P3 rules.",
			"",
			"Use this skill to triage tickets. " + sourceRef(),
			rep("Short skill content for testing. ", 5),
		}, "\n")
	case isCompilingTarget(lower, ".tasks/agents.md"):
		return strings.Join([]string{
			"# SupportOps - Agent Decomposition",
			"",
			"## Agent: Observe Intake",
			"- Role: Observe",
			"",
			"This agent inventories documentation. " + sourceRef(),
			rep("Short agent content for testing. ", 5),
		}, "\n")
	default:
		// All other files get normal-length content
		return generateContent(prompt)
	}
}

// extractFlag returns the value of a flag (e.g. "-o") from args, or "" if not found.
func extractFlag(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func rep(s string, n int) string {
	return strings.Repeat(s, n)
}
