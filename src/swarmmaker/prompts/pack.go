// pack.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Prompt pack loader, validator, and semantic reviewer.
// Loads prompt packs from embedded JSON (default) or user-provided files.
// Validates schema version, required draft/review/revision entries, and body
// format. The semantic reviewer rejects packs that contain forbidden intents
// (ignore evidence, skip validation, always approve) or are missing required
// concepts (source citation, UNKNOWN handling, review contracts).


package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

const (
	promptPackSchemaV1 = "v1"
	defaultPackSource  = "embedded:prompts/default_pack.json"
)

//go:embed default_pack.json
var defaultPackFS embed.FS

// Pack is a user-editable prompt body bundle. Non-negotiable runtime context,
// source material, and truth rules are injected by code outside this JSON.
type Pack struct {
	SchemaVersion string                   `json:"schema_version"`
	Name          string                   `json:"name"`
	Description   string                   `json:"description,omitempty"`
	Drafts        map[DraftKind]PromptBody `json:"drafts"`
	Review        PromptBody               `json:"review"`
	Revision      PromptBody               `json:"revision"`
	digest        string
	source        string
}

// PromptBody is rendered through text/template with missing-key errors enabled.
// Exactly one of Body or BodyLines must be supplied.
type PromptBody struct {
	Title     string   `json:"title"`
	Planning  bool     `json:"planning"`
	Body      string   `json:"body,omitempty"`
	BodyLines []string `json:"body_lines,omitempty"`
}

type PackReview struct {
	Approved bool                `json:"approved"`
	Findings []PackReviewFinding `json:"findings,omitempty"`
}

type PackReviewFinding struct {
	Severity string `json:"severity"`
	Scope    string `json:"scope"`
	Message  string `json:"message"`
}

var forbiddenPromptIntents = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"ignore_truth_rules", regexp.MustCompile(`(?i)\bignore\b.{0,40}\btruth rules\b`)},
	{"ignore_source", regexp.MustCompile(`(?i)\bignore\b.{0,40}\bsource\b`)},
	{"ignore_evidence", regexp.MustCompile(`(?i)\bignore\b.{0,40}\bevidence\b`)},
	{"ignore_validation", regexp.MustCompile(`(?i)\bignore\b.{0,40}\bvalidation\b`)},
	{"skip_validation", regexp.MustCompile(`(?i)\bskip\b.{0,40}\bvalidation\b`)},
	{"always_approve", regexp.MustCompile(`(?i)\balways\b.{0,30}\bapprove\b`)},
	{"approve_everything", regexp.MustCompile(`(?i)\bapprove\b.{0,30}\beverything\b`)},
	{"hide_fallback", regexp.MustCompile(`(?i)\bhide\b.{0,40}\bfallback\b`)},
	{"do_not_cite_source", regexp.MustCompile(`(?i)\bdo not\b.{0,30}\bcite\b.{0,30}\bsource\b`)},
	{"make_up_facts", regexp.MustCompile(`(?i)\bmake up\b.{0,40}\bfacts?\b`)},
}

var requiredPackConcepts = []string{
	"source",
	"evidence",
	"unknown",
	"validation",
	"tasks",
	"skills",
	"agents",
	"ledger",
	"agent",
	"tool",
	"output",
}

func LoadPack(path string) (Pack, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultPack()
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Pack{}, fmt.Errorf("resolve prompt pack path %q: %w", path, err)
	}
	payload, err := os.ReadFile(absPath)
	if err != nil {
		return Pack{}, fmt.Errorf("read prompt pack %s: %w", absPath, err)
	}
	return parsePack(payload, absPath)
}

func DefaultPack() (Pack, error) {
	payload, err := defaultPackFS.ReadFile("default_pack.json")
	if err != nil {
		return Pack{}, fmt.Errorf("read embedded prompt pack: %w", err)
	}
	return parsePack(payload, defaultPackSource)
}

func DefaultPackJSON() ([]byte, error) {
	payload, err := defaultPackFS.ReadFile("default_pack.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded prompt pack: %w", err)
	}
	return append([]byte(nil), payload...), nil
}

func ExportDefaultPack(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve output path %q: %w", path, err)
	}
	payload, err := DefaultPackJSON()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return fmt.Errorf("create prompt pack dir %s: %w", filepath.Dir(absPath), err)
	}
	if err := os.WriteFile(absPath, payload, 0644); err != nil {
		return fmt.Errorf("write prompt pack %s: %w", absPath, err)
	}
	return nil
}

func parsePack(payload []byte, source string) (Pack, error) {
	var pack Pack
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pack); err != nil {
		return Pack{}, fmt.Errorf("parse prompt pack %s: %w", source, err)
	}
	pack.digest = digestBytes(payload)
	pack.source = source
	if err := pack.Validate(); err != nil {
		return Pack{}, fmt.Errorf("prompt pack %s invalid: %w", source, err)
	}
	review := pack.SemanticReview()
	if !review.Approved {
		return Pack{}, fmt.Errorf("prompt pack %s failed semantic review: %s", source, review.ErrorSummary())
	}
	return pack, nil
}

