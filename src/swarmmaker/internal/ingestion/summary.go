// summary.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Token-budget-aware source summarization.
// Builds the concatenated source summary that gets embedded in LLM prompts.
// Uses binary search to find the optimal truncation point that stays within
// the token budget while preserving line boundaries. Records evidence for
// truncated and skipped files.


package ingestion

import (
	"fmt"
	"strings"
)

const summaryCloseFence = "```\n\n"

func buildSummary(ctx *Context) (string, []EvidenceEntry) {
	var b strings.Builder
	evidence := make([]EvidenceEntry, 0)
	remaining := TokenBudget
	truncatedCount := 0
	skippedCount := 0

	write := func(s string) {
		b.WriteString(s)
		remaining -= len(s)
	}

	header := fmt.Sprintf(
		"# Input Context\n\nSource folder: %s\nTotal files: %d\nTotal size: %d bytes\n\n",
		ctx.RootPath, ctx.FileCount, ctx.TotalBytes,
	)
	write(header)

	write("## File Listing\n\n")
	for _, f := range ctx.Files {
		line := fmt.Sprintf("- [%s] %s (%d bytes)\n", f.FileType, f.RelPath, f.Size)
		write(line)
	}
	write("\n")

	write("## File Contents\n\n")

	for _, f := range ctx.Files {
		headerLine := fmt.Sprintf("### %s\nType: %s | Size: %d bytes\n\n", f.RelPath, f.FileType, f.Size)
		headerLen := len(headerLine)
		contentLen := len(f.Content)

		// If there is not enough room for even a compact block, emit evidence and skip.
		if remaining <= headerLen+len(summaryCloseFence)+16 {
			skippedCount++
			evidence = append(evidence, EvidenceEntry{
				Phase:      EvidencePhaseSummary,
				Category:   EvidenceCategoryTokenBudgetSkipped,
				RelPath:    f.RelPath,
				AbsPath:    f.AbsPath,
				FileType:   f.FileType,
				Size:       f.Size,
				BytesRead:  int64(contentLen),
				BytesShown: 0,
				Detail:     "file omitted from summary because token budget was exhausted",
			})
			continue
		}

		blockLen := func(shown int) int {
			noteLen := 0
			if shown < contentLen {
				noteLen = len(fmt.Sprintf("[TRUNCATED %d/%d bytes shown]\n", shown, contentLen))
			}
			newlineLen := 0
			if shown == 0 || f.Content[shown-1] != '\n' {
				newlineLen = 1
			}
			return headerLen + len("```\n") + shown + newlineLen + noteLen + len(summaryCloseFence)
		}

		if blockLen(contentLen) <= remaining {
			write(headerLine)
			write("```\n")
			write(f.Content)
			if !strings.HasSuffix(f.Content, "\n") {
				write("\n")
			}
			write(summaryCloseFence)
			continue
		}

		upper := contentLen
		lo, hi := 0, upper
		best := -1
		for lo <= hi {
			mid := lo + (hi-lo)/2
			if blockLen(mid) <= remaining {
				best = mid
				lo = mid + 1
				continue
			}
			hi = mid - 1
		}

		if best > 0 {
			if idx := strings.LastIndex(f.Content[:best], "\n"); idx >= 0 {
				candidate := idx + 1
				if blockLen(candidate) <= remaining {
					best = candidate
				}
			}
		}

		if best <= 0 || blockLen(best) > remaining {
			skippedCount++
			evidence = append(evidence, EvidenceEntry{
				Phase:      EvidencePhaseSummary,
				Category:   EvidenceCategoryTokenBudgetSkipped,
				RelPath:    f.RelPath,
				AbsPath:    f.AbsPath,
				FileType:   f.FileType,
				Size:       f.Size,
				BytesRead:  int64(contentLen),
				BytesShown: 0,
				Detail:     "file omitted from summary because no safe truncation fit the token budget",
			})
			continue
		}

		truncatedCount++
		truncatedContent := f.Content[:best]
		write(headerLine)
		write("```\n")
		write(truncatedContent)
		if !strings.HasSuffix(truncatedContent, "\n") {
			write("\n")
		}
		write(fmt.Sprintf("[TRUNCATED %d/%d bytes shown]\n", best, contentLen))
		write(summaryCloseFence)

		evidence = append(evidence, EvidenceEntry{
			Phase:      EvidencePhaseSummary,
			Category:   EvidenceCategoryTokenBudgetTruncation,
			RelPath:    f.RelPath,
			AbsPath:    f.AbsPath,
			FileType:   f.FileType,
			Size:       f.Size,
			BytesRead:  int64(contentLen),
			BytesShown: int64(best),
			Detail:     "file content truncated to fit the token budget",
		})
	}

	if truncatedCount > 0 || skippedCount > 0 {
		noteParts := make([]string, 0, 2)
		if truncatedCount > 0 {
			noteParts = append(noteParts, fmt.Sprintf("%d files truncated", truncatedCount))
		}
		if skippedCount > 0 {
			noteParts = append(noteParts, fmt.Sprintf("%d files skipped", skippedCount))
		}
		write(fmt.Sprintf("\n[NOTE: %s due to token budget. Budget: %d chars.]\n", strings.Join(noteParts, ", "), TokenBudget))
	}

	return b.String(), evidence
}
