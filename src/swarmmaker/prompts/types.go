// types.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Prompt IR types and compilation contracts.
// Defines PromptIR (the structured metadata passed to every prompt), and the
// contract functions that assemble the prompt header: execution contract,
// citation contract, source material block, strict truth rules, conciseness
// contract, and toolchain language conventions. These contracts ensure every
// LLM prompt carries the same evidence discipline regardless of draft kind.


package prompts

import (
	"errors"
	"fmt"
	"strings"
)

// PromptIR is the typed context used by the prompt compiler. It is intentionally
// small: only facts already established by ingestion, routing, and CLI parsing
// are allowed in prompts.
type PromptIR struct {
	ProjectName          string          `json:"project_name"`
	SourceMaterial       string          `json:"source_material"`
	InputRoot            string          `json:"input_root,omitempty"`
	TargetFormats        []string        `json:"target_formats"`
	GeneratorProvider    string          `json:"generator_provider"`
	CriticProvider       string          `json:"critic_provider"`
	OutputRenderers      []string        `json:"output_renderers"`
	EvidenceManifestPath string          `json:"evidence_manifest_path"`
	IRManifestPath       string          `json:"ir_manifest_path"`
	PromptPackName       string          `json:"prompt_pack_name"`
	PromptPackSource     string          `json:"prompt_pack_source"`
	PromptPackDigest     string          `json:"prompt_pack_digest"`
	InputFileCount       int             `json:"input_file_count"`
	BinaryFileCount      int             `json:"binary_file_count"`
	EvidenceEventCount   int             `json:"evidence_event_count"`
	ToolLanguages        []string        `json:"tool_languages,omitempty"`
	SourceFiles          []SourceFileRef `json:"source_files,omitempty"`
}

type SourceFileRef struct {
	RelPath string `json:"rel_path"`
	AbsPath string `json:"abs_path"`
}

type PromptFileSnapshot struct {
	RelPath string `json:"rel_path"`
	AbsPath string `json:"abs_path"`
	Content string `json:"content"`
}

// Validate rejects missing required prompt context instead of letting empty
// fields shape generated output.
func (ir PromptIR) Validate() error {
	var errs []error
	require := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", field))
		}
	}
	require("project_name", ir.ProjectName)
	require("source_material", ir.SourceMaterial)
	require("generator_provider", ir.GeneratorProvider)
	require("critic_provider", ir.CriticProvider)
	require("evidence_manifest_path", ir.EvidenceManifestPath)
	require("ir_manifest_path", ir.IRManifestPath)
	require("prompt_pack_name", ir.PromptPackName)
	require("prompt_pack_source", ir.PromptPackSource)
	require("prompt_pack_digest", ir.PromptPackDigest)
	if len(normalizedOutputFormatNames(ir.TargetFormats)) == 0 {
		errs = append(errs, fmt.Errorf("target_formats is required"))
	}
	if len(normalizedOutputFormatNames(ir.OutputRenderers)) == 0 {
		errs = append(errs, fmt.Errorf("output_renderers is required"))
	}
	for field, value := range map[string]int{
		"input_file_count":     ir.InputFileCount,
		"binary_file_count":    ir.BinaryFileCount,
		"evidence_event_count": ir.EvidenceEventCount,
	} {
		if value < 0 {
			errs = append(errs, fmt.Errorf("%s must be non-negative", field))
		}
	}
	return errors.Join(errs...)
}

func (ir PromptIR) contextBlock() string {
	languages := ir.languageSummary()
	targets := strings.Join(normalizedOutputFormatNames(ir.TargetFormats), ", ")
	renderers := strings.Join(normalizedOutputFormatNames(ir.OutputRenderers), ", ")
	return fmt.Sprintf(`<swarmmaker-ir>
project_name: %s
target_output_formats: %s
generator_provider: %s
critic_provider: %s
output_renderers: %s
evidence_manifest: %s
ir_manifest: %s
prompt_pack_name: %s
prompt_pack_source: %s
prompt_pack_digest: %s
input_file_count: %d
binary_file_count: %d
evidence_event_count: %d
tool_languages: %s
</swarmmaker-ir>

`, ir.ProjectName, targets, ir.GeneratorProvider, ir.CriticProvider, renderers, ir.EvidenceManifestPath, ir.IRManifestPath, ir.PromptPackName, ir.PromptPackSource, ir.PromptPackDigest, ir.InputFileCount, ir.BinaryFileCount, ir.EvidenceEventCount, languages)
}

