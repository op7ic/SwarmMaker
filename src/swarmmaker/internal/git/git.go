// git.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Git repository context detection.
// Detects whether the output directory is inside a git repo and extracts
// context: branch, remote URL, tracked files, dirty state, tags, and last
// commit. Git context is optional enrichment -- failures return empty values
// rather than blocking the pipeline.


package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// Context holds git repository information for the project.
type Context struct {
	IsRepo       bool     // whether the path is inside a git repo
	RepoRoot     string   // root of the git repository
	Branch       string   // current branch name
	RemoteURL    string   // origin remote URL
	RemoteName   string   // remote name (usually "origin")
	TrackedFiles []string // list of tracked file paths
	LastCommit   string   // last commit hash (short)
	LastMessage  string   // last commit message
	IsDirty      bool     // whether there are uncommitted changes
	Tags         []string // recent tags
}

// DetectContext probes the given path for git repository information.
func DetectContext(path string) *Context {
	ctx := &Context{IsRepo: false}

	// Check if path is in a git repo
	repoRoot := findRepoRoot(path)
	if repoRoot == "" {
		return ctx
	}

	ctx.IsRepo = true
	ctx.RepoRoot = repoRoot

	// Get current branch
	ctx.Branch = runGit(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")

	// Get remote URL
	ctx.RemoteURL = runGit(repoRoot, "remote", "get-url", "origin")
	if ctx.RemoteURL != "" {
		ctx.RemoteName = "origin"
	}

	// Get tracked files
	filesOutput := runGit(repoRoot, "ls-files")
	if filesOutput != "" {
		ctx.TrackedFiles = strings.Split(filesOutput, "\n")
	}

	// Get last commit
	ctx.LastCommit = runGit(repoRoot, "rev-parse", "--short", "HEAD")
	ctx.LastMessage = runGit(repoRoot, "log", "-1", "--pretty=format:%s")

	// Check dirty state
	statusOutput := runGit(repoRoot, "status", "--porcelain")
	ctx.IsDirty = statusOutput != ""

	// Get recent tags
	tagsOutput := runGit(repoRoot, "tag", "--sort=-creatordate", "-l")
	if tagsOutput != "" {
		tags := strings.Split(tagsOutput, "\n")
		if len(tags) > 5 {
			tags = tags[:5]
		}
		ctx.Tags = tags
	}

	return ctx
}

func findRepoRoot(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ""
	}

	output := runGitAt(absPath, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(output)
}

func runGit(repoRoot string, args ...string) string {
	return runGitAt(repoRoot, args...)
}

func runGitAt(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

