// Package normalize converts raw shell commands into normalized
// template form using a two-phase pipeline.
//
// Phase 1 (unwrap) strips leading wrapper commands such as sudo,
// nice, env, and chroot, recursing into the wrapped command. This
// collapses `sudo apt update` to the same template as `apt update`.
//
// Phase 2 (classify) splits the command into tokens and classifies
// each by shape: flags, paths, URLs, hashes, numbers, quoted
// strings, and known-parent subcommands. Consecutive tokens of the
// same type are collapsed into a single representative token.
//
// The output is a deterministic template suitable for use as a key
// in a transition graph (e.g. "git push STR" or "mkdir PATH").
//
// Normalize is pure and stateless except for the caller-supplied
// parent set; it performs no IO.
package normalize

import "strings"

// Normalize converts a raw shell command into its normalized template form.
//
// If parents is nil, DefaultParents is used; pass an explicit list to
// override (e.g. when extending or restricting the set of tools whose
// subcommand is preserved verbatim).
//
// Returns an empty string for empty or whitespace-only input.
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

// MergeParents returns DefaultParents merged with extras. If extras is
// empty, DefaultParents is returned unchanged.
func MergeParents(extras []string) []string {
	if len(extras) == 0 {
		return DefaultParents
	}
	combined := make([]string, 0, len(DefaultParents)+len(extras))
	combined = append(combined, DefaultParents...)
	combined = append(combined, extras...)
	return combined
}