func (ir PromptIR) languageSummary() string {
	languages := normalizedLanguages(ir.ToolLanguages)
	if len(languages) == 0 {
		return "UNKNOWN (no supported code language was detected from source files)"
	}
	return strings.Join(languages, ", ")
}

func sourceBlock(content string) string {
	return fmt.Sprintf("<source-material>\n%s\n</source-material>\n\n", content)
}

func selfContainedExecutionContract() string {
	return strings.Join([]string{
		"EXECUTION CONTRACT:",
		"- This prompt is self-contained. Do not read workspace files, inspect git, run shell commands, or use patch/edit tools.",
		"- Do not create or modify files yourself. The caller captures your final response as stdout or writes it to the target file.",
		"- Use only the source material, manifests, file snapshots, and absolute paths embedded in this prompt.",
		"",
	}, "\n")
}

func citationContract(ir PromptIR, planning bool) string {
	var b strings.Builder
	b.WriteString("CITATION CONTRACT:\n")
	b.WriteString("- Every substantive claim must include a visible citation in the artifact body.\n")
	b.WriteString("- Preferred single-source format: write Source: followed by a markdown link whose display text is the file name and whose target is the absolute path from PromptIR.\n")
	b.WriteString("- Preferred multi-source format: write Sources: followed by comma-separated markdown links, each using the real file name and absolute path.\n")
	b.WriteString("- Do NOT use placeholder paths. Every citation must reference a real file from the source context or manifests.\n")
	b.WriteString("- Cite manifests when a claim comes from routing or evidence context rather than the loose notes.\n")
	if planning {
		b.WriteString("- Implementation decisions are allowed only when the source is silent. Mark them as `Source: Not specified in source material - implementation decision.`\n")
	} else {
		b.WriteString("- If the source is silent, write `UNKNOWN` instead of inventing a fact.\n")
	}
	if refs := ir.sourceCitationExamples(); refs != "" {
		b.WriteString("- Use these exact local citation targets when relevant:\n")
		b.WriteString(refs)
	}
	b.WriteString("\n")
	return b.String()
}

func (ir PromptIR) sourceCitationExamples() string {
	var b strings.Builder
	for _, ref := range ir.SourceFiles {
		if strings.TrimSpace(ref.RelPath) == "" || strings.TrimSpace(ref.AbsPath) == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("  - [%s](%s)\n", ref.RelPath, ref.AbsPath))
	}
	if strings.TrimSpace(ir.EvidenceManifestPath) != "" {
		b.WriteString(fmt.Sprintf("  - [evidence.json](%s)\n", ir.EvidenceManifestPath))
	}
	if strings.TrimSpace(ir.IRManifestPath) != "" {
		b.WriteString(fmt.Sprintf("  - [manifest.json](%s)\n", ir.IRManifestPath))
	}
	return b.String()
}

