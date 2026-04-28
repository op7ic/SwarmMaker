// ingestion.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Evidence-backed source file ingestion.
// Walks the input directory, reads text files, and builds a concatenated
// summary within a token budget. Records evidence for every decision: files
// skipped as binary, hidden, oversized, noise (node_modules, .git), symlink
// loops, mixed encoding, and permission errors. No file disappears silently.


package ingestion

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxFileSize = 1024 * 1024 // 1MB per file max

// TokenBudget is the maximum source material size in characters (~50K tokens).
// This leaves headroom for prompt overhead (contracts, citations, IR metadata)
// and generation output within the model's context window. Frontier models
// handle well beyond this limit, but keeping source material bounded ensures
// prompt compilation stays fast and token costs stay predictable.
const TokenBudget = 200_000

// FileEntry represents a single ingested file.
type FileEntry struct {
	RelPath  string // relative path from the input folder
	AbsPath  string // absolute path on disk
	Content  string // file content as string
	Size     int64  // file size in bytes
	FileType string // classified type: "markdown", "text", "code", "data", "unknown"
}

// DetectedTool represents a source code file detected during ingestion.
type DetectedTool struct {
	Path     string // relative path of the code file
	Language string // go, python, typescript, javascript, rust, shell
	Purpose  string // inferred from filename/path
}

// ReferenceFile represents a lookup table / reference data file that was
// detected during ingestion. Its content is NOT embedded in the prompt summary
// because it contains raw data (hashes, keywords, one-value-per-line lists)
// rather than prose documentation or source code. A summary line is included
// in the prompt instead.
type ReferenceFile struct {
	RelPath   string
	AbsPath   string
	Size      int64
	FileType  string
	LineCount int
}

// Context holds all ingested unstructured data from the input folder.
type Context struct {
	RootPath       string          // absolute path to the input folder
	Files          []FileEntry     // all ingested files (text-readable, non-reference)
	BinaryFiles    []FileEntry     // non-text files (images, PDFs, etc.) -- noted but not read
	ReferenceFiles []ReferenceFile // lookup tables, hash lists, keyword files -- recorded but not embedded
	DetectedTools  []DetectedTool  // source code files detected as tools
	Evidence       []EvidenceEntry // ingest and summary evidence records
	FileCount      int             // total number of readable files (excluding reference data)
	TotalBytes     int64           // total bytes read (excluding reference data)
	Summary        string          // concatenated summary of all content for LLM input
}

