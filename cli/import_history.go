package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/DerekCorniello/hunch/core/graph"
	"github.com/DerekCorniello/hunch/core/normalize"
	"github.com/DerekCorniello/hunch/daemon"
	"github.com/DerekCorniello/hunch/ipc"
)

func cmdImportHistory(args []string) error {
	var path string
	var threads int

	fs := flag.NewFlagSet("hunch import-history", flag.ContinueOnError)
	fs.StringVar(&path, "path", "", "history file path (overrides default ~/.zsh_history)")
	fs.IntVar(&threads, "threads", runtime.NumCPU(), "number of normalize threads")
	if err := fs.Parse(args); err != nil {
		return err
	}

	historyPath, _, err := resolveHistoryPath(path)
	if err != nil {
		return err
	}

	if err := ensureDaemonRunning(); err != nil {
		return fmt.Errorf("daemon must be running to import history: %w", err)
	}

	return runImport(historyPath, threads, func(msg string) {
		fmt.Print(msg)
	})
}

// resolveHistoryPath resolves the zsh history file to import: an explicit
// override if given, otherwise ~/.zsh_history.
func resolveHistoryPath(override string) (string, int, error) {
	if override != "" {
		_, err := os.Stat(override)
		if err != nil {
			return "", 0, fmt.Errorf("history file not found: %s", override)
		}
		return override, countLines(override), nil
	}

	path := resolveHome("~/.zsh_history")
	return path, countLines(path), nil
}

// maxHistoryLine bounds a single history line. The default bufio.Scanner
// limit (64 KiB) silently drops longer lines; shell history can legitimately
// contain very long one-liners, so allow up to 1 MiB per line.
const maxHistoryLine = 1 << 20

// newHistoryScanner returns a line scanner that tolerates lines up to
// maxHistoryLine bytes instead of the 64 KiB default.
func newHistoryScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxHistoryLine)
	return sc
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var n int
	sc := newHistoryScanner(f)
	for sc.Scan() {
		n++
	}
	return n
}

func ensureDaemonRunning() error {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return errors.New("could not determine socket path")
	}
	conn, err := daemon.Dial(opts.Socket, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}
	return cmdDaemonStart()
}

func runImport(path string, threads int, progress func(string)) error {
	progress("Parsing zsh history")

	rawCmds, err := parseZshHistory(path)
	if err != nil {
		return fmt.Errorf("parse history: %w", err)
	}
	if len(rawCmds) == 0 {
		progress(" - no commands found\n")
		return nil
	}
	progress(fmt.Sprintf(" - %d commands, ", len(rawCmds)))

	normalized, err := normalizeConcurrent(rawCmds, threads)
	if err != nil {
		return fmt.Errorf("normalize: %w", err)
	}
	progress("normalized, ")

	transitions := buildTransitions(normalized)
	progress(fmt.Sprintf("%d transitions, ", len(transitions)))

	if err := sendSeed(transitions); err != nil {
		return fmt.Errorf("import to daemon: %w", err)
	}

	rawExamples := buildRawMappings(rawCmds, normalized)
	if err := sendRawExamples(rawExamples); err != nil {
		return fmt.Errorf("send raw examples to daemon: %w", err)
	}

	progress("imported.\n")
	return nil
}

func normalizeConcurrent(rawCmds []string, threads int) ([]string, error) {
	normalized := make([]string, len(rawCmds))
	parents := normalize.DefaultParents

	type job struct {
		idx int
		raw string
	}
	jobs := make(chan job, len(rawCmds))

	var wg sync.WaitGroup
	for range threads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				normalized[j.idx] = normalize.Normalize(j.raw, parents)
			}
		}()
	}

	for i, cmd := range rawCmds {
		jobs <- job{idx: i, raw: cmd}
	}
	close(jobs)
	wg.Wait()

	return normalized, nil
}

func buildTransitions(normalized []string) []graph.Transition {
	g := graph.New(2)
	now := time.Now()

	var prev1, prev2 string
	for _, cmd := range normalized {
		if cmd != "" {
			// Expand exactly as the daemon does, so an imported graph
			// supports the same fallbacks as a learned one.
			for _, state := range graph.BackoffStates([]string{prev1, prev2}, false) {
				g.Record(state, cmd, now)
			}
		}
		prev1 = prev2
		prev2 = cmd
	}
	return g.All()
}

func sendSeed(transitions []graph.Transition) error {
	seed := graph.Seed{
		Version:     1,
		Source:      "hunch import-history",
		GeneratedAt: time.Now(),
		Transitions: transitions,
	}

	data, err := json.Marshal(seed)
	if err != nil {
		return fmt.Errorf("marshal seed: %w", err)
	}

	req := ipc.Request{
		Op:       "import",
		SeedData: string(data),
	}
	_, err = sendRequest(req)
	return err
}

func buildRawMappings(rawCmds, normalized []string) []ipc.RawExampleJSON {
	// Expand exactly as buildTransitions does. A raw example keyed only to
	// the exact context cannot be found when a prediction arrives through a
	// generalization, and a suggestion with no concrete command behind it is
	// suppressed rather than shown as a bare template.
	type stateKey struct {
		state    string
		template string
		raw      string
	}
	stateCounts := make(map[stateKey]int)
	states := make(map[string][]string)

	var prev1, prev2 string
	for i, tmpl := range normalized {
		if tmpl != "" && rawCmds[i] != "" {
			for _, st := range graph.BackoffStates([]string{prev1, prev2}, false) {
				trimmed := nonEmpty(st)
				joined := strings.Join(trimmed, "\x00")
				states[joined] = trimmed
				stateCounts[stateKey{joined, tmpl, rawCmds[i]}]++
			}
		}
		prev1 = prev2
		prev2 = tmpl
	}

	list := make([]ipc.RawExampleJSON, 0, len(stateCounts))
	for k, count := range stateCounts {
		list = append(list, ipc.RawExampleJSON{
			State:    states[k.state],
			Template: k.template,
			Raw:      k.raw,
			Count:    count,
		})
	}
	return list
}

// nonEmpty drops the empty padding from a state slice, matching how the
// daemon keys raw examples.
func nonEmpty(state []string) []string {
	out := make([]string, 0, len(state))
	for _, s := range state {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sendRawExamples(examples []ipc.RawExampleJSON) error {
	req := ipc.Request{
		Op:          "record_raws",
		RawExamples: examples,
	}
	_, err := sendRequest(req)
	return err
}

func parseZshHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cmds []string
	sc := newHistoryScanner(f)
	for sc.Scan() {
		line := sc.Text()
		cmd := stripZshMeta(line)
		if cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	return cmds, sc.Err()
}

func stripZshMeta(line string) string {
	if len(line) < 1 || line[0] != ':' {
		return ""
	}
	i := strings.IndexByte(line[1:], ':')
	if i < 0 {
		return ""
	}
	i++ // account for skipped first ':'
	rest := line[i+1:]
	if j := strings.IndexByte(rest, ';'); j >= 0 {
		return rest[j+1:]
	}
	return ""
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func resolveHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
