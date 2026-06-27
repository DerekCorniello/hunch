// Package redact decides whether a raw shell command is too sensitive to
// record. It is pure logic with no IO and no shell awareness.
//
// Normalized templates are already privacy-safe (concrete values collapse
// to STR/PATH/etc.), but hunch also stores the raw command so it can be
// suggested back verbatim. That raw form can leak secrets — API tokens,
// passwords, auth headers — so commands matching a sensitive pattern are
// dropped entirely (neither the transition nor the raw example is recorded).
//
// The built-in patterns are intentionally conservative to avoid discarding
// ordinary commands; users extend coverage via the configurable ignore list.
package redact

import "regexp"

// builtinPatterns matches commands that commonly carry inline secrets.
// Each pattern is anchored on a secret-bearing flag, assignment key, or
// header rather than on free text, so a commit message mentioning
// "password" is not mistaken for a secret.
var builtinPatterns = []*regexp.Regexp{
	// Assignment to a secret-like variable: AWS_SECRET_KEY=…, GH_TOKEN=…,
	// MY_PASSWORD=…, API_KEY=…, *_CREDENTIAL=…, PRIVATE_KEY=…
	regexp.MustCompile(`(?i)(^|\s)[A-Z0-9_]*(SECRET|TOKEN|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|CREDENTIAL)[A-Z0-9_]*\s*=\s*\S`),

	// A secret-bearing flag carrying a value: --password=…, --password …,
	// --token …, --secret=…, --api-key …, --auth …
	regexp.MustCompile(`(?i)(^|\s)--?(password|passwd|pass|token|secret|api[-_]?key|auth)(=\S|\s+\S)`),

	// An Authorization header value: Authorization: Bearer …, Basic …, etc.
	regexp.MustCompile(`(?i)authorization:\s*\S+\s+\S`),

	// A user:password pair after -u (curl, etc.): -u user:secret
	regexp.MustCompile(`(^|\s)-u\s+\S+:\S`),
}

// Matcher reports whether a raw command should be excluded from recording.
// The zero value is not usable; construct one with New.
type Matcher struct {
	patterns []*regexp.Regexp
}

// New builds a Matcher from the built-in sensitive patterns plus any
// user-supplied extra patterns, which are treated as regular expressions.
// Invalid extra patterns are skipped so a single bad config entry cannot
// disable recording or crash the daemon.
func New(extra []string) *Matcher {
	patterns := make([]*regexp.Regexp, len(builtinPatterns), len(builtinPatterns)+len(extra))
	copy(patterns, builtinPatterns)
	for _, p := range extra {
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		patterns = append(patterns, re)
	}
	return &Matcher{patterns: patterns}
}

// IsSensitive reports whether raw matches any sensitive pattern and should
// therefore not be recorded.
func (m *Matcher) IsSensitive(raw string) bool {
	for _, re := range m.patterns {
		if re.MatchString(raw) {
			return true
		}
	}
	return false
}
