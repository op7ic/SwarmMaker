// validate_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for pre-screening validation.
// Covers citation regex patterns, fabrication detection, boilerplate injection,
// amplification ratio, gap acknowledgment, dimension coverage, and blocking
// reason classification across shallow/moderate/deep source depths.


package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/op7ic/swarmmaker/internal/ingestion"
)

func TestErrorCountOnlyCountsErrors(t *testing.T) {
	issues := []Issue{
		{Severity: "error"},
		{Severity: "warning"},
		{Severity: "error"},
		{Severity: "warning"},
		{Severity: "error"},
	}
	if ErrorCount(issues) != 3 {
		t.Errorf("ErrorCount = %d, want 3", ErrorCount(issues))
	}
}

func TestErrorCountEmpty(t *testing.T) {
	if ErrorCount(nil) != 0 {
		t.Error("ErrorCount(nil) should be 0")
	}
	if ErrorCount([]Issue{}) != 0 {
		t.Error("ErrorCount([]) should be 0")
	}
}

// --- PreScreenFiles tests ---

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	path := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// --- Universal checks ---

func TestPreScreenPassesWithGoodCitationsNilComplexity(t *testing.T) {
	dir := t.TempDir()
	content := ""
	for i := 0; i < 20; i++ {
		content += "Some feature description here. Source: [Section " + strings.Repeat("x", 50) + "]\n"
	}
	writeFile(t, dir, ".tasks/tasks.md", content)

	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, nil, "some source text")
	if result.NeedsLLMReview {
		t.Errorf("expected pre-screen to pass with good citations, got flags: %v", result.Reasons)
	}
}

func TestPreScreenFlagsMissingCitationsNilComplexity(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("This is a feature without any source attribution. ", 50)
	writeFile(t, dir, ".tasks/tasks.md", content)

	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, nil, "source")
	if !result.NeedsLLMReview {
		t.Error("expected pre-screen to flag missing citations with nil complexity")
	}
	hasFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "citation") {
			hasFlag = true
		}
	}
	if !hasFlag {
		t.Errorf("expected citation flag, got: %v", result.Reasons)
	}
}

func TestPreScreenUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	result := PreScreenFiles(dir, []string{"nonexistent.md"}, nil, "")
	if !result.NeedsLLMReview {
		t.Error("expected pre-screen to flag unreadable file")
	}
}

func TestPreScreenNilComplexityNoPanic(t *testing.T) {
	dir := t.TempDir()
	content := ""
	for i := 0; i < 20; i++ {
		content += "Detail. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/tasks.md", content)
	// Should not panic with nil complexity
	_ = PreScreenFiles(dir, []string{".tasks/tasks.md"}, nil, "source")
}

func TestPreScreenFabricationNotFlaggedWhenInSource(t *testing.T) {
	dir := t.TempDir()
	// curl command IS in the source — should not be flagged
	source := "To test the API: curl https://api.example.com/v1/init"
	content := "## Setup\ncurl https://api.example.com/v1/init\nSource: [Setup]\n"
	for i := 0; i < 5; i++ {
		content += "Detail. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/technical.md", content)

	result := PreScreenFiles(dir, []string{".tasks/prompts/technical.md"}, nil, source)
	for _, r := range result.Reasons {
		if strings.Contains(r, "fabrication signal") && strings.Contains(r, "curl") {
			t.Errorf("curl command from source was incorrectly flagged: %s", r)
		}
	}
}

func TestPreScreenFabricationFlaggedWhenNotInSource(t *testing.T) {
	dir := t.TempDir()
	// curl command NOT in source — should be flagged
	source := "Build a weather dashboard with real-time data"
	content := "## Setup\ncurl https://api.weather.io/v1/data\nSource: [Setup]\n"
	for i := 0; i < 5; i++ {
		content += "Detail. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/technical.md", content)

	result := PreScreenFiles(dir, []string{".tasks/prompts/technical.md"}, nil, source)
	hasFabFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "fabrication signal") {
			hasFabFlag = true
		}
	}
	if !hasFabFlag {
		t.Errorf("expected fabrication flag for curl not in source, got: %v", result.Reasons)
	}
}

// --- Shallow source checks (messy notes, screenshots) ---

func TestPreScreenShallowFlagsHighAmplification(t *testing.T) {
	dir := t.TempDir()
	source := "Build a todo app with user login" // 32 chars
	// Output is >4x source length — suspicious for shallow source
	content := strings.Repeat("Feature detail. Source: [notes]\n", 20) // ~640 chars
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasAmpFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "amplification") {
			hasAmpFlag = true
		}
	}
	if !hasAmpFlag {
		t.Errorf("expected amplification flag for shallow source, got: %v", result.Reasons)
	}
}