func (p Pack) Validate() error {
	var errs []error
	if p.SchemaVersion != promptPackSchemaV1 {
		errs = append(errs, fmt.Errorf("unknown prompt pack schema_version %q", p.SchemaVersion))
	}
	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, fmt.Errorf("prompt pack name is required"))
	}
	for _, kind := range requiredDraftKinds() {
		body, ok := p.Drafts[kind]
		if !ok {
			errs = append(errs, fmt.Errorf("draft prompt %q is required", kind))
			continue
		}
		if err := body.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("draft prompt %q: %w", kind, err))
		}
	}
	if err := p.Review.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("review prompt: %w", err))
	}
	if err := p.Revision.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("revision prompt: %w", err))
	}
	return errors.Join(errs...)
}

func (p Pack) Digest() string {
	return p.digest
}

func (p Pack) Source() string {
	return p.source
}

func (p Pack) SemanticReview() PackReview {
	var findings []PackReviewFinding
	allText := strings.ToLower(p.combinedText())
	for _, concept := range requiredPackConcepts {
		if !strings.Contains(allText, concept) {
			findings = append(findings, PackReviewFinding{
				Severity: "error",
				Scope:    "pack",
				Message:  fmt.Sprintf("required concept %q is missing", concept),
			})
		}
	}
	findings = append(findings, forbiddenIntentFindings("pack", p.combinedText())...)
	findings = append(findings, reviewPromptFindings(p.Review.text())...)
	findings = append(findings, revisionPromptFindings(p.Revision.text())...)
	return PackReview{
		Approved: countPackReviewErrors(findings) == 0,
		Findings: findings,
	}
}

func (r PackReview) ErrorSummary() string {
	var parts []string
	for _, finding := range r.Findings {
		if finding.Severity == "error" {
			parts = append(parts, finding.Scope+": "+finding.Message)
		}
	}
	if len(parts) == 0 {
		return "no errors"
	}
	return strings.Join(parts, "; ")
}

func (b PromptBody) Validate() error {
	var errs []error
	if strings.TrimSpace(b.Title) == "" {
		errs = append(errs, fmt.Errorf("title is required"))
	}
	hasBody := strings.TrimSpace(b.Body) != ""
	hasLines := len(b.BodyLines) > 0
	if hasBody == hasLines {
		errs = append(errs, fmt.Errorf("exactly one of body or body_lines is required"))
	}
	for i, line := range b.BodyLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, "\x00") {
			errs = append(errs, fmt.Errorf("body_lines[%d] contains NUL byte", i))
		}
	}
	if _, err := template.New("prompt-body").Option("missingkey=error").Parse(b.text()); err != nil {
		errs = append(errs, fmt.Errorf("template parse failed: %w", err))
	}
	return errors.Join(errs...)
}

func (b PromptBody) render(data promptTemplateData) (string, error) {
	tpl, err := template.New("prompt-body").Option("missingkey=error").Parse(b.text())
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := tpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (b PromptBody) text() string {
	if strings.TrimSpace(b.Body) != "" {
		return b.Body
	}
	return strings.Join(b.BodyLines, "\n") + "\n"
}

func (p Pack) combinedText() string {
	var b strings.Builder
	b.WriteString(p.Name)
	b.WriteString("\n")
	b.WriteString(p.Description)
	b.WriteString("\n")
	for _, kind := range requiredDraftKinds() {
		if body, ok := p.Drafts[kind]; ok {
			b.WriteString(string(kind))
			b.WriteString("\n")
			b.WriteString(body.Title)
			b.WriteString("\n")
			b.WriteString(body.text())
			b.WriteString("\n")
		}
	}
	b.WriteString(p.Review.Title)
	b.WriteString("\n")
	b.WriteString(p.Review.text())
	b.WriteString("\n")
	b.WriteString(p.Revision.Title)
	b.WriteString("\n")
	b.WriteString(p.Revision.text())
	return b.String()
}

func forbiddenIntentFindings(scope, text string) []PackReviewFinding {
	var findings []PackReviewFinding
	for _, forbidden := range forbiddenPromptIntents {
		if forbidden.pattern.MatchString(text) {
			findings = append(findings, PackReviewFinding{
				Severity: "error",
				Scope:    scope,
				Message:  "forbidden intent detected: " + forbidden.name,
			})
		}
	}
	return findings
}

func reviewPromptFindings(text string) []PackReviewFinding {
	lower := strings.ToLower(text)
	var findings []PackReviewFinding
	for _, required := range []string{"approve", "revise", "files needing revision", "fabrication", "missing coverage"} {
		if !strings.Contains(lower, required) {
			findings = append(findings, PackReviewFinding{
				Severity: "error",
				Scope:    "review",
				Message:  fmt.Sprintf("required review contract term %q is missing", required),
			})
		}
	}
	return findings
}

func revisionPromptFindings(text string) []PackReviewFinding {
	lower := strings.ToLower(text)
	var findings []PackReviewFinding
	for _, required := range []string{"complete", "critic", "unknown", "source"} {
		if !strings.Contains(lower, required) {
			findings = append(findings, PackReviewFinding{
				Severity: "error",
				Scope:    "revision",
				Message:  fmt.Sprintf("required revision contract term %q is missing", required),
			})
		}
	}
	return findings
}

func countPackReviewErrors(findings []PackReviewFinding) int {
	count := 0
	for _, finding := range findings {
		if finding.Severity == "error" {
			count++
		}
	}
	return count
}

// NOTE: duplicated from internal/textutil because prompts is a public package.
func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