// ReadFolder reads all readable files from a directory tree and builds a
// unified context for LLM consumption.
func ReadFolder(rootPath string) (*Context, error) {
	if strings.TrimSpace(rootPath) == "" {
		return nil, fmt.Errorf("ingestion root path is empty")
	}

	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve root path %q: %w", rootPath, err)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve symlinks for root %q: %w", absRoot, err)
	}

	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("stat root %q: %w", resolvedRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path %q is not a directory", resolvedRoot)
	}

	ctx := &Context{
		RootPath: resolvedRoot,
		Files:    make([]FileEntry, 0),
	}

	if err := filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryUnreadableFile,
				AbsPath:  path,
				Err:      walkErr.Error(),
				Detail:   "directory entry could not be read",
			})
			return nil
		}

		if path == resolvedRoot {
			return nil
		}

		relPath, relErr := relPathWithin(resolvedRoot, path)
		if relErr != nil {
			return fmt.Errorf("relative path for %q: %w", path, relErr)
		}

		if d.Type()&os.ModeSymlink != 0 {
			recordSymlinkEvidence(ctx, path, relPath)
			return nil
		}

		if hasHiddenComponent(relPath) {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryHiddenPath,
				RelPath:  relPath,
				AbsPath:  path,
				Detail:   "hidden path excluded from ingestion",
			})
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if isNoiseDir(d.Name()) {
				ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
					Phase:    EvidencePhaseIngestion,
					Category: EvidenceCategoryExcludedNoisePath,
					RelPath:  relPath,
					AbsPath:  path,
					Detail:   "excluded noise directory",
				})
				return fs.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		fileType, readable := classifyReadableFile(d.Name(), ext)

		if !readable {
			if binaryType := classifyBinaryFile(ext); binaryType != "" {
				size, sizeErr := fileSize(path)
				if sizeErr != nil {
					ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
						Phase:    EvidencePhaseIngestion,
						Category: EvidenceCategoryUnreadableFile,
						RelPath:  relPath,
						AbsPath:  path,
						Err:      sizeErr.Error(),
						Detail:   "binary file size could not be determined",
					})
					return nil
				}
				ctx.BinaryFiles = append(ctx.BinaryFiles, FileEntry{
					RelPath:  relPath,
					AbsPath:  path,
					Size:     size,
					FileType: binaryType,
				})
				ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
					Phase:    EvidencePhaseIngestion,
					Category: EvidenceCategoryBinaryFile,
					RelPath:  relPath,
					AbsPath:  path,
					FileType: binaryType,
					Size:     size,
					Detail:   "binary file excluded from text ingestion",
				})
			}
			return nil
		}

		size, sizeErr := fileSize(path)
		if sizeErr != nil {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryUnreadableFile,
				RelPath:  relPath,
				AbsPath:  path,
				FileType: fileType,
				Err:      sizeErr.Error(),
				Detail:   "file size could not be determined",
			})
			return nil
		}

		if size > maxFileSize {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryOversizedFile,
				RelPath:  relPath,
				AbsPath:  path,
				FileType: fileType,
				Size:     size,
				Detail:   fmt.Sprintf("file exceeds max file size of %d bytes", maxFileSize),
			})
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryUnreadableFile,
				RelPath:  relPath,
				AbsPath:  path,
				FileType: fileType,
				Size:     size,
				Err:      readErr.Error(),
				Detail:   "file could not be read",
			})
			return nil
		}

		if !utf8.Valid(content) {
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:     EvidencePhaseIngestion,
				Category:  EvidenceCategoryMixedEncoding,
				RelPath:   relPath,
				AbsPath:   path,
				FileType:  fileType,
				Size:      size,
				BytesRead: int64(len(content)),
				Detail:    "invalid UTF-8 or mixed encoding detected",
			})
			return nil
		}

		contentStr := string(content)

		// Classify reference data: files that are lookup tables (hashes, keywords,
		// one-value-per-line lists) get recorded as evidence but their content is
		// NOT embedded in the prompt summary. Only a summary line is included.
		if isReferenceData(contentStr, fileType, relPath) {
			ctx.ReferenceFiles = append(ctx.ReferenceFiles, ReferenceFile{
				RelPath:   relPath,
				AbsPath:   path,
				Size:      size,
				FileType:  fileType,
				LineCount: strings.Count(contentStr, "\n"),
			})
			ctx.Evidence = append(ctx.Evidence, EvidenceEntry{
				Phase:    EvidencePhaseIngestion,
				Category: EvidenceCategoryReferenceData,
				RelPath:  relPath,
				AbsPath:  path,
				FileType: fileType,
				Size:     size,
				Detail:   fmt.Sprintf("classified as reference data (lookup table); content excluded from prompt summary, recorded as %d lines", strings.Count(contentStr, "\n")),
			})
			return nil
		}

		ctx.Files = append(ctx.Files, FileEntry{
			RelPath:  relPath,
			AbsPath:  path,
			Content:  contentStr,
			Size:     size,
			FileType: fileType,
		})
		ctx.TotalBytes += size
		return nil
	}); err != nil {
		return nil, err
	}

	ctx.FileCount = len(ctx.Files)

	// Sort: markdown first, then text, then data, then code.
	typeOrder := map[string]int{
		"markdown": 0,
		"text":     1,
		"data":     2,
		"code":     3,
		"unknown":  4,
	}
	sort.Slice(ctx.Files, func(i, j int) bool {
		oi := typeOrder[ctx.Files[i].FileType]
		oj := typeOrder[ctx.Files[j].FileType]
		if oi != oj {
			return oi < oj
		}
		return ctx.Files[i].RelPath < ctx.Files[j].RelPath
	})

	ctx.DetectedTools = detectTools(ctx.Files)

	// Scan for prompt injection patterns before building the summary.
	// Content is never modified -- matches are recorded as evidence for
	// human review.
	injectionEvidence := ScanForInjection(ctx)
	ctx.Evidence = append(ctx.Evidence, injectionEvidence...)

	summary, summaryEvidence := buildSummary(ctx)
	ctx.Summary = summary
	ctx.Evidence = append(ctx.Evidence, summaryEvidence...)

	return ctx, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("path %q is not a regular file", path)
	}
	return info.Size(), nil
}

func recordSymlinkEvidence(ctx *Context, path, relPath string) {
	target, targetErr := os.Readlink(path)
	entry := EvidenceEntry{
		Phase:    EvidencePhaseIngestion,
		Category: EvidenceCategorySymlink,
		RelPath:  relPath,
		AbsPath:  path,
		Detail:   "symlink excluded from ingestion",
	}
	if targetErr == nil {
		entry.SymlinkTarget = target
	}

	if evalTarget, err := filepath.EvalSymlinks(path); err != nil {
		entry.Category = EvidenceCategorySymlinkLoop
		entry.Err = err.Error()
		entry.Detail = "symlink target could not be resolved without looping"
	} else if evalTarget != "" {
		entry.SymlinkTarget = evalTarget
	}

	ctx.Evidence = append(ctx.Evidence, entry)
}

// isReferenceData detects files that are lookup tables rather than prose
// documentation or source code. These include: SHA256 hash lists, keyword
// files (one word per line), CSV-like data dumps, and other structured data
// that an LLM cannot meaningfully decompose into agent skills.
//
// Heuristic: if >80% of non-empty lines are "simple" (no spaces, or single
// short tokens, or hex strings), the file is reference data.
func isReferenceData(content, fileType, relPath string) bool {
	// Only classify text and data files. Code and markdown are never reference data.
	if fileType == "code" || fileType == "markdown" {
		return false
	}

	lines := strings.Split(content, "\n")
	if len(lines) < 5 {
		return false // too few lines to classify
	}

	nonEmpty := 0
	simple := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		nonEmpty++
		// A "simple" line is one that looks like a lookup value:
		// - no spaces (single token: hash, keyword, filename)
		// - or a single comma-separated row of short values
		// - or a hex string (SHA256, MD5)
		if !strings.Contains(trimmed, " ") || isHexLine(trimmed) || len(trimmed) < 80 && strings.Count(trimmed, ",") > 2 {
			simple++
		}
	}

	if nonEmpty == 0 {
		return false
	}

	return float64(simple)/float64(nonEmpty) > 0.80
}

// isHexLine returns true if the line looks like a hex hash (SHA256, MD5, etc.)
func isHexLine(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