func TestPreScreenShallowFlagsMissingGapAcknowledgments(t *testing.T) {
	dir := t.TempDir()
	source := "Build a trading platform"
	// Long output that fills every section — never acknowledges gaps
	content := strings.Repeat("Detailed tech stack analysis and deployment plan. Source: [notes]\n", 30)
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasGapFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "gap acknowledgment") {
			hasGapFlag = true
		}
	}
	if !hasGapFlag {
		t.Errorf("expected 'gap acknowledgment' flag for shallow source without fallbacks, got: %v", result.Reasons)
	}
}

func TestPreScreenShallowAlwaysReviewsButNoPerFileFlags(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("Build a trading platform with Bloomberg data. ", 10) // ~460 chars
	// Output uses gap acknowledgments correctly and has citations — healthy output
	content := "## Features\nBloomberg data integration. Source: [notes]\n"
	content += "## Tech Stack\nNot specified in source material.\n"
	content += "## Deployment\nNot specified in source material.\n"
	content += "Source: [user notes]\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	// Shallow sources always trigger review (policy)
	if !result.NeedsLLMReview {
		t.Error("expected shallow source to always trigger review")
	}
	// But clean output should have no per-file flags
	if len(result.FileFlags) > 0 {
		t.Errorf("expected no per-file flags for clean shallow output, got: %v", result.FileFlags)
	}
}

func TestPreScreenShallowFlagsBoilerplateInjection(t *testing.T) {
	dir := t.TempDir()
	source := "Build an app that tracks inventory" // no mention of enterprise infra
	// Output injects enterprise boilerplate not in source
	content := "Use a comprehensive test suite with CI/CD pipeline.\n"
	content += "Deploy with containerization and microservices architecture.\n"
	content += "Add monitoring and alerting, horizontal scaling, and load balancing.\n"
	content += "Source: [notes]\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasBPFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "boilerplate") {
			hasBPFlag = true
		}
	}
	if !hasBPFlag {
		t.Errorf("expected boilerplate injection flag, got: %v", result.Reasons)
	}
}

func TestPreScreenShallowBoilerplateNotFlaggedWhenInSource(t *testing.T) {
	dir := t.TempDir()
	// Source explicitly mentions these terms — not boilerplate
	source := "Build an app with CI/CD pipeline, microservices, containerization, and monitoring and alerting"
	content := "Use CI/CD pipeline with microservices and containerization.\n"
	content += "Add monitoring and alerting.\nSource: [notes]\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	for _, r := range result.Reasons {
		if strings.Contains(r, "boilerplate") {
			t.Errorf("boilerplate incorrectly flagged when terms ARE in source: %s", r)
		}
	}
}

func TestPreScreenShallowLenientCitations(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("Build a todo app with features. ", 20) // ~640 chars
	// Just 1 citation — should pass for shallow source (lenient threshold)
	content := "Feature list from notes. Source: [user notes]\nMore details here.\n"
	content += "Not specified in source material.\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: len(source)}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	for _, r := range result.Reasons {
		if strings.Contains(r, "citation") {
			t.Errorf("shallow source with 1 citation should pass lenient check, got: %s", r)
		}
	}
}

// --- Deep source checks (structured specs) ---

func TestPreScreenDeepFlagsDimensionGaps(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 50000) // deep source
	content := "## Dimension 1\nSource: [Dim 1]\n## Dimension 2\nSource: [Dim 2]\n## Dimension 3\nSource: [Dim 3]\n"
	for i := 0; i < 20; i++ {
		content += "Detail. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{DimensionCount: 10, Depth: "deep", SourceLength: 50000}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasDimFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "dimension gap") {
			hasDimFlag = true
		}
	}
	if !hasDimFlag {
		t.Errorf("expected dimension gap flag, got: %v", result.Reasons)
	}
}

func TestPreScreenDeepFlagsLowCitationDensity(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 50000)
	// Long output but only 1 citation — bad for deep source
	content := strings.Repeat("Detailed analysis without proper attribution. ", 100)
	content += "\nSource: [Section 1]\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "deep", SourceLength: 50000}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasCitFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "citation density") {
			hasCitFlag = true
		}
	}
	if !hasCitFlag {
		t.Errorf("expected low citation density flag for deep source, got: %v", result.Reasons)
	}
}

func TestPreScreenDeepFlagsShortOutput(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 50000)
	content := "Short.\nSource: [A]\nSource: [B]\nSource: [C]\n"
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "deep", SourceLength: 50000}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	if !result.NeedsLLMReview {
		t.Error("expected pre-screen to flag short file for deep source")
	}
}

// --- Moderate source checks ---

