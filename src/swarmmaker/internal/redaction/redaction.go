// redaction.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Secret pattern redaction for persisted artifacts.
// Redacts common secret patterns (PEM private keys, bearer tokens, AWS access
// keys, API key assignments, authorization headers, URL credentials) from
// source material before it is written to .tasks/ir/prompt-ir.json. Produces
// a deterministic findings report listing what was redacted and where.


package redaction

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Report records deterministic source redaction applied before persistence.
type Report struct {
	Redacted bool      `json:"redacted"`
	Findings []Finding `json:"findings,omitempty"`
}

type Finding struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

type rule struct {
	category string
	pattern  *regexp.Regexp
	replacer func([]string) string
}

var rules = []rule{
	{
		category: "private_key_block",
		pattern:  regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		replacer: func(_ []string) string { return "[REDACTED:private_key_block]" },
	},
	{
		category: "bearer_token",
		pattern:  regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`),
		replacer: func(_ []string) string { return "Bearer [REDACTED:bearer_token]" },
	},
	{
		category: "aws_access_key",
		pattern:  regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		replacer: func(_ []string) string { return "[REDACTED:aws_access_key]" },
	},
	{
		category: "secret_assignment",
		pattern:  regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token|password|passwd|client[_-]?secret|private[_-]?key)\b(\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s\n]+)`),
		replacer: func(groups []string) string {
			return groups[1] + groups[2] + "[REDACTED:" + normalizeCategory(groups[1]) + "]"
		},
	},
	{
		category: "authorization_header",
		pattern:  regexp.MustCompile(`(?i)\bAuthorization\b(\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s\n]+(?:\s+[^\s\n]+)?)`),
		replacer: func(groups []string) string { return "Authorization" + groups[1] + "[REDACTED:authorization_header]" },
	},
	{
		category: "url_credentials",
		pattern:  regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/\s:@]+):([^/\s@]+)@`),
		replacer: func(groups []string) string { return groups[1] + groups[2] + ":[REDACTED:url_password]@" },
	},
}

func Redact(input string) (string, Report) {
	output := input
	counts := make(map[string]int)
	for _, current := range rules {
		matches := current.pattern.FindAllStringSubmatch(output, -1)
		if len(matches) == 0 {
			continue
		}
		counts[current.category] += len(matches)
		output = current.pattern.ReplaceAllStringFunc(output, func(match string) string {
			groups := current.pattern.FindStringSubmatch(match)
			return current.replacer(groups)
		})
	}
	findings := make([]Finding, 0, len(counts))
	for category, count := range counts {
		findings = append(findings, Finding{Category: category, Count: count})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Category < findings[j].Category })
	return output, Report{
		Redacted: len(findings) > 0,
		Findings: findings,
	}
}

func normalizeCategory(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteByte('_')
		}
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "secret"
	}
	return result
}

func (r Report) Summary() string {
	if len(r.Findings) == 0 {
		return "no redactions"
	}
	parts := make([]string, 0, len(r.Findings))
	for _, finding := range r.Findings {
		parts = append(parts, fmt.Sprintf("%s=%d", finding.Category, finding.Count))
	}
	return strings.Join(parts, ", ")
}
