package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/normalize"
	"github.com/DerekCorniello/hunch/core/predict"
	"github.com/DerekCorniello/hunch/core/types"
	"github.com/DerekCorniello/hunch/ipc"
)

// connDeadline bounds how long a single request may occupy a connection,
// so a client that stalls mid-write cannot pin a goroutine indefinitely.
const connDeadline = 30 * time.Second

func (d *daemon) handleConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("panic handling connection", "recover", r)
		}
		conn.Close()
	}()

	_ = conn.SetDeadline(time.Now().Add(connDeadline))

	req, err := parseRequest(conn)
	if err != nil {
		d.respondError(conn, "bad request")
		return
	}

	_ = conn.SetDeadline(time.Now().Add(connDeadline))

	switch req.Op {
	case "record":
		d.log.Debug("record", "state", req.State, "next", req.Next)
		d.handleRecord(conn, req)
	case "predict":
		d.log.Debug("predict", "state", req.State, "prefix", req.Prefix)
		d.handlePredict(conn, req)
	case "reset":
		d.handleReset(conn)
	case "export":
		d.handleExport(conn)
	case "stats":
		d.handleStats(conn)
	case "normalize":
		d.handleNormalize(conn, req)
	case "config":
		d.handleConfig(conn)
	case "import":
		d.handleImport(conn, req)
	case "record_raws":
		d.handleRecordRaws(conn, req)
	default:
		d.respondError(conn, "unknown op: %s", req.Op)
	}
}

// Response helpers
//
// A failed response write means the client hung up mid-request: there is
// nothing to retry and no caller to return an error to. It is still logged,
// because a persistent pattern of write failures is a real symptom and
// discarding the error outright would hide it.

func (d *daemon) respondOK(conn net.Conn) {
	if err := writeOK(conn); err != nil {
		d.log.Debug("write ok response", "error", err)
	}
}

func (d *daemon) respondError(conn net.Conn, format string, args ...any) {
	if err := writeError(conn, fmt.Sprintf(format, args...)); err != nil {
		d.log.Debug("write error response", "error", err)
	}
}

func (d *daemon) respondSuggestions(conn net.Conn, suggestions []types.Suggestion) {
	if err := writeSuggestions(conn, suggestions); err != nil {
		d.log.Debug("write suggestions response", "error", err)
	}
}

func (d *daemon) respondTransitions(conn net.Conn, transitions []graph.Transition) {
	if err := writeTransitions(conn, transitions); err != nil {
		d.log.Debug("write transitions response", "error", err)
	}
}

// respondJSON marshals v as the whole response. what names the payload so a
// marshal failure is attributable.
func (d *daemon) respondJSON(conn net.Conn, what string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		d.log.Error("marshal response", "what", what, "error", err)
		d.respondError(conn, "marshal %s", what)
		return
	}
	if _, err := fmt.Fprintln(conn, string(data)); err != nil {
		d.log.Debug("write json response", "what", what, "error", err)
	}
}

// requestTime resolves the timestamp a request should be recorded at,
// defaulting to now when the client did not supply one.
func requestTime(raw string) (time.Time, error) {
	at, err := parseAt(raw)
	if err != nil {
		return time.Time{}, err
	}
	if at.IsZero() {
		return time.Now(), nil
	}
	return at, nil
}

func (d *daemon) handleRecord(conn net.Conn, req ipc.Request) {
	at, err := requestTime(req.At)
	if err != nil {
		d.respondError(conn, "bad at: %s", err)
		return
	}

	// Drop commands that look like they carry secrets: neither the
	// transition nor the raw example is recorded, so nothing sensitive is
	// ever persisted or suggested back. Reported as OK so the caller is
	// unaware which commands were skipped.
	if d.redactor.IsSensitive(req.Next) {
		d.log.Debug("skipping sensitive command")
		d.respondOK(conn)
		return
	}

	normalizedState := make([]string, 0, len(req.State)+1)
	if req.CWD != "" {
		normalizedState = append(normalizedState, filepath.Clean(req.CWD))
	}
	for _, raw := range req.State {
		normalizedState = append(normalizedState, normalize.Normalize(raw, d.parents))
	}
	normalizedNext := normalize.Normalize(req.Next, d.parents)

	// Acceptance: the executed command confirms a suggestion when its
	// template matches the template hunch last showed for this state, even
	// if the user edited a value (normalization collapses STR/PATH/etc.).
	accepted := req.Suggested != "" && normalize.Normalize(req.Suggested, d.parents) == normalizedNext

	states := backoffStates(normalizedState, req.CWD != "")

	g := d.g.Load()
	for _, state := range states {
		g.RecordObs(graph.Observation{
			State:        state,
			Next:         normalizedNext,
			At:           at,
			CWD:          req.CWD,
			NextOutcome:  graph.Outcome(req.Outcome),
			PriorOutcome: graph.Outcome(req.PriorOutcome),
			Accepted:     accepted,
		})
	}

	// Store the raw example under the same keys as the graph. A suggestion
	// reached through a generalization must be able to find a concrete
	// command, or it would be rendered as a bare template like
	// "git commit FLAG STR", which is not runnable.
	for _, state := range states {
		d.raws.record(state, normalizedNext, req.Next, at)
	}

	if d.dirty.Add(1) >= flushThreshold {
		select {
		case d.flushCh <- struct{}{}:
		default:
		}
	}

	d.respondOK(conn)
}