func TestPreScreenModerateFlagsLowCitations(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 15000)
	// Long output, zero citations — should flag
	content := strings.Repeat("Content without citation. ", 100)
	writeFile(t, dir, ".tasks/tasks.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "moderate", SourceLength: 15000}
	result := PreScreenFiles(dir, []string{".tasks/tasks.md"}, complexity, source)

	hasCitFlag := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "citation density") {
			hasCitFlag = true
		}
	}
	if !hasCitFlag {
		t.Errorf("expected citation density flag for moderate source, got: %v", result.Reasons)
	}
}

// --- FlaggedFiles tests ---

// --- Citation regex tests (Fix 3: backtick citations) ---

func TestCitationRegexMatchesBracketStyle(t *testing.T) {
	text := "Source: [Section Name]"
	if !citationRe.MatchString(text) {
		t.Error("citationRe should match bracket-style citation")
	}
}

func TestCitationRegexMatchesUppercaseStyle(t *testing.T) {
	text := "Source: DIMENSION 1"
	if !citationRe.MatchString(text) {
		t.Error("citationRe should match uppercase-style citation")
	}
}

func TestCitationRegexMatchesBacktickStyle(t *testing.T) {
	text := "Source: `DIMENSION 1`"
	if !citationRe.MatchString(text) {
		t.Error("citationRe should match backtick-style citation")
	}
}

// --- Fabrication regex tests (Fix 4: case-sensitive ticker) ---

func TestTickerFabricationCaseSensitive(t *testing.T) {
	// Should match actual uppercase ticker symbols
	tickerPat := fabricationPatterns[1] // ticker pattern
	if !tickerPat.MatchString("AAPL ticker") {
		t.Error("should match uppercase AAPL ticker")
	}
	// Should NOT match lowercase English words that happen to precede "ticker"
	falsePositives := []string{
		"are ticker", "one ticker", "and ticker", "using ticker", "the ticker",
	}
	for _, fp := range falsePositives {
		if tickerPat.MatchString(fp) {
			t.Errorf("should not match lowercase %q as fabricated ticker", fp)
		}
	}
}

func TestPreScreenFlaggedFilesReturnsOnlyBadFiles(t *testing.T) {
	dir := t.TempDir()
	goodContent := ""
	for i := 0; i < 20; i++ {
		goodContent += "Detail here. Source: [Section " + strings.Repeat("x", 50) + "]\n"
	}
	badContent := strings.Repeat("No citations here at all. ", 50)

	writeFile(t, dir, ".tasks/tasks.md", goodContent)
	writeFile(t, dir, ".tasks/prompts/technical.md", badContent)

	result := PreScreenFiles(dir, []string{".tasks/tasks.md", ".tasks/prompts/technical.md"}, nil, "source")
	flagged := result.FlaggedFiles()
	if len(flagged) != 1 {
		t.Errorf("expected 1 flagged file, got %d: %v", len(flagged), flagged)
	}
	if len(flagged) == 1 && flagged[0] != ".tasks/prompts/technical.md" {
		t.Errorf("expected technical to be flagged, got %s", flagged[0])
	}
}

func TestPreScreenBlockingReasonsIgnoreAdvisoryOnlyReview(t *testing.T) {
	result := &PreScreenResult{
		NeedsLLMReview: true,
		Reasons:        []string{"shallow source: always review (high fabrication risk)"},
		FileFlags:      map[string][]string{},
	}
	if result.HasConcreteFlags() {
		t.Fatal("expected advisory-only shallow review to be non-blocking")
	}
	if got := result.ConcreteReasons(); len(got) != 0 {
		t.Fatalf("expected no blocking reasons, got %v", got)
	}
}

