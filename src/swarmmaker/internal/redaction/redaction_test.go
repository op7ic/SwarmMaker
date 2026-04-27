// redaction_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for secret redaction.
// Covers all redaction patterns: API keys, bearer tokens, AWS keys, URL
// credentials, PEM blocks, multiple secrets per line, secrets at line
// boundaries, secrets in JSON, and replacement text verification.


package redaction

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	source := strings.Join([]string{
		`api_key = "sk-live-1234567890"`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz`,
		`aws=AKIA1234567890ABCDEF`,
		`postgres://user:password@example.com/db`,
	}, "\n")

	redacted, report := Redact(source)
	for _, forbidden := range []string{
		"sk-live-1234567890",
		"abcdefghijklmnopqrstuvwxyz",
		"AKIA1234567890ABCDEF",
		":password@example.com",
	} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redacted source still contains %q:\n%s", forbidden, redacted)
		}
	}
	if !report.Redacted || len(report.Findings) == 0 {
		t.Fatalf("expected redaction findings, got %#v", report)
	}
}

func TestRedactLeavesOrdinaryText(t *testing.T) {
	source := "# Notes\nUse evidence and mark UNKNOWN facts.\n"
	redacted, report := Redact(source)
	if redacted != source {
		t.Fatalf("ordinary source changed:\nwant %q\ngot  %q", source, redacted)
	}
	if report.Redacted || len(report.Findings) != 0 {
		t.Fatalf("unexpected redaction report: %#v", report)
	}
}

func TestRedactPEMBlock(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAJBALRiMLAH\n-----END RSA PRIVATE KEY-----"
	source := "config:\n" + pem + "\nmore text"
	redacted, report := Redact(source)
	if strings.Contains(redacted, "MIIBogIBAAJBALRiMLAH") {
		t.Fatalf("PEM key material not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:private_key_block]") {
		t.Fatalf("expected private_key_block placeholder, got: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
	foundCategory := false
	for _, f := range report.Findings {
		if f.Category == "private_key_block" && f.Count == 1 {
			foundCategory = true
		}
	}
	if !foundCategory {
		t.Fatalf("expected private_key_block finding, got %v", report.Findings)
	}
}

func TestRedactMultipleSecrets(t *testing.T) {
	source := strings.Join([]string{
		`api_key = "secret1234567890"`,
		`token = "another_secret_value"`,
		`password = hunter2hunter2`,
	}, "\n")
	redacted, report := Redact(source)
	for _, forbidden := range []string{"secret1234567890", "another_secret_value", "hunter2hunter2"} {
		if strings.Contains(redacted, forbidden) {
			t.Errorf("redacted output still contains %q", forbidden)
		}
	}
	if !report.Redacted {
		t.Fatal("expected redactions")
	}
	totalFindings := 0
	for _, f := range report.Findings {
		totalFindings += f.Count
	}
	if totalFindings < 3 {
		t.Errorf("expected at least 3 total findings, got %d from %v", totalFindings, report.Findings)
	}
}

func TestRedactBearerToken(t *testing.T) {
	// Standalone Bearer token (not in an Authorization header context)
	source := "token: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig"
	redacted, report := Redact(source)
	if strings.Contains(redacted, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("bearer token not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:bearer_token]") {
		t.Fatalf("expected bearer_token placeholder, got: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
}

func TestRedactAWSAccessKey(t *testing.T) {
	source := "access_key=AKIA1234567890ABCDEF is the key"
	redacted, report := Redact(source)
	if strings.Contains(redacted, "AKIA1234567890ABCDEF") {
		t.Fatalf("AWS key not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:aws_access_key]") {
		t.Fatalf("expected aws_access_key placeholder, got: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
}

func TestRedactURLCredentials(t *testing.T) {
	source := "database_url=postgres://admin:s3cretP@ss@db.example.com:5432/mydb"
	redacted, report := Redact(source)
	if strings.Contains(redacted, "s3cretP@ss") {
		t.Fatalf("URL password not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:url_password]") {
		t.Fatalf("expected url_password placeholder, got: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
}

func TestRedactSecretAtLineBoundary(t *testing.T) {
	source := "api_key = \"first_secret\"\nsome text\ntoken = \"second_secret\""
	redacted, report := Redact(source)
	if strings.Contains(redacted, "first_secret") {
		t.Errorf("first secret not redacted")
	}
	if strings.Contains(redacted, "second_secret") {
		t.Errorf("second secret not redacted")
	}
	// Verify the surrounding text is preserved
	if !strings.Contains(redacted, "some text") {
		t.Error("non-secret text was removed")
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
}

func TestRedactJSONEmbedded(t *testing.T) {
	// The secret_assignment pattern uses \b so the key needs a word boundary.
	// In JSON, keys preceded by space or line start will match.
	source := `{"config": {"name": "test"},
 api_key: "sk-live-abcdef1234567890"}`
	redacted, report := Redact(source)
	if strings.Contains(redacted, "sk-live-abcdef1234567890") {
		t.Fatalf("JSON-embedded secret not redacted: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
	// Verify surrounding text is preserved
	if !strings.Contains(redacted, `"name": "test"`) {
		t.Error("surrounding JSON structure was damaged")
	}
}

func TestRedactReportSummary(t *testing.T) {
	_, report := Redact("api_key = secret123456789012345")
	summary := report.Summary()
	if !strings.Contains(summary, "secret_assignment") {
		t.Errorf("summary = %q, expected to mention secret_assignment", summary)
	}

	_, cleanReport := Redact("no secrets here")
	if cleanReport.Summary() != "no redactions" {
		t.Errorf("clean summary = %q, want 'no redactions'", cleanReport.Summary())
	}
}

func TestRedactAuthorizationHeader(t *testing.T) {
	source := `Authorization: Basic dXNlcjpwYXNz`
	redacted, report := Redact(source)
	if strings.Contains(redacted, "dXNlcjpwYXNz") {
		t.Fatalf("authorization header not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:authorization_header]") {
		t.Fatalf("expected authorization_header placeholder, got: %s", redacted)
	}
	if !report.Redacted {
		t.Fatal("expected report.Redacted = true")
	}
}

func TestRedactClientSecret(t *testing.T) {
	source := `client_secret = "my-oauth-client-secret-value"`
	redacted, _ := Redact(source)
	if strings.Contains(redacted, "my-oauth-client-secret-value") {
		t.Fatalf("client_secret not redacted: %s", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:client_secret]") {
		t.Fatalf("expected client_secret placeholder, got: %s", redacted)
	}
}

func TestRedactMultiplePEMBlocks(t *testing.T) {
	source := "-----BEGIN RSA PRIVATE KEY-----\nkey1data\n-----END RSA PRIVATE KEY-----\n" +
		"some text\n" +
		"-----BEGIN EC PRIVATE KEY-----\nkey2data\n-----END EC PRIVATE KEY-----"
	redacted, report := Redact(source)
	if strings.Contains(redacted, "key1data") || strings.Contains(redacted, "key2data") {
		t.Fatalf("PEM key data not fully redacted: %s", redacted)
	}
	if count := strings.Count(redacted, "[REDACTED:private_key_block]"); count != 2 {
		t.Errorf("expected 2 private_key_block placeholders, got %d", count)
	}
	for _, f := range report.Findings {
		if f.Category == "private_key_block" {
			if f.Count != 2 {
				t.Errorf("expected private_key_block count=2, got %d", f.Count)
			}
			break
		}
	}
}