// backoffStates expands one observation into the state keys it should be
// recorded under: the exact context, plus progressively more general ones.
//
// Without this the prediction fallbacks cannot fire. State keys are compared
// by exact join, so a query for ["git status"] never matches a recording of
// ["", "git status"], and a query with no directory never matches a recording
// made with one. Recording the generalizations is what lets a workflow learned
// in one directory, or after a longer run of commands, still apply elsewhere.
//
// The fully general (empty) key is deliberately omitted: Merge rejects
// transitions with empty state, so persisting one would produce rows that fail
// to load on restart. It would only ever predict the single most frequent
// command anyway, which is the baseline rather than a useful suggestion.
func backoffStates(state []string, hasCWD bool) [][]string {
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

func (d *daemon) handleRecordRaws(conn net.Conn, req ipc.Request) {
	d.raws.mergeExamples(req.RawExamples, time.Now())
	d.respondOK(conn)
}

func (d *daemon) handlePredict(conn net.Conn, req ipc.Request) {
	at, err := requestTime(req.At)
	if err != nil {
		d.respondError(conn, "bad at: %s", err)
		return
	}

	prev := make([]types.Command, len(req.State))
	for i, raw := range req.State {
		prev[i] = types.Command{Template: normalize.Normalize(raw, d.parents)}
	}

	cwd := req.CWD
	if cwd != "" {
		cwd = filepath.Clean(cwd)
	}

	suggestions := d.predictWithFallback(prev, cwd, types.Outcome(req.PriorOutcome), req.Prefix, at)

	if req.Prefix != "" {
		suggestions = filterByPrefix(suggestions, req.Prefix, d.parents)
	}
	suggestions = suppressCdToCurrent(suggestions, cwd)

	if req.Limit > 0 && len(suggestions) > req.Limit {
		suggestions = suggestions[:req.Limit]
	}

	// Hydrate templates into runnable commands. Argument tokens from the
	// immediately preceding commands bias this toward reusing the same file
	// or script name the user just typed.
	argTokens := collectArgTokens(req.State, d.parents)
	stateTemplates := make([]string, 0, len(prev))
	for _, cmd := range prev {
		if cmd.Template != "" {
			stateTemplates = append(stateTemplates, cmd.Template)
		}
	}
	d.raws.hydrate(suggestions, stateTemplates, req.Prefix, argTokens, at)

	d.respondSuggestions(conn, suggestions)
}

// withMinCount drops suggestions backed by too few observations.
//
// This is deliberately applied to every level, including the exact-context
// match that MinConfidence exempts. The two guards answer different questions:
// MinConfidence asks how likely a candidate is, while this asks how much is
// known about it. A command run once in a context nobody revisits is the only
// candidate there, so it scores 1.0 and would otherwise be offered as
// confidently as a daily habit.
func withMinCount(suggestions []types.Suggestion, minCount int) []types.Suggestion {
	if minCount <= 1 {
		return suggestions
	}
	kept := suggestions[:0]
	for _, s := range suggestions {
		if s.Count >= minCount {
			kept = append(kept, s)
		}
	}
	return kept
}

// predictWithFallback queries the predictor through progressively more general
// state keys, stopping at the first level that yields a usable answer.
//
// The levels trade specificity for coverage: an exact directory match is the
// strongest signal, a trimmed history with no directory the weakest. Only the
// exact match is trusted unconditionally. Every broader level must clear
// MinConfidence, because a generalized context nearly always has *some*
// candidate, and offering it regardless turns a fallback into a guess. Ghost
// text that is usually wrong trains people to ignore it, which costs more than
// staying quiet.
//
// Only an unfiltered query walks the ladder; when the caller supplied a
// prefix, a broadened match would suggest commands unrelated to what they are
// typing, so level 1 stands alone.
func (d *daemon) predictWithFallback(prev []types.Command, cwd string, prior types.Outcome, prefix string, at time.Time) []types.Suggestion {
	query := func(previous []types.Command, dir string) []types.Suggestion {
		return withMinCount(d.pred.Load().Predict(types.State{
			Previous:     previous,
			CWD:          dir,
			PriorOutcome: prior,
		}, at, 0), d.opts.MinCount)
	}
	// confident reports whether a generalized match is strong enough to show.
	confident := func(s []types.Suggestion) bool {
		return len(s) > 0 && s[0].Score >= d.opts.MinConfidence
	}

	// Level 1: this exact directory, as learned from live sessions.
	suggestions := query(prev, cwd)
	if len(suggestions) > 0 || prefix != "" {
		return suggestions
	}

	// Level 2: ancestor directories, so a workflow learned in ~/project
	// still applies in ~/project/src.
	if cwd != "" {
		for parent := filepath.Dir(cwd); parent != cwd && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if s := query(prev, parent); confident(s) {
				return s
			}
		}
	}

	// Level 3: no directory at all, which is how imported shell history and
	// anything recorded before CWD tracking is keyed.
	if s := query(prev, ""); confident(s) {
		return s
	}

	// Level 4: progressively shorter history windows.
	for trim := 1; trim <= len(prev); trim++ {
		if s := query(prev[trim:], ""); confident(s) {
			return s
		}
	}
	return nil
}

