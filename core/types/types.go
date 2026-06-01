package types

// Outcome represents the result of a command execution.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Command represents a shell command that was executed.
type Command struct {
	Raw      string
	Template string
	Outcome  Outcome
	CWD      string
}

// State represents the current context used for prediction.
type State struct {
	Previous []Command
	CWD      string
}

// Suggestion represents a predicted next command with its score.
type Suggestion struct {
	Template string
	Score    float64
	Count    int
}
