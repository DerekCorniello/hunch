package graph

import "time"

// Seed is the top-level container for exported or seed transition data.
//
// The metadata fields provide provenance for debugging and forward
// compatibility; they are informational only and are not used by Merge.
type Seed struct {
	Version     int          `json:"version"`               // schema version (currently 1)
	Source      string       `json:"source,omitempty"`      // e.g. "hunch export" or a community pack name
	GeneratedAt time.Time    `json:"generated_at"`          // when this seed was exported
	Transitions []Transition `json:"transitions"`           // the transition data
}
