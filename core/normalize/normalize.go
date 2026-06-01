package normalize

import "strings"

// Normalize converts a raw shell command into its normalized template form
// using a two-phase pipeline: unwrap wrappers, then classify tokens.
//
// If parents is nil, DefaultParents is used.
func Normalize(raw string, parents []string) string {
	if parents == nil {
		parents = DefaultParents
	}
	tokens := tokenize(raw)
	if len(tokens) == 0 {
		return ""
	}
	tokens = unwrapPrefixTokens(tokens)
	tokens = classifyTokens(tokens, parents)
	return strings.Join(tokens, " ")
}
