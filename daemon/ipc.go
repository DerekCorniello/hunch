package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
)

// request is a parsed IPC request.
type request struct {
	Op      string   `json:"op"`
	State   []string `json:"state,omitempty"`
	Next    string   `json:"next,omitempty"`
	Outcome string   `json:"outcome,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
	At      string   `json:"at,omitempty"`
	Prefix  string   `json:"prefix,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// suggestionJSON is the JSON shape for a single suggestion in a predict response.
type suggestionJSON struct {
	Template string  `json:"template"`
	Score    float64 `json:"score"`
	Count    int     `json:"count"`
}

// transitionJSON is the JSON shape for a single transition in an export response.
type transitionJSON struct {
	State    []string `json:"state"`
	Next     string   `json:"next"`
	Count    int      `json:"count"`
	LastSeen string   `json:"last_seen"`
}

// parseRequest reads one JSON object from conn and returns the parsed request.
func parseRequest(conn net.Conn) (request, error) {
	var req request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return req, fmt.Errorf("decode request: %w", err)
	}
	return req, nil
}

// writeOK writes {"ok":true} to conn.
func writeOK(conn net.Conn) error {
	_, err := fmt.Fprint(conn, `{"ok":true}`+"\n")
	return err
}

// writeSuggestions writes a predict response.
func writeSuggestions(conn net.Conn, suggestions []types.Suggestion) error {
	sj := make([]suggestionJSON, len(suggestions))
	for i, s := range suggestions {
		sj[i] = suggestionJSON{
			Template: s.Template,
			Score:    s.Score,
			Count:    s.Count,
		}
	}
	resp := struct {
		Suggestions []suggestionJSON `json:"suggestions"`
	}{Suggestions: sj}
	return json.NewEncoder(conn).Encode(resp)
}

// writeTransitions writes an export response.
func writeTransitions(conn net.Conn, transitions []graph.Transition) error {
	tj := make([]transitionJSON, len(transitions))
	for i, t := range transitions {
		tj[i] = transitionJSON{
			State:    t.State,
			Next:     t.Next,
			Count:    t.Count,
			LastSeen: t.LastSeen.Format(time.RFC3339),
		}
	}
	resp := struct {
		Transitions []transitionJSON `json:"transitions"`
	}{Transitions: tj}
	return json.NewEncoder(conn).Encode(resp)
}

// writeError writes an error response.
func writeError(conn net.Conn, msg string) error {
	resp := struct {
		Error string `json:"error"`
	}{Error: msg}
	return json.NewEncoder(conn).Encode(resp)
}

// parseAt parses an RFC 3339 timestamp. Returns the zero time if s is empty.
func parseAt(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse at: %w", err)
	}
	return t, nil
}
