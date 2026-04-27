// textutil_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker

package textutil

import (
	"strings"
	"testing"
)

func TestDigestBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{
			name:  "empty payload",
			input: []byte{},
			want:  "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:  "hello world",
			input: []byte("hello world"),
			want:  "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		},
		{
			name:  "binary payload",
			input: []byte{0x00, 0xff, 0x42},
			want:  "sha256:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DigestBytes(tt.input)
			if !strings.HasPrefix(got, "sha256:") {
				t.Errorf("DigestBytes() = %q, missing sha256: prefix", got)
			}
			if tt.name != "binary payload" && got != tt.want {
				t.Errorf("DigestBytes() = %q, want %q", got, tt.want)
			}
			if tt.name == "binary payload" && len(got) != len("sha256:")+64 {
				t.Errorf("DigestBytes() length = %d, want %d", len(got), len("sha256:")+64)
			}
		})
	}
}

func TestDigestString(t *testing.T) {
	t.Parallel()
	got := DigestString("hello world")
	want := DigestBytes([]byte("hello world"))
	if got != want {
		t.Errorf("DigestString() = %q, want %q", got, want)
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple lowercase", "hello", "hello"},
		{"uppercase", "Hello World", "hello-world"},
		{"special chars", "foo@bar!baz", "foobarbaz"},
		{"dashes preserved", "foo-bar", "foo-bar"},
		{"underscores to dash", "foo_bar", "foo-bar"},
		{"spaces to dash", "foo bar", "foo-bar"},
		{"consecutive dashes collapsed", "foo--bar", "foo-bar"},
		{"leading trailing dashes trimmed", "--foo--", "foo"},
		{"mixed special chars", "  Hello, World! 42  ", "hello-world-42"},
		{"empty string", "", ""},
		{"only special chars", "@#$%", ""},
		{"numbers only", "123", "123"},
		{"unicode stripped", "caf\u00e9", "caf"},
		{"multiple spaces", "foo   bar", "foo-bar"},
		{"tabs and newlines", "foo\tbar\nbaz", "foobarbaz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Slugify(tt.input)
			if got != tt.want {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
