package redact

import "testing"

func TestIsSensitiveBuiltins(t *testing.T) {
	m := New(nil)

	sensitive := []string{
		"export AWS_SECRET_ACCESS_KEY=abc123",
		"GH_TOKEN=ghp_xxx gh auth login",
		"MY_PASSWORD=hunter2",
		"API_KEY=sk-live-123 ./deploy.sh",
		"curl --header 'Authorization: Bearer abcdef' https://api.example.com",
		"mysql --password=secret -u root",
		"some-tool --token deadbeef",
		"curl -u admin:hunter2 https://example.com",
		"foo --api-key=xyz",
	}
	for _, cmd := range sensitive {
		if !m.IsSensitive(cmd) {
			t.Errorf("expected sensitive: %q", cmd)
		}
	}

	safe := []string{
		"git commit -m \"fix password reset bug\"",
		"git push origin main",
		"ls -la",
		"cp -pr src dst",
		"grep -i token notes.txt",
		"echo secret stuff", // no assignment/flag/header shape
		"cargo build --release",
		"",
	}
	for _, cmd := range safe {
		if m.IsSensitive(cmd) {
			t.Errorf("expected safe: %q", cmd)
		}
	}
}

func TestExtraPatterns(t *testing.T) {
	m := New([]string{`(?i)\bvault\b`})
	if !m.IsSensitive("vault kv get secret/foo") {
		t.Error("expected extra pattern to match 'vault ...'")
	}
	if m.IsSensitive("git status") {
		t.Error("git status should not match the extra pattern")
	}
}

func TestInvalidExtraPatternSkipped(t *testing.T) {
	// An unparsable regex must be ignored, not panic or disable matching.
	m := New([]string{"("})
	if !m.IsSensitive("PASSWORD=x") {
		t.Error("builtins should still work when an extra pattern is invalid")
	}
	if m.IsSensitive("git status") {
		t.Error("invalid pattern should not match everything")
	}
}