func TestPreScreenBlockingReasonsReturnConcreteFlagsInOrder(t *testing.T) {
	result := &PreScreenResult{
		NeedsLLMReview: true,
		FileFlags: map[string][]string{
			".tasks/prompts/technical.md": {"low citation density"},
			".tasks/tasks.md":             {"dimension gap", "fabrication signal"},
		},
	}
	if !result.HasConcreteFlags() {
		t.Fatal("expected concrete file flags to be blocking")
	}
	got := result.ConcreteReasons()
	want := []string{
		".tasks/prompts/technical.md: low citation density",
		".tasks/tasks.md: dimension gap",
		".tasks/tasks.md: fabrication signal",
	}
	if len(got) != len(want) {
		t.Fatalf("ConcreteReasons() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ConcreteReasons()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// --- Operational depth advisory checks ---

func TestPreScreenSkillsDepthAdvisory(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 15000)
	// skills.md without process steps, constraints, or trigger conditions
	content := "## Overview\nThis skill does stuff.\n"
	for i := 0; i < 10; i++ {
		content += "Detail here. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/skills.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "moderate", SourceLength: 15000}
	result := PreScreenFiles(dir, []string{".tasks/prompts/skills.md"}, complexity, source)

	if !result.NeedsLLMReview {
		t.Error("expected advisory flags to trigger NeedsLLMReview")
	}

	wantAdvisories := []string{"missing procedural steps", "missing constraints", "missing trigger conditions"}
	for _, want := range wantAdvisories {
		found := false
		for _, r := range result.Reasons {
			if strings.Contains(r, want) && strings.Contains(r, "[advisory]") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected advisory containing %q, got reasons: %v", want, result.Reasons)
		}
	}

	// Advisory flags should NOT appear in FileFlags (not concrete/blocking)
	if result.HasConcreteFlags() {
		t.Errorf("advisory flags should not be concrete, got FileFlags: %v", result.FileFlags)
	}
}

func TestPreScreenSkillsDepthClean(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 15000)
	// skills.md WITH process steps, constraints, and trigger conditions
	content := "## Process\n1. First step\n2. Second step\n"
	content += "Constraints: MUST DO validation before proceeding.\n"
	content += "When to Invoke: triggered on new input.\n"
	for i := 0; i < 10; i++ {
		content += "Detail here. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/skills.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "deep", SourceLength: 50000}
	result := PreScreenFiles(dir, []string{".tasks/prompts/skills.md"}, complexity, source)

	for _, r := range result.Reasons {
		if strings.Contains(r, "[advisory]") && strings.Contains(r, "skills.md") {
			t.Errorf("expected no advisory flags for complete skills.md, got: %s", r)
		}
	}
}

func TestPreScreenAgentsDepthAdvisory(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 15000)
	// agents.md without coordination protocol or error handling
	// Must be >1500 chars to avoid deep-source "suspiciously short" concrete flag
	content := "## Overview\nAgent definitions.\n"
	for i := 0; i < 60; i++ {
		content += "Detail here. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/agents.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "deep", SourceLength: 50000}
	result := PreScreenFiles(dir, []string{".tasks/prompts/agents.md"}, complexity, source)

	if !result.NeedsLLMReview {
		t.Error("expected advisory flags to trigger NeedsLLMReview")
	}

	wantAdvisories := []string{"missing coordination protocol", "missing error handling"}
	for _, want := range wantAdvisories {
		found := false
		for _, r := range result.Reasons {
			if strings.Contains(r, want) && strings.Contains(r, "[advisory]") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected advisory containing %q, got reasons: %v", want, result.Reasons)
		}
	}

	// Advisory flags should NOT be concrete
	if result.HasConcreteFlags() {
		t.Errorf("advisory flags should not be concrete, got FileFlags: %v", result.FileFlags)
	}
}

func TestPreScreenAgentsDepthClean(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 15000)
	// agents.md WITH coordination protocol and error handling
	content := "## Coordination Protocol\nHandoff between agents.\n"
	content += "## Error Handling\nOn failure, retry with backoff.\n"
	for i := 0; i < 10; i++ {
		content += "Detail here. Source: [Section X]\n"
	}
	writeFile(t, dir, ".tasks/prompts/agents.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "moderate", SourceLength: 15000}
	result := PreScreenFiles(dir, []string{".tasks/prompts/agents.md"}, complexity, source)

	for _, r := range result.Reasons {
		if strings.Contains(r, "[advisory]") && strings.Contains(r, "agents.md") {
			t.Errorf("expected no advisory flags for complete agents.md, got: %s", r)
		}
	}
}

func TestPreScreenDepthAdvisorySkippedForShallow(t *testing.T) {
	dir := t.TempDir()
	source := strings.Repeat("x", 500)
	// skills.md without process steps — but shallow source should skip depth checks
	content := "## Overview\nThis skill does stuff.\nSource: [notes]\n"
	content += "Not specified in source material.\n"
	writeFile(t, dir, ".tasks/prompts/skills.md", content)

	complexity := &ingestion.SourceComplexity{Depth: "shallow", SourceLength: 500}
	result := PreScreenFiles(dir, []string{".tasks/prompts/skills.md"}, complexity, source)

	for _, r := range result.Reasons {
		if strings.Contains(r, "[advisory]") {
			t.Errorf("shallow source should not trigger depth advisory checks, got: %s", r)
		}
	}
}

func TestCitationRegexMatchesBothSingularAndPlural(t *testing.T) {
	cases := []struct {
		input string
		match bool
	}{
		{"Source: [file.md](/path/file.md)", true},
		{"Sources: [a.md](/a), [b.md](/b)", true},
		{"Source: Architecture doc says...", true},
		{"Sources: Architecture and notes", true},
		{"Source: `manifest.json`", true},
		{"Sources: `manifest.json`, `evidence.json`", true},
		{"no citation here", false},
		{"some source material", false},
	}
	for _, tc := range cases {
		matches := citationRe.FindAllString(tc.input, -1)
		got := len(matches) > 0
		if got != tc.match {
			t.Errorf("citationRe on %q: got match=%v, want %v", tc.input, got, tc.match)
		}
	}
}
