// textutil.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Shared text utilities for digest computation and slug generation.
// Centralises logic that was previously duplicated across ir, cli, and output.

package textutil

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DigestBytes returns a SHA-256 hex digest prefixed with "sha256:".
func DigestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DigestString is a convenience wrapper around DigestBytes.
func DigestString(value string) string {
	return DigestBytes([]byte(value))
}

// Slugify lowercases the input, replaces non-alphanumeric characters (including
// spaces, dashes, and underscores) with a single dash, collapses consecutive
// dashes, and trims leading/trailing dashes.
func Slugify(s string) string {
	clean := strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range clean {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ':
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
}
