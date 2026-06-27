// Package types defines the shared domain models used across the
// hunch codebase: commands, outcomes, the prediction state, and
// the suggestion returned to callers.
package types

// Outcome represents the result of a command execution.
//
// It is intentionally coarse — hunch uses outcomes to influence
// transition weights (e.g. a successful command makes the following
// command a stronger candidate for suggestion), not to model every
// possible exit condition.
type Outcome string

const (
	OutcomeUnknown Outcome = ""
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Command represents a single shell command observed by hunch.
//
// Raw is the original command string as entered by the user; Template
// is its normalized form suitable for use as a graph key; Outcome is
// the result of execution; CWD is the working directory in which the
// command was run.
type Command struct {
	Raw      string
	Template string
	Outcome  Outcome
	CWD      string
}

// State represents the context used to generate predictions.
//
// Previous is the recent command history, most recent last. CWD is the
// current working directory, used to boost transitions seen in the same
// location. PriorOutcome is the outcome of the most recent command, used
// to weight transitions by the prior-command result.
type State struct {
	Previous     []Command
	CWD          string
	PriorOutcome Outcome
}

// Suggestion is a predicted next command.
//
// Template is the normalized form (the graph key). Raw is the most
// common raw command observed for this template under the queried state.
// Score is a relative ranking in [0, 1]; callers should treat it as a
// comparison value, not a probability. Count is the number of observed
// transitions that produced this suggestion.
type Suggestion struct {
	Template string
	Raw      string
	Score    float64
	Count    int
}
