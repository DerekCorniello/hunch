package ipc

import "strings"

// placeholders are the token types normalization substitutes for concrete
// values. A template containing any of them describes a shape rather than a
// command.
var placeholders = map[string]bool{
	"FLAG":   true,
	"PATH":   true,
	"REPO":   true,
	"HASH":   true,
	"NUM":    true,
	"STR":    true,
	"KWARGS": true,
}

// DisplayCommand returns the text to show for a suggestion.
//
// Suggestions carry both a normalized template and, when one is known, the
// concrete command that produced it. The template is only a fallback, and only
// a safe one when it survived normalization unchanged: showing
// "git commit FLAG STR" as a suggestion offers the user something they cannot
// run, which is worse than showing nothing. An empty result means the caller
// should display nothing.
func DisplayCommand(raw, template string) string {
	if raw != "" {
		return raw
	}
	for _, tok := range strings.Fields(template) {
		if placeholders[tok] {
			return ""
		}
	}
	return template
}
