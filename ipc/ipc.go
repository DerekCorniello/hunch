package ipc

import (
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
)

// Request is a parsed IPC request.
type Request struct {
	Op     string   `json:"op"`
	State  []string `json:"state,omitempty"`
	Next   string   `json:"next,omitempty"`
	At     string   `json:"at,omitempty"`
	Prefix string   `json:"prefix,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

// SuggestionJSON is the JSON shape for a single suggestion in a predict response.
type SuggestionJSON struct {
	Template string  `json:"template"`
	Raw      string  `json:"raw,omitempty"`
	Score    float64 `json:"score"`
	Count    int     `json:"count"`
}

// TransitionJSON is the JSON shape for a single transition in an export response.
type TransitionJSON struct {
	State    []string `json:"state"`
	Next     string   `json:"next"`
	Count    int      `json:"count"`
	LastSeen string   `json:"last_seen"`
}

// OKResponse is a standard success response.
type OKResponse struct {
	OK bool `json:"ok"`
}

// SuggestionsResponse is a predict response.
type SuggestionsResponse struct {
	Suggestions []SuggestionJSON `json:"suggestions"`
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

// TransitionFromGraph converts a graph.Transition to TransitionJSON.
func TransitionFromGraph(t graph.Transition) TransitionJSON {
	return TransitionJSON{
		State:    t.State,
		Next:     t.Next,
		Count:    t.Count,
		LastSeen: t.LastSeen.Format(time.RFC3339),
	}
}
