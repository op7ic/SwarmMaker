// complexity_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for source complexity analysis.
// Covers deep/shallow/moderate classification, numerical density detection,
// dimension and appendix counting, and format hints generation.


package ingestion

import (
	"strings"
	"testing"
)

func TestAnalyzeComplexityDeepSource(t *testing.T) {
	// Simulate the earnings framework structure: 20 dimensions, 3 appendices, dense numbers
	var b strings.Builder
	b.WriteString("# Earnings Flow Framework v4\n\n")
	b.WriteString("## Operating Philosophy\n")
	b.WriteString("No single observation predicts anything. <60 DTE is strongest signal.\n\n")

	for i := 1; i <= 20; i++ {
		b.WriteString("## Dimension " + itoa(i) + ": Signal Category\n")
		b.WriteString("- Rule 1: threshold > 0.75\n")
		b.WriteString("- Rule 2: volume_oi_ratio > 2.0\n")
		b.WriteString("- Rule 3: premium > $50,000\n\n")
	}

	b.WriteString("## Appendix A: Endpoint Matrix\n")
	b.WriteString("47 endpoints listed below.\n\n")
	b.WriteString("## Appendix B: Investigation Report Template\n")
	b.WriteString("Field: Market Cap Tier\n\n")
	b.WriteString("## Appendix C: Workflow\n")
	b.WriteString("Step 1: Download. Step 2: Analyze.\n")

	source := b.String()
	c := AnalyzeComplexity(source)

	if c.DimensionCount != 20 {
		t.Errorf("DimensionCount = %d, want 20", c.DimensionCount)
	}
	if c.AppendixCount != 3 {
		t.Errorf("AppendixCount = %d, want 3", c.AppendixCount)
	}
	if c.Depth != "deep" {
		t.Errorf("Depth = %q, want %q", c.Depth, "deep")
	}
	if c.SectionCount < 20 {
		t.Errorf("SectionCount = %d, want >= 20", c.SectionCount)
	}
}

func TestAnalyzeComplexityShallowSource(t *testing.T) {
	source := "# Simple App\n\n## Features\n- Login\n- Dashboard\n\n## Tech\nNode.js\n"
	c := AnalyzeComplexity(source)

	if c.Depth != "shallow" {
		t.Errorf("Depth = %q, want %q", c.Depth, "shallow")
	}
	if c.DimensionCount != 0 {
		t.Errorf("DimensionCount = %d, want 0", c.DimensionCount)
	}
	if c.AppendixCount != 0 {
		t.Errorf("AppendixCount = %d, want 0", c.AppendixCount)
	}
}

func TestAnalyzeComplexityModerateSource(t *testing.T) {
	// 12K chars with 8 sections
	source := strings.Repeat("## Section\nSome content here with details.\n", 200)
	c := AnalyzeComplexity(source)

	if c.Depth != "moderate" && c.Depth != "deep" {
		t.Errorf("Depth = %q, want moderate or deep for 12K+ chars", c.Depth)
	}
}

func TestAnalyzeComplexityNumericalDensity(t *testing.T) {
	// Dense numbers: thresholds, percentages, counts
	source := "Threshold: 0.75. Volume > 1000. Premium $50000. Ratio 2.5. " +
		"Coverage 90%. Count 47. DTE < 60. Weight 40%. Max 8. Min 3."
	c := AnalyzeComplexity(source)

	if c.NumericalDensity < 5.0 {
		t.Errorf("NumericalDensity = %.1f, want > 5.0 for number-dense source", c.NumericalDensity)
	}
}

func TestAnalyzeComplexityListItems(t *testing.T) {
	source := "## Features\n- Item 1\n- Item 2\n- Item 3\n* Item 4\n1. Item 5\n2. Item 6\n"
	c := AnalyzeComplexity(source)

	if c.ListItemCount < 6 {
		t.Errorf("ListItemCount = %d, want >= 6", c.ListItemCount)
	}
}

func TestAnalyzeComplexityDeduplicatesDimensions(t *testing.T) {
	// Same dimension referenced multiple times
	source := "## Dimension 1: Flow\nDetails for Dim 1.\n" +
		"See Dimension 1 above.\n" +
		"## Dimension 2: Volume\nDetails.\n"
	c := AnalyzeComplexity(source)

	if c.DimensionCount != 2 {
		t.Errorf("DimensionCount = %d, want 2 (deduplicated)", c.DimensionCount)
	}
}

func TestAnalyzeComplexityDeduplicatesAppendices(t *testing.T) {
	source := "## Appendix A: Endpoints\nSee Appendix A.\n## Appendix B: Template\n"
	c := AnalyzeComplexity(source)

	if c.AppendixCount != 2 {
		t.Errorf("AppendixCount = %d, want 2 (deduplicated)", c.AppendixCount)
	}
}

func TestAnalyzeComplexityEmpty(t *testing.T) {
	c := AnalyzeComplexity("")

	if c.Depth != "shallow" {
		t.Errorf("Depth = %q, want shallow for empty source", c.Depth)
	}
	if c.SourceLength != 0 {
		t.Errorf("SourceLength = %d, want 0", c.SourceLength)
	}
}

func TestFormatHintsDeepSource(t *testing.T) {
	c := &SourceComplexity{
		SectionCount:     25,
		DimensionCount:   20,
		AppendixCount:    3,
		NumericalDensity: 12.5,
		ListItemCount:    150,
		SourceLength:     70000,
		Depth:            "deep",
	}

	hints := c.FormatHints()

	required := []string{
		"70000 characters",
		"25",                        // sections
		"20",                        // dimensions
		"Cover all 20",              // forces coverage
		"3",                         // appendices
		"each appendix",             // forces appendix coverage
		"numerically dense",         // preserves numbers
		"150+",                      // list items
		"Preserve each one rather than summarizing", // prevents collapse
	}
	for _, r := range required {
		if !strings.Contains(hints, r) {
			t.Errorf("FormatHints missing %q for deep source", r)
		}
	}
}

func TestFormatHintsShallowSource(t *testing.T) {
	c := &SourceComplexity{
		SectionCount:     3,
		DimensionCount:   0,
		AppendixCount:    0,
		NumericalDensity: 1.0,
		ListItemCount:    5,
		SourceLength:     3000,
		Depth:            "shallow",
	}

	hints := c.FormatHints()

	// Shallow source should NOT contain dimension/appendix/density warnings
	if strings.Contains(hints, "Cover all") {
		t.Error("FormatHints should not mention dimensions for shallow source")
	}
	if strings.Contains(hints, "appendix") {
		t.Error("FormatHints should not mention appendices for shallow source")
	}
	if strings.Contains(hints, "numerically dense") {
		t.Error("FormatHints should not warn about density for low-density source")
	}
}

func TestFormatHintsNil(t *testing.T) {
	var c *SourceComplexity
	if c.FormatHints() != "" {
		t.Error("FormatHints on nil should return empty string")
	}
}

func TestFormatHintsContainsVerifiedLabel(t *testing.T) {
	c := &SourceComplexity{SourceLength: 1000, Depth: "shallow"}
	hints := c.FormatHints()
	if !strings.Contains(hints, "verified") {
		t.Error("FormatHints should label counts as verified (to distinguish from LLM guesses)")
	}
}

// helper for tests — avoids fmt.Sprintf import just for int→string
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
