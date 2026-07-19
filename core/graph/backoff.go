package graph

// BackoffStates expands one observation into the state keys it should be
// recorded under: the exact context, plus progressively more general ones.
//
// Recording only the exact context makes the generalized lookups unreachable.
// State keys compare by exact join, so a query for ["git status"] never
// matches a recording of ["", "git status"], and a query with no directory
// never matches a recording made with one. Every producer of transitions has
// to expand the same way or the data it writes cannot be found by the
// fallbacks: that is why this lives here rather than in the daemon.
//
// The fully general (empty) key is deliberately omitted. Merge rejects
// transitions with empty state, so persisting one would produce rows that
// fail to load, and it would only ever predict the single most frequent
// command anyway, which is the baseline rather than a useful suggestion.
func BackoffStates(state []string, hasCWD bool) [][]string {
	states := [][]string{state}

	// With a directory prefix, the same context without it is a distinct and
	// more general key that would otherwise never be recorded.
	templates := state
	if hasCWD && len(state) > 0 {
		templates = state[1:]
		states = appendIfInformative(states, templates)
	}

	// Drop the oldest command for a shorter-history key.
	if len(templates) > 1 {
		states = appendIfInformative(states, templates[1:])
	}
	return states
}

// appendIfInformative adds a generalization only when it carries context. At
// the start of a session the history is empty padding, and a key of nothing
// but empty strings would collapse every such observation into one bucket
// that predicts the user's most frequent command regardless of context.
func appendIfInformative(states [][]string, candidate []string) [][]string {
	if len(candidate) == 0 || allEmpty(candidate) {
		return states
	}
	return append(states, candidate)
}

func allEmpty(state []string) bool {
	for _, s := range state {
		if s != "" {
			return false
		}
	}
	return true
}
