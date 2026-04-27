// evidence.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Evidence type definitions for the ingestion phase.
// Defines EvidencePhase, EvidenceCategory, and EvidenceEntry types used to
// record what happened during source ingestion and generation. Categories
// cover: hidden paths, noise exclusions, binary files, oversized files,
// symlinks, unreadable files, mixed encoding, truncation, implementation
// decisions, and sandbox fallback events.


package ingestion

import (
	"path/filepath"
	"strings"
)

// EvidencePhase marks whether an evidence entry came from filesystem ingestion
// or from summary construction.
type EvidencePhase string

const (
	EvidencePhaseIngestion  EvidencePhase = "ingestion"
	EvidencePhaseSummary    EvidencePhase = "summary"
	EvidencePhaseGeneration EvidencePhase = "generation"
)

// EvidenceCategory classifies why a file or path was included, skipped, or
// transformed.
type EvidenceCategory string

const (
	EvidenceCategoryUnreadableFile        EvidenceCategory = "unreadable_file"
	EvidenceCategoryHiddenPath            EvidenceCategory = "hidden_path"
	EvidenceCategoryExcludedNoisePath     EvidenceCategory = "excluded_noise_path"
	EvidenceCategoryOversizedFile         EvidenceCategory = "oversized_file"
	EvidenceCategoryBinaryFile            EvidenceCategory = "binary_file"
	EvidenceCategorySymlink               EvidenceCategory = "symlink"
	EvidenceCategorySymlinkLoop           EvidenceCategory = "symlink_loop"
	EvidenceCategoryMixedEncoding         EvidenceCategory = "mixed_encoding"
	EvidenceCategoryTokenBudgetTruncation EvidenceCategory = "token_budget_truncation"
	EvidenceCategoryTokenBudgetSkipped        EvidenceCategory = "token_budget_skipped"
	EvidenceCategoryImplementationDecision    EvidenceCategory = "implementation_decision"
	EvidenceCategorySandboxFallback           EvidenceCategory = "sandbox_fallback"
	EvidenceCategoryInputQualityGate         EvidenceCategory = "input_quality_gate"
)

// EvidenceEntry records an ingestion or summary event.
type EvidenceEntry struct {
	Phase         EvidencePhase
	Category      EvidenceCategory
	RelPath       string
	AbsPath       string
	Detail        string
	FileType      string
	Size          int64
	BytesRead     int64
	BytesShown    int64
	SymlinkTarget string
	Err           string
}

// readableExtensions are treated as text-bearing inputs unless the path is
// hidden, excluded, unreadable, oversized, or invalid UTF-8.
var readableExtensions = map[string]string{
	".md":         "markdown",
	".markdown":   "markdown",
	".txt":        "text",
	".text":       "text",
	".log":        "text",
	".csv":        "data",
	".json":       "data",
	".yaml":       "data",
	".yml":        "data",
	".toml":       "data",
	".xml":        "data",
	".ini":        "data",
	".cfg":        "data",
	".conf":       "data",
	".env":        "data",
	".py":         "code",
	".go":         "code",
	".js":         "code",
	".ts":         "code",
	".jsx":        "code",
	".tsx":        "code",
	".rs":         "code",
	".rb":         "code",
	".java":       "code",
	".sh":         "code",
	".bash":       "code",
	".zsh":        "code",
	".ps1":        "code",
	".bat":        "code",
	".cmd":        "code",
	".sql":        "code",
	".html":       "code",
	".css":        "code",
	".scss":       "code",
	".vue":        "code",
	".svelte":     "code",
	".proto":      "code",
	".graphql":    "code",
	".gql":        "code",
	".tf":         "code",
	".hcl":        "code",
	".dockerfile": "code",
	".makefile":   "code",
}

// noiseDirs are directories we never descend into.
var noiseDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".nuxt":        true,
	"vendor":       true,
	"target":       true,
}

func classifyReadableFile(name, ext string) (string, bool) {
	if fileType, ok := readableExtensions[strings.ToLower(ext)]; ok {
		return fileType, true
	}

	baseName := strings.ToLower(name)
	switch {
	case baseName == "makefile" || baseName == "dockerfile" || baseName == "vagrantfile":
		return "code", true
	case baseName == "readme" || baseName == "todo" || baseName == "notes" || baseName == "plan":
		return "text", true
	default:
		return "", false
	}
}

func classifyBinaryFile(ext string) string {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico":
		return "image"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt":
		return "document"
	case ".mp4", ".mp3", ".wav", ".avi", ".mov", ".webm":
		return "media"
	case ".zip", ".tar", ".gz", ".rar", ".7z", ".bz2":
		return "archive"
	default:
		return ""
	}
}

func relPathWithin(rootPath, path string) (string, error) {
	relPath, err := filepath.Rel(rootPath, path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(relPath), nil
}

func hasHiddenComponent(relPath string) bool {
	if relPath == "." {
		return false
	}

	for _, part := range strings.Split(relPath, string(filepath.Separator)) {
		if part == "." || part == ".." || part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func isNoiseDir(name string) bool {
	return noiseDirs[strings.ToLower(name)]
}

// codeExtToLanguage maps file extensions to normalized language names used by
// the prompt compiler. Only extensions that map to a supported tool language
// are included.
var codeExtToLanguage = map[string]string{
	".go":   "go",
	".py":   "python",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".rs":   "rust",
	".sh":   "shell",
	".bash": "shell",
	".zsh":  "shell",
}

// detectTools scans ingested files for source code and returns a DetectedTool
// entry for each one.
func detectTools(files []FileEntry) []DetectedTool {
	var tools []DetectedTool
	for _, f := range files {
		if f.FileType != "code" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.RelPath))
		lang, ok := codeExtToLanguage[ext]
		if !ok {
			continue
		}
		tools = append(tools, DetectedTool{
			Path:     f.RelPath,
			Language: lang,
			Purpose:  inferPurpose(f.RelPath),
		})
	}
	return tools
}

// inferPurpose guesses a short purpose description from the filename.
func inferPurpose(relPath string) string {
	base := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	base = strings.ToLower(base)
	switch {
	case base == "main":
		return "entry point"
	case strings.Contains(base, "webhook"):
		return "webhook handler"
	case strings.Contains(base, "validate") || strings.Contains(base, "validator"):
		return "validator"
	case strings.Contains(base, "deploy"):
		return "deployment script"
	case strings.Contains(base, "config") || strings.Contains(base, "conf"):
		return "config loader"
	case strings.Contains(base, "test"):
		return "test file"
	case strings.Contains(base, "util") || strings.Contains(base, "helper"):
		return "utility"
	case strings.Contains(base, "server") || strings.Contains(base, "serve"):
		return "server"
	case strings.Contains(base, "client"):
		return "client"
	case strings.Contains(base, "handler"):
		return "request handler"
	case strings.Contains(base, "model") || strings.Contains(base, "schema"):
		return "data model"
	case strings.Contains(base, "route") || strings.Contains(base, "router"):
		return "router"
	case strings.Contains(base, "middleware"):
		return "middleware"
	default:
		return "source file"
	}
}
