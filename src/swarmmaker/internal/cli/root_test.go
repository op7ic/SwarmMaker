// root_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for the CLI pipeline orchestrator.
// Covers project name sanitization, verdict parsing, revision path parsing,
// evidence manifest writing, output swarm rendering, overwrite protection,
// programmatic validation (broken links, template leaks, status notes),
// validation report generation, and the validationPassed truth table.


package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/op7ic/swarmmaker/internal/ingestion"
	"github.com/op7ic/swarmmaker/internal/output"
	"github.com/op7ic/swarmmaker/internal/swarm"
	"github.com/op7ic/swarmmaker/prompts"
)

func TestVersionDefaultIsDev(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestVersionVariableIsUsedByVersionCmd(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "test-1.2.3"
	if Version != "test-1.2.3" {
		t.Fatalf("Version = %q, want %q", Version, "test-1.2.3")
	}
}

func TestSanitizeProjectName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyProject", "MyProject"},
		{"my-project", "my-project"},
		{"my_project", "my_project"},
		{"My Project", "My Project"},
		{"project.v2", "project.v2"},
		{"$(rm -rf /)", "rm -rf"},
		{"`whoami`", "whoami"},
		{"proj;echo pwned", "projecho pwned"},
		{"proj|cat /etc/passwd", "projcat etcpasswd"},
		{"proj&& malicious", "proj malicious"},
		{"proj$(cmd)", "projcmd"},
		{"proj'injection'", "projinjection"},
		{`proj"injection"`, "projinjection"},
		{"", "Project"},
		{"$$$", "Project"},
		{"   ", "Project"},
		{"123", "123"},
		{"a", "a"},
	}
	for _, tt := range tests {
		got := sanitizeProjectName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeProjectName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean approve",
			input: "## Issues (must fix)\n- None\n\n## Verdict\nAPPROVE\n",
			want:  "approve",
		},
		{
			name:  "clean revise",
			input: "## Issues (must fix)\n- File X has problem\n\n## Verdict\nREVISE\n",
			want:  "revise",
		},
		{
			name:  "lowercase approve",
			input: "## Verdict\napprove",
			want:  "approve",
		},
		{
			name:  "approved variant rejected",
			input: "## Verdict\nApproved",
			want:  "unknown",
		},
		{
			name:  "no verdict section",
			input: "The files look fine. No issues found.",
			want:  "unknown",
		},
		{
			name:  "verdict with extra whitespace",
			input: "## Verdict\n  APPROVE  \n",
			want:  "approve",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVerdict(tt.input)
			if got != tt.want {
				t.Errorf("parseVerdict() = %q, want %q\ninput:\n%s", got, tt.want, tt.input)
			}
		})
	}
}

func TestParseFilesForRevision(t *testing.T) {
	candidates := []string{
		".tasks/context.md",
		".tasks/prompts/product.md",
		".tasks/prompts/technical.md",
		".tasks/todo.md",
	}

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "parses tasks paths directly",
			input: `### Verdict
REVISE

### Files Needing Revision
- .tasks/prompts/product.md: missing bundle contract
- .tasks/todo.md: missing verify steps

### End`,
			want: []string{".tasks/prompts/product.md", ".tasks/todo.md"},
		},
		{
			name: "accepts trimmed paths without tasks prefix",
			input: `### Files Needing Revision
- prompts/technical.md: fix routing policy`,
			want: []string{".tasks/prompts/technical.md"},
		},
		{
			name:  "accepts absolute tasks paths",
			input: "### Files Needing Revision\n- `/tmp/out/.tasks/context.md`: replace status note",
			want:  []string{".tasks/context.md"},
		},
		{
			name:  "returns empty when no revision section exists",
			input: "### Verdict\nREVISE\nSome files need work.",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilesForRevision(tt.input, candidates)
			if len(got) != len(tt.want) {
				t.Fatalf("parseFilesForRevision() = %v, want %v", got, tt.want)
			}
			for i, value := range got {
				if value != tt.want[i] {
					t.Fatalf("parseFilesForRevision()[%d] = %q, want %q", i, value, tt.want[i])
				}
			}
		})
	}
}