func normalizedLanguages(values []string) []string {
	allowed := map[string]struct{}{
		"go":         {},
		"python":     {},
		"typescript": {},
		"javascript": {},
		"rust":       {},
		"shell":      {},
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[clean]; !ok {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func toolchainConventions(languages []string) string {
	normalized := normalizedLanguages(languages)
	if len(normalized) == 0 {
		return "TOOLCHAIN LANGUAGES: UNKNOWN (no source code files were detected in the input folder). The source material may mention preferred languages (e.g., 'Language: Go'), but this describes the target system's runtime stack, not a detected codebase. Do not invent build commands, test runners, compile steps, or release tooling from runtime stack preferences alone. If tool synthesis requires a language, mark it as UNKNOWN and fail the dependent decision gate.\n\n"
	}
	conventions := map[string]string{
		"go":         "Use `go test ./...`, `go vet ./...`, and `go build ./...`.",
		"python":     "Use `python3`; tests run with `python3 -m pytest` if pytest is present.",
		"typescript": "Use `npx tsc`, `npm test`, and `npm run build` when package scripts exist.",
		"javascript": "Use `npm test` and `npm run build` when package scripts exist.",
		"rust":       "Use `cargo test`, `cargo clippy`, and `cargo build`.",
		"shell":      "Use `sh -n` for syntax checks and executable integration tests.",
	}
	var b strings.Builder
	b.WriteString("TOOLCHAIN CONVENTIONS:\n")
	for _, language := range normalized {
		b.WriteString("- ")
		b.WriteString(language)
		b.WriteString(": ")
		b.WriteString(conventions[language])
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

func concisenessContract() string {
	return strings.Join([]string{
		"CONCISENESS CONTRACT:",
		"- Be comprehensive but concise. Each source-backed fact should appear exactly once with its citation.",
		"- Do not repeat the same fact across multiple sections of the same file.",
		"- Reference large source blocks (JSON payloads, tables, config examples) by citation rather than copying them inline. Preserve exact field names and values in your own prose without duplicating the entire block.",
		"- If a source fact is already preserved in .tasks/context.md or .tasks/tasks.md, subsequent files may cite those ledger files rather than re-embedding the raw data.",
		"",
	}, "\n")
}

func strictRules(planning bool) string {
	var b strings.Builder
	b.WriteString("SWARMMAKER TRUTH RULES:\n")
	b.WriteString("- No empty/default value may influence final output because the validation pipeline treats default values as system lies and will fail the build. If a required source fact is missing, write UNKNOWN and mark the dependent decision gate as failed.\n")
	b.WriteString("- No hidden fallback because silent fallbacks corrupt downstream artifacts that depend on accurate data. Every fallback must be explicit, logged, counted, and visible in the evidence trail.\n")
	b.WriteString("- No partial parsing because incomplete data propagates errors to every dependent artifact. If source material is malformed, preserve the raw payload and state why the dependent decision cannot be made.\n")
	b.WriteString("- No schema drift because the renderer validates output format coverage and rejects mismatches. Generated artifacts must target every requested output format exactly.\n")
	b.WriteString("- No success without evidence because the adversarial review checks citation presence and rejects unsupported claims. Every major claim must cite source material or recorded implementation-decision evidence.\n")
	if planning {
		b.WriteString("- Implementation decisions are allowed only when source material is silent; mark them as implementation decisions and list validation proof.\n")
	} else {
		b.WriteString("- Do not invent product facts, APIs, provider capabilities, tool languages, numbers, or output-tree rules not present in source or IR.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// normalizedOutputFormatNames deduplicates and lowercases format names,
// preserving input order. This intentionally differs from
// ir.normalizeOutputFormats which sorts for deterministic artifact IDs.
func normalizedOutputFormatNames(values []string) []string {
	allowed := map[string]struct{}{
		"claude": {},
		"codex":  {},
		"gemini": {},
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[clean]; !ok {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func outputOnlyMarkdown() string {
	return "Output markdown only. The caller writes your response to a file; non-markdown content will corrupt the artifact. No preamble. No commentary. No wrapper text.\n\n"
}

func constraintReminder() string {
	return strings.Join([]string{
		"REMINDER (these rules override everything above):",
		"- Every claim must cite its source file. No citation = no claim.",
		"- Missing facts become UNKNOWN. Never invent data, endpoints, or commands.",
		"- Write the artifact body directly. No preamble, no commentary, no status notes.",
		"- Embed data schemas inline for skills. Reference by citation for other artifacts.",
		"- Each Process step must include failure handling and source citation.",
		"",
	}, "\n")
}
