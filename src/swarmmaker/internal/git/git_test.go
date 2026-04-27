// git_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for git context detection.
// Covers real git repo detection (with branch, tracked files, dirty state,
// remote, and tags) and non-repo path handling.


package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectContextNonRepo(t *testing.T) {
	dir := t.TempDir()
	ctx := DetectContext(dir)
	if ctx.IsRepo {
		t.Error("temp dir should not be a git repo")
	}
	if ctx.Branch != "" {
		t.Errorf("Branch should be empty for non-repo, got %q", ctx.Branch)
	}
}

func TestDetectContextRealRepo(t *testing.T) {
	dir := t.TempDir()

	// Initialize a real git repo
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")

	// Create a file and commit
	writeTestFile(t, dir, "hello.txt", "hello world\n")
	runCmd(t, dir, "git", "add", "hello.txt")
	runCmd(t, dir, "git", "commit", "-m", "initial commit")

	ctx := DetectContext(dir)
	if !ctx.IsRepo {
		t.Fatal("expected IsRepo = true")
	}
	if ctx.RepoRoot == "" {
		t.Fatal("expected non-empty RepoRoot")
	}
	if ctx.Branch == "" {
		t.Fatal("expected non-empty Branch")
	}
	if ctx.LastCommit == "" {
		t.Fatal("expected non-empty LastCommit")
	}
	if ctx.LastMessage != "initial commit" {
		t.Errorf("LastMessage = %q, want %q", ctx.LastMessage, "initial commit")
	}
	if len(ctx.TrackedFiles) == 0 {
		t.Fatal("expected at least one tracked file")
	}
	found := false
	for _, f := range ctx.TrackedFiles {
		if f == "hello.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("TrackedFiles = %v, want to contain hello.txt", ctx.TrackedFiles)
	}
	if ctx.IsDirty {
		t.Error("expected clean repo, got IsDirty = true")
	}
}

func TestDetectContextDirtyState(t *testing.T) {
	dir := t.TempDir()

	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")

	writeTestFile(t, dir, "clean.txt", "clean\n")
	runCmd(t, dir, "git", "add", "clean.txt")
	runCmd(t, dir, "git", "commit", "-m", "clean state")

	// Create an untracked file to make it dirty
	writeTestFile(t, dir, "dirty.txt", "uncommitted\n")

	ctx := DetectContext(dir)
	if !ctx.IsRepo {
		t.Fatal("expected IsRepo = true")
	}
	if !ctx.IsDirty {
		t.Error("expected IsDirty = true after adding untracked file")
	}
}

func TestDetectContextWithRemote(t *testing.T) {
	// Create a bare "remote" repo
	remoteDir := t.TempDir()
	runCmd(t, remoteDir, "git", "init", "--bare")

	// Create a working repo that clones from it
	workDir := t.TempDir()
	runCmd(t, workDir, "git", "clone", remoteDir, ".")
	runCmd(t, workDir, "git", "config", "user.email", "test@test.com")
	runCmd(t, workDir, "git", "config", "user.name", "Test")

	writeTestFile(t, workDir, "file.txt", "content\n")
	runCmd(t, workDir, "git", "add", "file.txt")
	runCmd(t, workDir, "git", "commit", "-m", "with remote")

	ctx := DetectContext(workDir)
	if !ctx.IsRepo {
		t.Fatal("expected IsRepo = true")
	}
	if ctx.RemoteURL == "" {
		t.Fatal("expected non-empty RemoteURL")
	}
	if ctx.RemoteName != "origin" {
		t.Errorf("RemoteName = %q, want %q", ctx.RemoteName, "origin")
	}
}

func TestDetectContextWithTags(t *testing.T) {
	dir := t.TempDir()

	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")

	writeTestFile(t, dir, "file.txt", "v1\n")
	runCmd(t, dir, "git", "add", "file.txt")
	runCmd(t, dir, "git", "commit", "-m", "first")
	runCmd(t, dir, "git", "tag", "v1.0.0")

	ctx := DetectContext(dir)
	if len(ctx.Tags) == 0 {
		t.Fatal("expected at least one tag")
	}
	if ctx.Tags[0] != "v1.0.0" {
		t.Errorf("Tags[0] = %q, want %q", ctx.Tags[0], "v1.0.0")
	}
}

// helpers

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, out)
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