func TestNormalizeRevisionPath(t *testing.T) {
	tests := map[string]string{
		".tasks/prompts/product.md":            ".tasks/prompts/product.md",
		"`/tmp/out/.tasks/prompts/product.md`": ".tasks/prompts/product.md",
		"./.tasks/context.md":                  ".tasks/context.md",
		"/tmp/out/.tasks/todo.md":              ".tasks/todo.md",
		"prompts/technical.md":                 "prompts/technical.md",
		"'./.tasks/prompts/deployment.md'":     ".tasks/prompts/deployment.md",
	}

	for input, want := range tests {
		if got := normalizeRevisionPath(input); got != want {
			t.Fatalf("normalizeRevisionPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWriteEvidenceManifestPersistsEvidence(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := &ingestion.Context{
		RootPath:   "input",
		FileCount:  1,
		TotalBytes: 12,
		Files: []ingestion.FileEntry{
			{RelPath: "notes.md", FileType: "markdown", Size: 12},
		},
		Evidence: []ingestion.EvidenceEntry{
			{
				Phase:    ingestion.EvidencePhaseIngestion,
				Category: ingestion.EvidenceCategoryHiddenPath,
				RelPath:  ".env",
				Detail:   "hidden path excluded from ingestion",
			},
		},
	}

	path, err := writeEvidenceManifest(tasksDir, ctx)
	if err != nil {
		t.Fatalf("writeEvidenceManifest failed: %v", err)
	}
	if filepath.Base(path) != "evidence.json" {
		t.Fatalf("expected evidence.json, got %q", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading evidence manifest: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"hidden_path", "notes.md", "file_count"} {
		if !strings.Contains(text, want) {
			t.Fatalf("evidence manifest missing %q:\n%s", want, text)
		}
	}
}

func TestEmitOutputSwarmWritesRequestedFormatTree(t *testing.T) {
	dir := t.TempDir()
	for rel, content := range sampleLedgerContents(filepath.Join(dir, "input", "notes.md")) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := emitOutputSwarms(dir, dir, []output.Format{output.FormatGemini}, "SwarmMaker", "codex", "codex", ledgerFiles)
	if err != nil {
		t.Fatalf("emitOutputSwarms failed: %v", err)
	}

	for _, rel := range []string{
		".gemini/GEMINI.md",
		".gemini/README.md",
		".gemini/playbooks/index.md",
		".gemini/playbooks/finance-intake.md",
		"README.md",
		"install.sh",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s to be written: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gemini")); !os.IsNotExist(err) {
		t.Fatalf("did not expect legacy visible root, got %v", err)
	}
	rootReadme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read root README: %v", err)
	}
	if strings.Contains(string(rootReadme), "..`") || strings.Contains(string(rootReadme), "need validate") {
		t.Fatalf("root README usage hint was not normalized:\n%s", rootReadme)
	}
}

func TestEmitOutputSwarmsWritesAllRequestedTrees(t *testing.T) {
	dir := t.TempDir()
	for rel, content := range sampleLedgerContents(filepath.Join(dir, "input", "notes.md")) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	formats := []output.Format{output.FormatClaude, output.FormatCodex, output.FormatGemini}
	if err := emitOutputSwarms(dir, dir, formats, "SwarmMaker", "codex", "codex", ledgerFiles); err != nil {
		t.Fatalf("emitOutputSwarms(all) failed: %v", err)
	}

	for _, rel := range []string{
		".claude/SKILL.md",
		".codex/AGENTS.md",
		".gemini/GEMINI.md",
		"README.md",
		"install.sh",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s to be written: %v", rel, err)
		}
	}

	installContent, err := os.ReadFile(filepath.Join(dir, "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	for _, want := range []string{".claude", ".codex", ".gemini"} {
		if !strings.Contains(string(installContent), want) {
			t.Fatalf("install.sh missing %q:\n%s", want, installContent)
		}
	}
}

func TestParseOutputFormatsSupportsAllAndCommaLists(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []output.Format
	}{
		{input: "all", want: []output.Format{output.FormatClaude, output.FormatCodex, output.FormatGemini}},
		{input: "codex,claude,gemini", want: []output.Format{output.FormatClaude, output.FormatCodex, output.FormatGemini}},
	} {
		got, err := parseOutputFormats(tc.input)
		if err != nil {
			t.Fatalf("parseOutputFormats(%q) failed: %v", tc.input, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("parseOutputFormats(%q) len = %d, want %d", tc.input, len(got), len(tc.want))
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("parseOutputFormats(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestPrepareOutputTreeForceRemovesStaleTree(t *testing.T) {
	dir := t.TempDir()
	staleHidden := filepath.Join(dir, ".gemini", "old.md")
	staleVisible := filepath.Join(dir, "gemini", "old.md")
	staleLedger := filepath.Join(dir, ".tasks", "validation-report.md")
	staleSwarmmaker := filepath.Join(dir, ".swarmmaker", "ir", "manifest.json")
	staleReadme := filepath.Join(dir, "README.md")
	staleInstall := filepath.Join(dir, "install.sh")
	staleEvidence := filepath.Join(dir, "evidence.json")
	staleReport := filepath.Join(dir, "validation-report.md")
	for _, path := range []string{staleHidden, staleVisible, staleLedger, staleSwarmmaker, staleReadme, staleInstall, staleEvidence, staleReport} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := prepareOutputTree(dir, []output.Format{output.FormatGemini}, true); err != nil {
		t.Fatalf("prepareOutputTree(force) failed: %v", err)
	}
	for _, path := range []string{staleHidden, staleVisible, staleLedger, staleSwarmmaker, staleReadme, staleInstall, staleEvidence, staleReport} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected stale artifact %s to be removed, got: %v", path, err)
		}
	}
}

func TestPrepareOutputTreeRejectsExistingArtifactsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	err := prepareOutputTree(dir, []output.Format{output.FormatGemini}, false)
	if err == nil {
		t.Fatal("expected overwrite protection to reject existing root artifacts")
	}
	if !strings.Contains(err.Error(), "output artifact already exists") {
		t.Fatalf("error = %v, want overwrite protection failure", err)
	}
}

func TestValidateDraftProgrammaticAcceptsLedgerFilesOnly(t *testing.T) {
	dir := t.TempDir()
	writeLedgerFiles(t, dir, ledgerFiles)

	issues := validateDraftProgrammatic(dir, ledgerFiles)
	if len(issues) != 0 {
		t.Fatalf("expected ledger validation to pass, got: %#v", issues)
	}
}

func TestValidateDraftProgrammaticFailsMissingFile(t *testing.T) {
	dir := t.TempDir()
	expectedFiles := []string{".tasks/prompts/product.md"}

	issues := validateDraftProgrammatic(dir, expectedFiles)
	if len(issues) != 1 || issues[0].Problem != "file missing" {
		t.Fatalf("expected missing-file error, got: %#v", issues)
	}
}

func TestValidateDraftProgrammaticFailsBrokenRelativeLink(t *testing.T) {
	dir := t.TempDir()
	expectedFiles := []string{".tasks/prompts/product.md"}
	path := filepath.Join(dir, ".tasks/prompts/product.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("source-backed content. ", 20) + "\n[missing](missing.md)\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	issues := validateDraftProgrammatic(dir, expectedFiles)
	if len(issues) == 0 {
		t.Fatal("expected broken relative link to fail validation")
	}
	if !strings.Contains(issues[0].Problem, "broken link") {
		t.Fatalf("expected broken-link error, got: %#v", issues)
	}
}

func TestValidateDraftProgrammaticAllowsLinksIntoConfiguredRoots(t *testing.T) {
	dir := t.TempDir()
	inputDir := filepath.Join(dir, "input")
	outputDir := filepath.Join(dir, "output")
	expectedFiles := []string{".tasks/prompts/product.md"}

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputDir, "notes.md"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outputDir, ".tasks", "prompts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, ".tasks", "evidence.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, ".tasks", "manifest.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	content := strings.Repeat("source-backed content. ", 20) +
		"\n[source](../../../input/notes.md)\n[evidence](../evidence.json)\n[manifest](../manifest.json)\n"
	path := filepath.Join(outputDir, ".tasks/prompts/product.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	issues := validateDraftProgrammatic(outputDir, expectedFiles, outputDir, inputDir)
	if len(issues) != 0 {
		t.Fatalf("expected allowed-root links to pass, got: %#v", issues)
	}
}

func TestValidateDraftProgrammaticAllowsAbsoluteLinksIntoConfiguredRoots(t *testing.T) {
	dir := t.TempDir()
	inputDir := filepath.Join(dir, "input")
	outputDir := filepath.Join(dir, "output")
	expectedFiles := []string{".tasks/prompts/product.md"}

	if err := os.MkdirAll(filepath.Join(outputDir, ".tasks", "prompts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(inputDir, "notes.md")
	if err := os.WriteFile(sourcePath, []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}

	content := "# Product\n\n- Goal. Source: [notes.md](" + sourcePath + ")\n" + strings.Repeat("source-backed content. ", 20)
	path := filepath.Join(outputDir, ".tasks/prompts/product.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	issues := validateDraftProgrammatic(outputDir, expectedFiles, outputDir, inputDir)
	if len(issues) != 0 {
		t.Fatalf("expected absolute links to pass, got: %#v", issues)
	}
}

func TestValidateDraftProgrammaticFlagsStatusNoteMetaCommentary(t *testing.T) {
	dir := t.TempDir()
	expectedFiles := []string{".tasks/prompts/product.md"}
	path := filepath.Join(dir, ".tasks/prompts/product.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := "Created .tasks/prompts/product.md with the required sections.\n\nIt now covers the source.\n"
	if err := os.WriteFile(path, []byte(content+strings.Repeat("meta text. ", 30)), 0644); err != nil {
		t.Fatal(err)
	}

	issues := validateDraftProgrammatic(dir, expectedFiles)
	if len(issues) == 0 {
		t.Fatal("expected status-note detection failure")
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Problem, "status-note/meta commentary") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected status-note/meta commentary issue, got %#v", issues)
	}
}

func TestValidateDraftProgrammaticCatchesLeakedTemplatePaths(t *testing.T) {
	leakCases := []struct {
		name    string
		content string
		wantMsg string
	}{
		{"absolute/path", "# Product\n\nSee [notes](absolute/path) for details.\n" + strings.Repeat("filler. ", 30), "absolute/path"},
		{"example/path", "# Product\n\nSee [file](example/path) for details.\n" + strings.Repeat("filler. ", 30), "example/path"},
		{"relative/path.md", "# Product\n\nSee relative/path.md for details.\n" + strings.Repeat("filler. ", 30), "relative/path.md"},
		{"FILENAME placeholder link", "# Product\n\nSource: [FILENAME](some/path)\n" + strings.Repeat("filler. ", 30), "[FILENAME]("},
		{"skill template block", "## Skill: <Skill Name>\n- Slug: <kebab-case slug>\n" + strings.Repeat("filler. ", 30), "<Skill Name>"},
		{"agent template block", "## Agent: <Agent Name>\n- Role: Observe\n" + strings.Repeat("filler. ", 30), "<Agent Name>"},
	}
	for _, tc := range leakCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			expectedFiles := []string{".tasks/prompts/product.md"}
			path := filepath.Join(dir, ".tasks/prompts/product.md")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			issues := validateDraftProgrammatic(dir, expectedFiles)
			found := false
			for _, issue := range issues {
				if strings.Contains(issue.Problem, "leaked template fragment") && strings.Contains(issue.Problem, tc.wantMsg) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected leaked template fragment %q to be caught, got: %#v", tc.wantMsg, issues)
			}
		})
	}
}

func TestValidateDraftProgrammaticPassesCleanFiles(t *testing.T) {
	dir := t.TempDir()
	writeLedgerFiles(t, dir, ledgerFiles)
	issues := validateDraftProgrammatic(dir, ledgerFiles)
	for _, issue := range issues {
		if strings.Contains(issue.Problem, "leaked template fragment") {
			t.Fatalf("clean ledger files should not trigger template leak: %#v", issue)
		}
	}
}

func TestSanitizeProjectNameNoShellMetachars(t *testing.T) {
	dangerous := []rune{'$', '`', '"', '\'', ';', '|', '&', '(', ')', '{', '}', '[', ']', '<', '>', '\\', '!', '#', '~', '^'}
	for _, ch := range dangerous {
		input := "safe" + string(ch) + "name"
		result := sanitizeProjectName(input)
		if strings.ContainsRune(result, ch) {
			t.Errorf("sanitizeProjectName(%q) still contains dangerous char %q: got %q", input, string(ch), result)
		}
	}
}

func TestRevisionRoundCount(t *testing.T) {
	tests := []struct {
		name      string
		revisions []revisionResult
		want      int
	}{
		{"empty", nil, 0},
		{"legacy no round field", []revisionResult{{File: "a.md", Round: 0}}, 1},
		{"single round", []revisionResult{{File: "a.md", Round: 1}}, 1},
		{"two rounds", []revisionResult{
			{File: "a.md", Round: 1},
			{File: "b.md", Round: 1},
			{File: "b.md", Round: 2},
		}, 2},
		{"three rounds", []revisionResult{
			{File: "a.md", Round: 1},
			{File: "a.md", Round: 2},
			{File: "a.md", Round: 3},
		}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := revisionRoundCount(tt.revisions)
			if got != tt.want {
				t.Errorf("revisionRoundCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestValidationReportShowsRevisionRounds(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}
	report := &validationReport{
		promptPackName:   "swarmmaker-default",
		promptPackSource: "embedded:prompts/default_pack.json",
		promptPackDigest: "sha256:test",
		promptPackReview: prompts.PackReview{Approved: true},
		evidencePath:     filepath.Join(tasksDir, "evidence.json"),
		irManifestPath:   filepath.Join(tasksDir, "manifest.json"),
		reviewVerdict:    "revise",
		reviewOutput:     "### Verdict\nREVISE\n",
		revisions: []revisionResult{
			{File: "a.md", Success: true, Duration: time.Second, Chars: 100, Round: 1},
			{File: "b.md", Success: true, Duration: time.Second, Chars: 200, Round: 1},
			{File: "b.md", Success: true, Duration: time.Second, Chars: 250, Round: 2},
		},
		postScreen: &swarm.PreScreenResult{NeedsLLMReview: false},
	}
	for _, path := range []string{report.evidencePath, report.irManifestPath} {
		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeValidationReport(tasksDir, report); err != nil {
		t.Fatalf("writeValidationReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(tasksDir, "validation-report.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "## Revisions (2 round(s))") {
		t.Errorf("expected '## Revisions (2 round(s))' in report:\n%s", text)
	}
	if !strings.Contains(text, "[round 1]") {
		t.Errorf("expected '[round 1]' in report:\n%s", text)
	}
	if !strings.Contains(text, "[round 2]") {
		t.Errorf("expected '[round 2]' in report:\n%s", text)
	}
}

func TestMaxRevisionRoundsConstant(t *testing.T) {
	if maxRevisionRounds != 3 {
		t.Errorf("maxRevisionRounds = %d, want 3", maxRevisionRounds)
	}
}

func TestWriteValidationReportPassesAfterSuccessfulRevision(t *testing.T) {
	dir := t.TempDir()
	report := &validationReport{
		promptPackName:   "swarmmaker-default",
		promptPackSource: "embedded:prompts/default_pack.json",
		promptPackDigest: "sha256:test",
		promptPackReview: prompts.PackReview{Approved: true},
		evidencePath:     filepath.Join(dir, ".tasks", "evidence.json"),
		irManifestPath:   filepath.Join(dir, ".tasks", "manifest.json"),
		reviewVerdict:    "revise",
		reviewOutput:     "### Verdict\nREVISE\n",
		revisions: []revisionResult{
			{File: ".tasks/prompts/tools.md", Success: true, Duration: time.Second, Chars: 1234},
		},
		postScreen: &swarm.PreScreenResult{NeedsLLMReview: false},
	}
	if err := os.MkdirAll(filepath.Dir(report.evidencePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report.evidencePath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(report.irManifestPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := writeValidationReport(filepath.Join(dir, ".tasks"), report); err != nil {
		t.Fatalf("writeValidationReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".tasks", "validation-report.md"))
	if err != nil {
		t.Fatalf("read validation-report.md: %v", err)
	}
	if !strings.Contains(string(content), "**Decision**: PASS") {
		t.Fatalf("expected PASS decision after successful revisions:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(dir, "validation-report.md")); !os.IsNotExist(err) {
		t.Fatalf("did not expect top-level validation report, got: %v", err)
	}
}

func TestWriteValidationReportShowsAutomaticPreScreenRouting(t *testing.T) {
	dir := t.TempDir()
	report := &validationReport{
		promptPackName:   "swarmmaker-default",
		promptPackSource: "embedded:prompts/default_pack.json",
		promptPackDigest: "sha256:test",
		promptPackReview: prompts.PackReview{Approved: true},
		evidencePath:     filepath.Join(dir, ".tasks", "evidence.json"),
		irManifestPath:   filepath.Join(dir, ".tasks", "manifest.json"),
		preScreen: &swarm.PreScreenResult{
			NeedsLLMReview: true,
			Reasons:        []string{".tasks/tasks.md: low citation density"},
			FileFlags: map[string][]string{
				".tasks/tasks.md": {"low citation density"},
			},
		},
	}
	if err := os.MkdirAll(filepath.Join(dir, ".tasks"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{report.evidencePath, report.irManifestPath} {
		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeValidationReport(filepath.Join(dir, ".tasks"), report); err != nil {
		t.Fatalf("writeValidationReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".tasks", "validation-report.md"))
	if err != nil {
		t.Fatalf("read validation-report.md: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"**Concrete file flags**: 1",
		"Concrete file flags are routed into adversarial review and targeted revision automatically.",
		"**Verdict**: NOT RUN",
		"**Decision**: FAIL",
		"Concrete pre-screen findings were not ignored; they were fed into adversarial review and any unresolved findings would fail the final decision gate.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("validation report missing %q:\n%s", want, text)
		}
	}
}

func TestWriteValidationReportIncludesRenderParityFailures(t *testing.T) {
	dir := t.TempDir()
	report := &validationReport{
		promptPackName:   "swarmmaker-default",
		promptPackSource: "embedded:prompts/default_pack.json",
		promptPackDigest: "sha256:test",
		promptPackReview: prompts.PackReview{Approved: true},
		evidencePath:     filepath.Join(dir, ".tasks", "evidence.json"),
		irManifestPath:   filepath.Join(dir, ".tasks", "manifest.json"),
		reviewVerdict:    "approve",
		renderParity: []swarm.Issue{
			{File: ".codex/instructions/finance-intake.md", Problem: "skill \"finance-intake\" content drifted from .tasks blueprint", Severity: "error"},
		},
	}
	if err := os.MkdirAll(filepath.Join(dir, ".tasks"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{report.evidencePath, report.irManifestPath} {
		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeValidationReport(filepath.Join(dir, ".tasks"), report); err != nil {
		t.Fatalf("writeValidationReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".tasks", "validation-report.md"))
	if err != nil {
		t.Fatalf("read validation-report.md: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"## Render Parity",
		".codex/instructions/finance-intake.md: skill \"finance-intake\" content drifted from .tasks blueprint",
		"**Decision**: FAIL",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("validation report missing %q:\n%s", want, text)
		}
	}
}

func writeLedgerFiles(t *testing.T, dir string, relPaths []string) {
	t.Helper()
	sourcePath := filepath.Join(dir, "input", "notes.md")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("source-backed notes"), 0644); err != nil {
		t.Fatal(err)
	}
	contents := sampleLedgerContents(sourcePath)
	for _, rel := range relPaths {
		content, ok := contents[rel]
		if !ok {
			content = "# " + rel + "\n\n" + strings.Repeat("source-backed content. ", 25)
		}
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleLedgerContents(sourcePath string) map[string]string {
	sourceLink := "Source: [notes.md](" + filepath.ToSlash(sourcePath) + ")"
	return map[string]string{
		".tasks/context.md": strings.Join([]string{
			"# SwarmMaker - .tasks Context",
			"",
			"## Ledger Objective",
			"- Build a skill bundle from loose notes. " + sourceLink,
			"",
			"## Source Evidence Map",
			"- The ledger uses source notes plus `.tasks/evidence.json` and `.tasks/manifest.json`. " + sourceLink,
			"",
			"## Target Bundle Tree",
			"- Emit `.tasks/`, one hidden platform root, `README.md`, and `install.sh`. " + sourceLink,
			"",
			"## OODA Agent Roles",
			"- Observe inventories evidence, Orient decomposes work, Decide applies validation gates, Act renders the final bundle. " + sourceLink,
			"",
			strings.Repeat("Evidence-backed context. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/tasks.md": strings.Join([]string{
			"# SwarmMaker - .tasks Decomposition",
			"",
			"## Source-Derived User Goals",
			"- Convert loose notes into an installable skill bundle. " + sourceLink,
			"",
			"## Skill And Agent Capabilities",
			"- Skills must decompose analysis work and agents must coordinate it. " + sourceLink,
			"",
			"## Validation Requirements",
			"- Validate links, manifests, routing, and rendered bundle shape. " + sourceLink,
			"",
			strings.Repeat("Decomposition detail. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/prompts/product.md": strings.Join([]string{
			"# SwarmMaker - Product Prompt",
			"",
			"## Product Goal",
			"- Produce a SKILL bundle rooted in `.tasks/`. " + sourceLink,
			"",
			"## Output Expectations",
			"- Final output includes `.tasks/`, one selected platform root, `README.md`, and `install.sh`. " + sourceLink,
			"",
			strings.Repeat("Product prompt detail. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/prompts/technical.md": strings.Join([]string{
			"# SwarmMaker - Technical Prompt",
			"",
			"## Pipeline",
			"- Ingest, normalize, decompose, critique, revise, validate, and render. " + sourceLink,
			"",
			"## Output Renderer Contract",
			"- Render from `.tasks/` into a hidden selected-platform subtree. " + sourceLink,
			"",
			strings.Repeat("Technical prompt detail. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/prompts/tools.md": strings.Join([]string{
			"# Tool Synthesis Prompt",
			"",
			"## Tool Requests",
			"- Decision: UNKNOWN until the source justifies a generated helper tool. " + sourceLink,
			"",
			"## No-Tool Cases",
			"- Keep prompt-only behavior when no helper tool is justified. " + sourceLink,
			"",
			strings.Repeat("Tool prompt detail. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/prompts/deployment.md": strings.Join([]string{
			"# SwarmMaker - Operational Validation And Packaging",
			"",
			"## Output Tree Acceptance",
			"- Bundle must include `.tasks/`, one hidden platform root, `README.md`, and `install.sh`. " + sourceLink,
			"",
			"## Release Checks",
			"- Run `go test`, `go vet`, and CLI end-to-end validation. " + sourceLink,
			"",
			strings.Repeat("Deployment prompt detail. "+sourceLink+"\n", 6),
		}, "\n"),
		".tasks/todo.md": strings.Join([]string{
			"# SwarmMaker - .tasks Delivery Todo",
			"",
			"## Queue Rules",
			"- [ ] **Observe: Inventory sources** - Build: capture readable files Verify: compare counts Evidence: `.tasks/evidence.json` Source: " + sourceLink,
			"- [ ] **Orient: Build decomposition** - Build: write `.tasks/skills.md` and `.tasks/agents.md` Verify: parse blocks Evidence: `.tasks/manifest.json` Source: " + sourceLink,
			"- [ ] **Decide: Validate bundle contract** - Build: check selected renderer Verify: hidden root only Evidence: `.tasks/validation-report.md` Source: " + sourceLink,
			"- [ ] **Act: Render bundle** - Build: write platform tree Verify: install script copies `.tasks/` and hidden root Evidence: `README.md` Source: " + sourceLink,
			"",
			strings.Repeat("- [ ] **Iterate** - Build: refine ledger Verify: rerun checks Evidence: `.tasks/validation-report.md` Source: "+sourceLink+"\n", 4),
		}, "\n"),
		".tasks/skills.md": strings.Join([]string{
			"# SwarmMaker - Skill Decomposition",
			"",
			"## Skill: Finance Intake",
			"- Slug: finance-intake",
			"- Summary: Intake and normalize finance analysis requests into bundle-ready work plans.",
			"",
			"Use this skill to turn loose finance notes into structured work. " + sourceLink,
			"Consumers: final selected-platform playbooks and instructions. " + sourceLink,
			"Boundaries: do not invent unsupported APIs, credentials, or model capabilities. " + sourceLink,
			"Shared capabilities: routing, critique, revision, validation, and tool-use decisions must remain evidence-backed. " + sourceLink,
			"Final skill tree inputs: `.tasks/context.md`, `.tasks/tasks.md`, `.tasks/prompts/product.md`, `.tasks/prompts/technical.md`. " + sourceLink,
			"UNKNOWN gate: any missing data-provider contract remains UNKNOWN and blocks generated tool claims. " + sourceLink,
		}, "\n"),
		".tasks/agents.md": strings.Join([]string{
			"# SwarmMaker - Agent Decomposition",
			"",
			"## Agent: Observe Intake",
			"- Role: Observe",
			"",
			"This agent inventories source notes, evidence ledgers, and blocked assumptions before render. " + sourceLink,
			"OODA handoff: send normalized facts to Orient and blocked UNKNOWNs to Decide. " + sourceLink,
			"Coordination rules: critique findings flow back through Decide before Act writes output. " + sourceLink,
			"Final agent tree inputs: `.tasks/context.md`, `.tasks/tasks.md`, `.tasks/skills.md`, `.tasks/todo.md`. " + sourceLink,
			"UNKNOWN gate: missing install targets remain UNKNOWN until deployment rules are explicit. " + sourceLink,
		}, "\n"),
	}
}

func TestDumpValidationReportToWriter(t *testing.T) {
	var buf bytes.Buffer
	r := &validationReport{
		reviewVerdict: "approve",
		programmatic:  []swarm.Issue{{File: "f.md", Problem: "bad", Severity: "error"}},
		preScreen: &swarm.PreScreenResult{
			NeedsLLMReview: true,
			Reasons:        []string{"concrete issue"},
			FileFlags:      map[string][]string{"file.md": {"concrete issue"}},
		},
	}
	dumpValidationReportToWriter(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "verdict=approve") {
		t.Errorf("expected verdict in output, got: %s", out)
	}
	if !strings.Contains(out, "programmatic_errors=1") {
		t.Errorf("expected programmatic_errors=1 in output, got: %s", out)
	}
	if !strings.Contains(out, "concrete_flags=true") {
		t.Errorf("expected concrete_flags=true in output, got: %s", out)
	}
	if !strings.Contains(out, "passed=false") {
		t.Errorf("expected passed=false in output, got: %s", out)
	}
}

func TestDumpValidationReportToWriterNilReport(t *testing.T) {
	var buf bytes.Buffer
	dumpValidationReportToWriter(&buf, nil)
	if !strings.Contains(buf.String(), "nil report") {
		t.Errorf("expected nil report message, got: %s", buf.String())
	}
}

func TestValidationReportHasRiskAnalysis(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a skills.md with numbered process steps
	skillsContent := "## Skill: Ingest\n\n1. Read input files.\n2. Parse headers.\n3. Validate schema.\n4. Emit records.\n\n## Skill: Analyze\n\n1. Correlate events.\n2. Score severity.\n3. Classify priority.\n"
	if err := os.WriteFile(filepath.Join(tasksDir, "skills.md"), []byte(skillsContent), 0644); err != nil {
		t.Fatal(err)
	}
	report := &validationReport{
		promptPackName:   "swarmmaker-default",
		promptPackSource: "embedded:prompts/default_pack.json",
		promptPackDigest: "sha256:test",
		promptPackReview: prompts.PackReview{Approved: true},
		evidencePath:     filepath.Join(tasksDir, "evidence.json"),
		irManifestPath:   filepath.Join(tasksDir, "manifest.json"),
		reviewVerdict:    "approve",
	}
	for _, path := range []string{report.evidencePath, report.irManifestPath} {
		if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeValidationReport(tasksDir, report); err != nil {
		t.Fatalf("writeValidationReport failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(tasksDir, "validation-report.md"))
	if err != nil {
		t.Fatalf("read validation-report.md: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"## Risk Analysis",
		"Total process steps across all skills",
		"Estimated compound reliability at 95%",
		"Estimated compound reliability at 99%",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("validation report missing %q", want)
		}
	}
	// 7 total steps
	if !strings.Contains(text, "**Total process steps across all skills**: 7") {
		t.Errorf("expected 7 process steps in report, got:\n%s", text)
	}
}

func TestWriteValidationReportFailsOnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	report := &validationReport{
		reviewVerdict: "approve",
		programmatic:  []swarm.Issue{},
	}
	err := writeValidationReport(readOnlyDir, report)
	if err == nil {
		t.Fatal("expected error writing to read-only directory")
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("expected 'write' in error message, got: %v", err)
	}
}

func testBundle() renderedOutputBundle {
	return renderedOutputBundle{
		Blueprint: output.Blueprint{
			Name:    "TestProject",
			Purpose: "Testing atomic writes",
			Metadata: map[string]string{
				"format": "claude",
			},
			Agents: []output.Agent{{Name: "test", Role: "test", Instructions: "test"}},
			Skills: []output.Skill{{Name: "test", Slug: "test", Summary: "test skill", Body: "test body"}},
		},
		Manifests: []output.Manifest{
			{
				Format:  output.FormatClaude,
				RootDir: ".claude",
				Files: []output.FileArtifact{
					{Path: ".claude/README.md", Content: "# Test"},
					{Path: ".claude/skills/test/SKILL.md", Content: "# Skill"},
				},
			},
		},
	}
}

func TestAtomicWriteSuccess(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatal(err)
	}

	bundle := testBundle()
	formats := []output.Format{output.FormatClaude}
	err := writeRenderedOutputSwarms(outputDir, formats, "TestProject", "claude", "claude", bundle)
	if err != nil {
		t.Fatalf("writeRenderedOutputSwarms failed: %v", err)
	}

	// Verify all expected files exist.
	for _, path := range []string{
		".claude/README.md",
		".claude/skills/test/SKILL.md",
		"README.md",
		"install.sh",
	} {
		full := filepath.Join(outputDir, path)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", path)
		}
	}

	// Verify content.
	content, err := os.ReadFile(filepath.Join(outputDir, ".claude/README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Test" {
		t.Errorf("unexpected content: %s", content)
	}

	// Verify no staging dir remains.
	entries, _ := filepath.Glob(filepath.Join(dir, ".staging-*"))
	if len(entries) > 0 {
		t.Errorf("staging dir not cleaned up: %v", entries)
	}
}

func TestAtomicWriteNoPartialOnFailure(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Make the parent dir read-only so staging dir creation fails.
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755) // restore for cleanup

	bundle := testBundle()
	formats := []output.Format{output.FormatClaude}
	err := writeRenderedOutputSwarms(outputDir, formats, "TestProject", "claude", "claude", bundle)
	if err == nil {
		t.Fatal("expected error when staging dir cannot be created")
	}

	// Verify no manifest files were written to outputDir.
	if _, statErr := os.Stat(filepath.Join(outputDir, ".claude/README.md")); statErr == nil {
		t.Error("partial output should not exist in output dir after failure")
	}
}

func TestAtomicWritePreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a pre-existing user file.
	userFile := filepath.Join(outputDir, "user-notes.txt")
	if err := os.WriteFile(userFile, []byte("my notes"), 0644); err != nil {
		t.Fatal(err)
	}

	bundle := testBundle()
	formats := []output.Format{output.FormatClaude}
	err := writeRenderedOutputSwarms(outputDir, formats, "TestProject", "claude", "claude", bundle)
	if err != nil {
		t.Fatalf("writeRenderedOutputSwarms failed: %v", err)
	}

	// Verify user file still exists and is unchanged.
	content, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "my notes" {
		t.Errorf("user file was modified: %s", content)
	}
}

func TestValidationPassedTruthTable(t *testing.T) {
	noErrors := []swarm.Issue{}
	withErrors := []swarm.Issue{{File: "f.md", Problem: "bad", Severity: "error"}}

	cleanPreScreen := &swarm.PreScreenResult{NeedsLLMReview: false}
	advisoryPreScreen := &swarm.PreScreenResult{NeedsLLMReview: true, Reasons: []string{"advisory"}}
	concretePreScreen := &swarm.PreScreenResult{
		NeedsLLMReview: true,
		Reasons:        []string{"concrete issue"},
		FileFlags:      map[string][]string{"file.md": {"concrete issue"}},
	}

	cleanPostScreen := &swarm.PreScreenResult{NeedsLLMReview: false}
	dirtyPostScreen := &swarm.PreScreenResult{NeedsLLMReview: true}
	concretePostScreen := &swarm.PreScreenResult{
		NeedsLLMReview: false,
		FileFlags:      map[string][]string{"file.md": {"still flagged"}},
	}

	successRevision := []revisionResult{{File: "f.md", Success: true}}
	failedRevision := []revisionResult{{File: "f.md", Success: false}}

	tests := []struct {
		name string
		r    *validationReport
		want bool
	}{
		// nil report
		{"nil report", nil, false},

		// render errors block everything
		{"render error string blocks", &validationReport{
			renderError:   "boom",
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, false},
		{"render parity errors block", &validationReport{
			renderParity:  withErrors,
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, false},

		// programmatic errors block everything
		{"programmatic errors block approve", &validationReport{
			reviewVerdict: "approve",
			programmatic:  withErrors,
			preScreen:     cleanPreScreen,
		}, false},
		{"programmatic errors block revise", &validationReport{
			reviewVerdict: "revise",
			programmatic:  withErrors,
			preScreen:     cleanPreScreen,
			revisions:     successRevision,
			postScreen:    cleanPostScreen,
		}, false},

		// approve path: clean pre-screen
		{"approve + clean prescreen + no postscreen = pass", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, true},
		{"approve + clean prescreen + clean postscreen = pass", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			postScreen:    cleanPostScreen,
		}, true},
		{"approve + clean prescreen + dirty postscreen = fail", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			postScreen:    dirtyPostScreen,
		}, false},

		// approve path: advisory-only pre-screen (no concrete flags)
		{"approve + advisory prescreen + no postscreen = pass", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     advisoryPreScreen,
		}, true},

		// approve path: concrete pre-screen findings
		{"approve + concrete prescreen + no postscreen = FAIL", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     concretePreScreen,
		}, false},
		{"approve + concrete prescreen + clean postscreen = pass", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     concretePreScreen,
			postScreen:    cleanPostScreen,
		}, true},
		{"approve + concrete prescreen + dirty postscreen = fail", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     concretePreScreen,
			postScreen:    dirtyPostScreen,
		}, false},
		{"approve + concrete prescreen + concrete postscreen = fail", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     concretePreScreen,
			postScreen:    concretePostScreen,
		}, false},

		// revise path
		{"revise + success + clean postscreen = pass", &validationReport{
			reviewVerdict: "revise",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			revisions:     successRevision,
			postScreen:    cleanPostScreen,
		}, true},
		{"revise + failed revision = fail", &validationReport{
			reviewVerdict: "revise",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			revisions:     failedRevision,
			postScreen:    cleanPostScreen,
		}, false},
		{"revise + no revisions = fail", &validationReport{
			reviewVerdict: "revise",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			revisions:     nil,
			postScreen:    cleanPostScreen,
		}, false},
		{"revise + success + no postscreen = fail", &validationReport{
			reviewVerdict: "revise",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			revisions:     successRevision,
			postScreen:    nil,
		}, false},
		{"revise + success + dirty postscreen = fail", &validationReport{
			reviewVerdict: "revise",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
			revisions:     successRevision,
			postScreen:    dirtyPostScreen,
		}, false},

		// unknown/error verdicts
		{"unknown verdict = fail", &validationReport{
			reviewVerdict: "unknown",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, false},
		{"error verdict = fail", &validationReport{
			reviewVerdict: "error",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, false},
		{"empty verdict = fail", &validationReport{
			reviewVerdict: "",
			programmatic:  noErrors,
			preScreen:     cleanPreScreen,
		}, false},

		// nil pre-screen treated as clean (no concrete flags)
		{"approve + nil prescreen = pass", &validationReport{
			reviewVerdict: "approve",
			programmatic:  noErrors,
			preScreen:     nil,
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validationPassed(tt.r)
			if got != tt.want {
				t.Errorf("validationPassed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScanImplementationDecisions(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}

	// File with implementation decisions (both variants)
	if err := os.WriteFile(filepath.Join(tasksDir, "todo.md"), []byte(
		"Task 1: Source: Not specified - implementation decision\n"+
			"Task 2: Source: implementation-decision based\n"+
			"Task 3: Source: notes.md\n",
	), 0644); err != nil {
		t.Fatal(err)
	}

	// File with no implementation decisions
	if err := os.WriteFile(filepath.Join(tasksDir, "skills.md"), []byte(
		"Skill 1: Based on source notes.md\n",
	), 0644); err != nil {
		t.Fatal(err)
	}

	entries := scanImplementationDecisions(dir, []string{".tasks/todo.md", ".tasks/skills.md", ".tasks/missing.md"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].RelPath != ".tasks/todo.md" {
		t.Errorf("RelPath = %q, want .tasks/todo.md", entries[0].RelPath)
	}
	if entries[0].Category != ingestion.EvidenceCategoryImplementationDecision {
		t.Errorf("Category = %q, want implementation_decision", entries[0].Category)
	}
	if entries[0].Phase != ingestion.EvidencePhaseGeneration {
		t.Errorf("Phase = %q, want generation", entries[0].Phase)
	}
	if !strings.Contains(entries[0].Detail, "2 implementation decisions") {
		t.Errorf("Detail = %q, want count of 2", entries[0].Detail)
	}
}

func TestRewriteEvidenceManifestUpdatesFile(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, ".tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}

	ctx := &ingestion.Context{
		RootPath:   "input",
		FileCount:  1,
		TotalBytes: 12,
		Files: []ingestion.FileEntry{
			{RelPath: "notes.md", FileType: "markdown", Size: 12},
		},
		Evidence: []ingestion.EvidenceEntry{
			{
				Phase:    ingestion.EvidencePhaseIngestion,
				Category: ingestion.EvidenceCategoryHiddenPath,
				RelPath:  ".env",
				Detail:   "hidden path excluded",
			},
		},
	}

	// Write initial evidence
	if _, err := writeEvidenceManifest(tasksDir, ctx); err != nil {
		t.Fatal(err)
	}

	// Rewrite with new entries
	newEntries := []ingestion.EvidenceEntry{
		{
			Phase:    ingestion.EvidencePhaseGeneration,
			Category: ingestion.EvidenceCategoryImplementationDecision,
			RelPath:  ".tasks/todo.md",
			Detail:   "3 implementation decisions recorded",
		},
	}
	if err := rewriteEvidenceManifest(tasksDir, ctx, newEntries); err != nil {
		t.Fatalf("rewriteEvidenceManifest failed: %v", err)
	}

	// Verify the file contains both old and new evidence
	payload, err := os.ReadFile(filepath.Join(tasksDir, "evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, want := range []string{"hidden_path", "implementation_decision", "3 implementation decisions"} {
		if !strings.Contains(text, want) {
			t.Fatalf("evidence.json missing %q:\n%s", want, text)
		}
	}

	// Verify ctx.Evidence was updated
	if len(ctx.Evidence) != 2 {
		t.Fatalf("expected 2 evidence entries, got %d", len(ctx.Evidence))
	}
}

func TestRewriteEvidenceManifestNoopOnEmpty(t *testing.T) {
	dir := t.TempDir()
	ctx := &ingestion.Context{}
	if err := rewriteEvidenceManifest(dir, ctx, nil); err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}

func TestWriteRenderedOutputSwarmsRejectsPathTraversal(t *testing.T) {
	bundle := renderedOutputBundle{
		Blueprint: output.Blueprint{
			Name:    "test",
			Purpose: "test purpose",
			Metadata: map[string]string{
				"generator": "claude",
				"critic":    "codex",
			},
			Docs: []output.Document{
				{Path: ".tasks/context.md", Content: "doc content"},
			},
			Agents: []output.Agent{
				{Name: "lead", Role: "coordinator", Instructions: "coordinate work"},
			},
			Skills: []output.Skill{
				{Name: "Ingest", Slug: "ingest", Summary: "Normalize input docs", Body: "Read source files."},
			},
		},
		Manifests: []output.Manifest{
			{
				Format:  output.FormatClaude,
				RootDir: ".claude",
				Files: []output.FileArtifact{
					{Path: "../../../etc/passwd", Content: "malicious"},
				},
			},
		},
	}
	outputDir := t.TempDir()
	err := writeRenderedOutputSwarms(outputDir, []output.Format{output.FormatClaude}, "test", "claude", "codex", bundle)
	if err == nil {
		t.Fatal("expected path traversal to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "escapes staging directory") {
		t.Fatalf("error = %q, want 'escapes staging directory'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Input quality gate tests (basic sanity check)
// ---------------------------------------------------------------------------

func TestInputQualityGateRejectsEmptyDir(t *testing.T) {
	ctx := &ingestion.Context{
		Files:   nil,
		Summary: "",
	}
	complexity := &ingestion.SourceComplexity{SectionCount: 0}
	err := checkInputQualityGate(ctx, complexity)
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
	if !strings.Contains(err.Error(), "Insufficient") {
		t.Fatalf("error = %q, want 'Insufficient'", err.Error())
	}
	if !strings.Contains(err.Error(), "0 readable files") {
		t.Fatalf("error = %q, want '0 readable files'", err.Error())
	}
	if len(ctx.Evidence) != 1 || ctx.Evidence[0].Category != ingestion.EvidenceCategoryInputQualityGate {
		t.Fatalf("expected 1 input_quality_gate evidence entry, got %d", len(ctx.Evidence))
	}
}

func TestInputQualityGateRejectsZeroContent(t *testing.T) {
	ctx := &ingestion.Context{
		Files:   []ingestion.FileEntry{{RelPath: "empty.md", Content: ""}},
		Summary: "",
	}
	complexity := &ingestion.SourceComplexity{SectionCount: 5}
	err := checkInputQualityGate(ctx, complexity)
	if err == nil {
		t.Fatal("expected error for zero-content input, got nil")
	}
	if !strings.Contains(err.Error(), "0 chars") {
		t.Fatalf("error = %q, want '0 chars'", err.Error())
	}
}

func TestInputQualityGateAcceptsValidInput(t *testing.T) {
	ctx := &ingestion.Context{
		Files:   []ingestion.FileEntry{{RelPath: "a.md", Content: "some content"}},
		Summary: "some content",
	}
	complexity := &ingestion.SourceComplexity{SectionCount: 1}
	err := checkInputQualityGate(ctx, complexity)
	if err != nil {
		t.Fatalf("expected nil error for valid input, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Pre-flight prompt construction tests
// ---------------------------------------------------------------------------

func TestBuildPreFlightPromptIncludesMetrics(t *testing.T) {
	ctx := &ingestion.Context{
		FileCount:  3,
		TotalBytes: 5000,
		Summary:    "## Section A\nContent here.\n## Section B\nMore content.",
	}
	complexity := &ingestion.SourceComplexity{SectionCount: 5, Depth: "moderate"}
	prompt := buildPreFlightPrompt(ctx, complexity)

	for _, want := range []string{
		"Files: 3",
		"5000 bytes",
		"Sections/headings: 5",
		"Depth classification: moderate",
		"SUFFICIENT:",
		"INSUFFICIENT:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPreFlightPromptTruncatesLongSummary(t *testing.T) {
	longSummary := strings.Repeat("x", 5000)
	ctx := &ingestion.Context{
		FileCount:  1,
		TotalBytes: 5000,
		Summary:    longSummary,
	}
	complexity := &ingestion.SourceComplexity{SectionCount: 1, Depth: "shallow"}
	prompt := buildPreFlightPrompt(ctx, complexity)

	// The source content in the prompt should be truncated to preFlightSummaryLimit
	if strings.Contains(prompt, strings.Repeat("x", preFlightSummaryLimit+1)) {
		t.Error("prompt should truncate source content to preFlightSummaryLimit chars")
	}
	if !strings.Contains(prompt, strings.Repeat("x", preFlightSummaryLimit)) {
		t.Error("prompt should include preFlightSummaryLimit chars of source content")
	}
}

func TestBuildPreFlightPromptHandlesNilComplexity(t *testing.T) {
	ctx := &ingestion.Context{
		FileCount:  1,
		TotalBytes: 100,
		Summary:    "content",
	}
	prompt := buildPreFlightPrompt(ctx, nil)
	if !strings.Contains(prompt, "Sections/headings: 0") {
		t.Error("nil complexity should default to 0 sections")
	}
	if !strings.Contains(prompt, "Depth classification: unknown") {
		t.Error("nil complexity should default to 'unknown' depth")
	}
}
