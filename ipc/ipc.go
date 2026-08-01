package ipc

import (
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/predict"
	"github.com/DerekCorniello/hunch/core/types"
)

// Request is a parsed IPC request.
type Request struct {
	Op           string           `json:"op"`
	State        []string         `json:"state,omitempty"`
	Next         string           `json:"next,omitempty"`
	At           string           `json:"at,omitempty"`
	Prefix       string           `json:"prefix,omitempty"`
	Limit        int              `json:"limit,omitempty"`
	RawExamples  []RawExampleJSON `json:"raw_examples,omitempty"`
	CWD          string           `json:"cwd,omitempty"`           // working dir: where Next ran (record) or current (predict)
	Outcome      string           `json:"outcome,omitempty"`       // outcome of Next (record): "success"/"failure"/""
	PriorOutcome string           `json:"prior_outcome,omitempty"` // outcome of the command preceding Next / most recent command
	Suggested    string           `json:"suggested,omitempty"`     // raw suggestion hunch last showed (record): for acceptance detection
	SeedData     string           `json:"seed_data,omitempty"`     // inline seed JSON for import (alternative to file path)
}

// RawExampleJSON is a single state+template->raw observation carried by the
// record_raws op, used to bulk-load raw command examples (e.g. from
// shell-history import) without smuggling a JSON blob through Next.
// State holds the normalized prior-command templates (same ordering as the
// graph state); omit or leave empty for a global (stateless) example.
// LastSeen is a Unix timestamp; 0 means "use server time".
type RawExampleJSON struct {
	State    []string `json:"state,omitempty"`
	Template string   `json:"template"`
	Raw      string   `json:"raw"`
	Count    int      `json:"count"`
	LastSeen int64    `json:"last_seen,omitempty"`
}

// ServeRequest is one line of input to the `hunch client serve` loop, one
// JSON object per line. JSON framing keeps commands containing tabs or
// newlines from corrupting the stream.
type ServeRequest struct {
	Prefix       string   `json:"prefix"`                  // current command-line buffer
	State        []string `json:"state,omitempty"`         // previous raw commands, most recent last
	CWD          string   `json:"cwd,omitempty"`           // current working directory
	PriorOutcome string   `json:"prior_outcome,omitempty"` // outcome of the most recent command
}

// ServeResponse is one line of output from the serve loop. Prefix is echoed
// verbatim so the integration can discard responses for a buffer the user has
// already moved past. Raws holds the ranked raw suggestions (most likely
// first); the integration shows the first and lets the user cycle the rest.
type ServeResponse struct {
	Prefix string   `json:"prefix"`
	Raws   []string `json:"raws"`
}

// TransitionJSON is the JSON shape for a single transition in an export response.
type TransitionJSON struct {
	State    []string       `json:"state"`
	Next     string         `json:"next"`
	Count    int            `json:"count"`
	LastSeen string         `json:"last_seen"`
	CWDs     map[string]int `json:"cwds,omitempty"`

	NextSuccess  int `json:"next_success,omitempty"`
	NextFailure  int `json:"next_failure,omitempty"`
	PriorSuccess int `json:"prior_success,omitempty"`
	PriorFailure int `json:"prior_failure,omitempty"`
	Accepted     int `json:"accepted,omitempty"`
}

// OKResponse is a standard success response.
type OKResponse struct {
	OK bool `json:"ok"`
}

// SuggestionsResponse is a predict response.
type SuggestionsResponse struct {
	Suggestions []types.Suggestion `json:"suggestions"`
}

// TransitionsResponse is an export response.
type TransitionsResponse struct {
	Transitions []TransitionJSON `json:"transitions"`
}

// ErrorResponse is an error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// StatsResponse is a stats response.
type StatsResponse struct {
	Size     int     `json:"size"`
	HalfLife string  `json:"half_life"`
	Alpha    float64 `json:"alpha"`
	DBPath   string  `json:"db_path"`
}

// NormalizeResponse is a normalize response.
type NormalizeResponse struct {
	Raw      string `json:"raw"`
	Template string `json:"template"`
}

// ConfigResponse is a config response.
type ConfigResponse struct {
	AcceptKeys   []string `json:"accept_keys"`
	ExtraParents []string `json:"extra_parents"`
	HalfLife     string   `json:"half_life"`
	Alpha        float64  `json:"alpha"`
}

// ScoreBreakdownJSON is the JSON shape for a single candidate's scoring
// breakdown in an explain response. See predict.ScoreBreakdown for what each
// field means.
type ScoreBreakdownJSON struct {
	Next  string `json:"next"`
	Count int    `json:"count"`

	DecayWeight   float64 `json:"decay_weight"`
	CWDAffinity   float64 `json:"cwd_affinity"`
	PriorAffinity float64 `json:"prior_affinity"`
	AcceptRate    float64 `json:"accept_rate"`
	FailureRate   float64 `json:"failure_rate"`

	EffCount float64 `json:"eff_count"`
	Score    float64 `json:"score"`
}

// ExplainResponse is an explain response: which fallback rung answered, the
// context it was answered under, the gating thresholds a candidate has to
// clear to be shown, and the scoring breakdown for the top candidates there.
type ExplainResponse struct {
	Level         string               `json:"level"`
	State         []string             `json:"state"`
	CWD           string               `json:"cwd,omitempty"`
	MinConfidence float64              `json:"min_confidence"`
	MinCount      int                  `json:"min_count"`
	Breakdown     []ScoreBreakdownJSON `json:"breakdown"`
}

// BreakdownFromPredict converts predictor score breakdowns to their JSON shape.
func BreakdownFromPredict(bs []predict.ScoreBreakdown) []ScoreBreakdownJSON {
	out := make([]ScoreBreakdownJSON, len(bs))
	for i, b := range bs {
		out[i] = ScoreBreakdownJSON{
			Next:          b.Next,
			Count:         b.Count,
			DecayWeight:   b.DecayWeight,
			CWDAffinity:   b.CWDAffinity,
			PriorAffinity: b.PriorAffinity,
			AcceptRate:    b.AcceptRate,
			FailureRate:   b.FailureRate,
			EffCount:      b.EffCount,
			Score:         b.Score,
		}
	}
	return out
}

// TransitionFromGraph converts a graph.Transition to TransitionJSON.
func TransitionFromGraph(t graph.Transition) TransitionJSON {
	return TransitionJSON{
		State:        t.State,
		Next:         t.Next,
		Count:        t.Count,
		LastSeen:     t.LastSeen.Format(time.RFC3339),
		CWDs:         t.CWDs,
		NextSuccess:  t.NextSuccess,
		NextFailure:  t.NextFailure,
		PriorSuccess: t.PriorSuccess,
		PriorFailure: t.PriorFailure,
		Accepted:     t.Accepted,
	}
}
