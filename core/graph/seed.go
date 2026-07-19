package graph

import (
	"fmt"
	"time"
)

// Seed is the top-level container for exported or seed transition data.
//
// The metadata fields provide provenance for debugging and forward
// compatibility; they are informational only and are not used by Merge.
type Seed struct {
	Version     int          `json:"version"`          // schema version (currently 1)
	Source      string       `json:"source,omitempty"` // e.g. "hunch export" or a community pack name
	GeneratedAt time.Time    `json:"generated_at"`     // when this seed was exported
	Transitions []Transition `json:"transitions"`      // the transition data
}

// Validate checks a seed read from outside the daemon.
//
// It is stricter than the checks Merge applies, because a seed file is
// untrusted input while the database is data hunch wrote itself. In
// particular a zero LastSeen is rejected: it almost always means the
// timestamp field did not unmarshal, which is invisible at import time and
// then deletes the whole seed on the next start, when decay reads those
// transitions as two thousand years old. Failing here turns silent delayed
// data loss into an error at the point of the mistake.
func (s Seed) Validate() error {
	if len(s.Transitions) == 0 {
		return fmt.Errorf("seed contains no transitions")
	}
	for i, t := range s.Transitions {
		if t.LastSeen.IsZero() {
			return fmt.Errorf("transition %d (%q): missing or unparseable last_seen", i, t.Next)
		}
	}
	return nil
}
