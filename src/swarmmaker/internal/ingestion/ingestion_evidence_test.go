// ingestion_evidence_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for evidence collection during ingestion.
// Verifies that every ingestion decision is recorded: hidden paths, noise
// directories, binary files, oversized files, unreadable files, symlinks,
// symlink loops, mixed encoding, and token budget truncation.


package ingestion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFolderMixedInputTreeEvidence(t *testing.T) {
	dir := prepareMixedInputFixture(t)

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}

	categories := make(map[EvidenceCategory]int)
	summaryCategories := make(map[EvidenceCategory]int)
	for _, entry := range ctx.Evidence {
		categories[entry.Category]++
		if entry.Phase == EvidencePhaseSummary {
			summaryCategories[entry.Category]++
		}
	}

	for _, want := range []EvidenceCategory{
		EvidenceCategoryHiddenPath,
		EvidenceCategoryExcludedNoisePath,
		EvidenceCategoryBinaryFile,
		EvidenceCategoryOversizedFile,
		EvidenceCategoryUnreadableFile,
		EvidenceCategoryMixedEncoding,
		EvidenceCategorySymlink,
		EvidenceCategorySymlinkLoop,
		EvidenceCategoryTokenBudgetTruncation,
		EvidenceCategoryTokenBudgetSkipped,
	} {
		if categories[want] == 0 {
			t.Fatalf("expected evidence category %q to be recorded", want)
		}
	}

	if len(ctx.BinaryFiles) != 2 {
		t.Fatalf("BinaryFiles = %d, want 2", len(ctx.BinaryFiles))
	}

	if ctx.FileCount == 0 {
		t.Fatal("expected readable files to be ingested")
	}

	if containsRelPath(ctx.Files, ".env") {
		t.Fatal("hidden file .env should not be ingested")
	}
	if containsRelPath(ctx.Files, "node_modules/ignored.js") {
		t.Fatal("noise directory file should not be ingested")
	}
	if containsRelPath(ctx.Files, "large/oversized.md") {
		t.Fatal("oversized file should not be ingested")
	}

	if !strings.Contains(ctx.Summary, "TRUNCATED") {
		t.Fatal("summary should contain a truncation marker for budgeted output")
	}
	if !strings.Contains(ctx.Summary, "NOTE") {
		t.Fatal("summary should contain a budget note when files are truncated or skipped")
	}

	if summaryCategories[EvidenceCategoryTokenBudgetTruncation] == 0 {
		t.Fatal("expected summary-phase truncation evidence")
	}
	if summaryCategories[EvidenceCategoryTokenBudgetSkipped] == 0 {
		t.Fatal("expected summary-phase skip evidence")
	}
}

func TestReadFolderRejectsUnreadableAndLoopedSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.md"), []byte("# target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.md", filepath.Join(dir, "link.md")); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if err := os.Symlink("loop.md", filepath.Join(dir, "loop.md")); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	privatePath := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(privatePath, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privatePath, 0); err != nil {
		t.Skipf("chmod not supported on this platform: %v", err)
	}

	ctx, err := ReadFolder(dir)
	if err != nil {
		t.Fatalf("ReadFolder failed: %v", err)
	}

	if !hasEvidence(ctx.Evidence, EvidenceCategorySymlink) {
		t.Fatal("expected a symlink evidence entry for link.md")
	}
	if !hasEvidence(ctx.Evidence, EvidenceCategorySymlinkLoop) {
		t.Fatal("expected a symlink loop evidence entry for loop.md")
	}
	if !hasEvidence(ctx.Evidence, EvidenceCategoryUnreadableFile) {
		t.Fatal("expected an unreadable-file evidence entry for private.txt")
	}
}

func prepareMixedInputFixture(t *testing.T) string {
	t.Helper()

	dest := t.TempDir()
	if err := copyFixtureTree("testdata/mixed_input_tree", dest); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dest, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "assets", "image.png"), []byte{0x89, 0x50, 0x4e, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "assets", "report.pdf"), []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, "large"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "large", "zzz-huge.md"), []byte(strings.Repeat("A", 220000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "large", "zzz-huge-2.md"), []byte(strings.Repeat("B", 120000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "large", "oversized.md"), []byte(strings.Repeat("O", maxFileSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "mixed-encoding.txt"), []byte{0xff, 0xfe, 0xfd, 0x61}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "private.txt"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dest, "private.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("overview.md", filepath.Join(dest, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop.md", filepath.Join(dest, "loop.md")); err != nil {
		t.Fatal(err)
	}

	return dest
}

func copyFixtureTree(srcRoot, destRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		destPath := filepath.Join(destRoot, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destPath, content, info.Mode())
	})
}

func containsRelPath(entries []FileEntry, relPath string) bool {
	for _, entry := range entries {
		if entry.RelPath == relPath {
			return true
		}
	}
	return false
}

func hasEvidence(entries []EvidenceEntry, category EvidenceCategory) bool {
	for _, entry := range entries {
		if entry.Category == category {
			return true
		}
	}
	return false
}
