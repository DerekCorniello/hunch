package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/types"
	"github.com/DerekCorniello/hunch/ipc"
)

// parseRequest reads one JSON object from conn and returns the parsed request.
func parseRequest(conn net.Conn) (ipc.Request, error) {
	var req ipc.Request
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
	sj := make([]ipc.SuggestionJSON, len(suggestions))
	for i, s := range suggestions {
		sj[i] = ipc.SuggestionJSON{
			Template: s.Template,
			Score:    s.Score,
			Count:    s.Count,
		}
	}
	resp := ipc.SuggestionsResponse{Suggestions: sj}
	return json.NewEncoder(conn).Encode(resp)
}

// writeTransitions writes an export response.
func writeTransitions(conn net.Conn, transitions []graph.Transition) error {
	tj := make([]ipc.TransitionJSON, len(transitions))
	for i, t := range transitions {
		tj[i] = ipc.TransitionFromGraph(t)
	}
	resp := ipc.TransitionsResponse{Transitions: tj}
	return json.NewEncoder(conn).Encode(resp)
}

// writeError writes an error response.
func writeError(conn net.Conn, msg string) error {
	resp := ipc.ErrorResponse{Error: msg}
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
