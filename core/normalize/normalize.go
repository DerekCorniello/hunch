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
// Compound commands separated by |, &&, ||, or ; are split and each
// segment normalized independently, then rejoined with the operator.
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

	// Split on compound operators and normalize each segment.
	return normalizeSegments(tokens, parents)
}

// normalizeSegments splits tokens on |, ||, &&, and ;, normalizes each
// segment independently, and rejoins with the operator.
func normalizeSegments(tokens []string, parents []string) string {
	var segments []string
	var current []string

	for _, tok := range tokens {
		if tok == "|" || tok == "||" || tok == "&&" || tok == ";" {
			if len(current) > 0 {
				segments = append(segments, normalizeOne(current, parents))
				current = nil
			}
			segments = append(segments, tok)
		} else {
			current = append(current, tok)
		}
	}
	if len(current) > 0 {
		segments = append(segments, normalizeOne(current, parents))
	}

	return strings.Join(segments, " ")
}

// normalizeOne normalizes a single command segment (no operators).
func normalizeOne(tokens []string, parents []string) string {
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