func (d *daemon) handleReset(conn net.Conn) {
	d.flushMu.Lock()
	newG := graph.New(2)
	d.g.Store(newG)
	d.pred.Store(d.newPredictor(newG))
	if err := d.st.clear(); err != nil {
		d.log.Error("clear store", "error", err)
	}
	d.raws.reset()
	d.dirty.Store(0)
	d.flushMu.Unlock()

	d.respondOK(conn)
}

func (d *daemon) handleExport(conn net.Conn) {
	d.respondTransitions(conn, d.g.Load().All())
}

func (d *daemon) handleStats(conn net.Conn) {
	d.respondJSON(conn, "stats", ipc.StatsResponse{
		Size:     d.g.Load().Size(),
		HalfLife: d.opts.HalfLife().String(),
		Alpha:    d.opts.Alpha,
		DBPath:   d.opts.DBPath,
	})
}

func (d *daemon) handleConfig(conn net.Conn) {
	d.respondJSON(conn, "config", ipc.ConfigResponse{
		AcceptKeys:   d.opts.AcceptKeys,
		ExtraParents: d.opts.ExtraParents,
		HalfLife:     d.opts.HalfLife().String(),
		Alpha:        d.opts.Alpha,
	})
}

func (d *daemon) handleNormalize(conn net.Conn, req ipc.Request) {
	raw := req.Next
	if raw == "" && len(req.State) > 0 {
		raw = req.State[len(req.State)-1]
	}
	if raw == "" {
		d.respondError(conn, "no input to normalize")
		return
	}
	d.respondJSON(conn, "normalize", ipc.NormalizeResponse{
		Raw:      raw,
		Template: normalize.Normalize(raw, d.parents),
	})
}

func (d *daemon) handleImport(conn net.Conn, req ipc.Request) {
	if req.Next == "" {
		d.respondError(conn, "import path required")
		return
	}
	path, err := filepath.Abs(req.Next)
	if err != nil {
		d.respondError(conn, "bad path: %v", err)
		return
	}
	if err := checkImportable(path); err != nil {
		d.respondError(conn, "%s", err)
		return
	}
	if err := d.importSeed(path); err != nil {
		d.respondError(conn, "import failed: %v", err)
		return
	}
	d.pred.Store(d.newPredictor(d.g.Load()))
	d.respondOK(conn)
}

// checkImportable rejects anything that is not a plain on-disk file. The seed
// path arrives over IPC, so a symlink or device node here would let a caller
// steer the daemon at a file it should not read.
func checkImportable(path string) error {
	// Lstat, not Stat, so a symlink is detected rather than followed.
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file not found")
		}
		return fmt.Errorf("stat: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlinks not allowed")
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	return nil
}

// newPredictor builds a predictor over g using the daemon's tuning options.
func (d *daemon) newPredictor(g *graph.Graph) *predict.Predictor {
	return predict.New(g, d.opts.HalfLife(), d.opts.Alpha, d.opts.Beta, d.opts.Gamma, d.opts.Delta, d.opts.Epsilon)
}
