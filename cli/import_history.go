package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	var shell, path string
	var threads int

	fs := flag.NewFlagSet("hunch import-history", flag.ContinueOnError)
	fs.StringVar(&path, "path", "", "history file path (overrides default)")
	fs.IntVar(&threads, "threads", runtime.NumCPU(), "number of normalize threads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: hunch import-history <shell> [--path <file>] [--threads N]\n\nshells: zsh, bash, fish, powershell")
	}
	shell = fs.Arg(0)
	if !validShell(shell) {
		return fmt.Errorf("unknown shell: %q\n\nsupported shells: zsh, bash, fish, powershell", shell)
	}

	historyPath, _, err := resolveHistoryPath(shell, path)
	if err != nil {
		return err
	}

	if err := ensureDaemonRunning(); err != nil {
		return fmt.Errorf("daemon must be running to import history: %w", err)
	}

	return runImport(shell, historyPath, threads, func(msg string) {
		fmt.Print(msg)
	})
}

func resolveHistoryPath(shell, override string) (string, int, error) {
	if override != "" {
		_, err := os.Stat(override)
		if err != nil {
			return "", 0, fmt.Errorf("history file not found: %s", override)
		}
		return override, countLines(override), nil
	}

	switch shell {
	case "zsh":
		path := resolveHome("~/.zsh_history")
		c := countLines(path)
		return path, c, nil
	case "bash":
		path := resolveHome("~/.bash_history")
		c := countLines(path)
		return path, c, nil
	case "fish":
		path := resolveHome("~/.local/share/fish/fish_history")
		c := countLines(path)
		return path, c, nil
	case "powershell":
		return "", -1, nil
	}
	return "", 0, fmt.Errorf("unknown shell: %s", shell)
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var n int
	sc := bufio.NewScanner(f)
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

func runImport(shell, path string, threads int, progress func(string)) error {
	progress(fmt.Sprintf("Parsing %s history", shell))

	var rawCmds []string
	var err error
	switch shell {
	case "zsh":
		rawCmds, err = parseZshHistory(path)
	case "bash":
		rawCmds, err = parseBashHistory(path)
	case "fish":
		rawCmds, err = parseFishHistory(path)
	case "powershell":
		rawCmds, err = parsePowerShellHistory()
	}
	if err != nil {
		return fmt.Errorf("parse history: %w", err)
	}
	if len(rawCmds) == 0 {
		progress(" — no commands found\n")
		return nil
	}
	progress(fmt.Sprintf(" — %d commands, ", len(rawCmds)))

	normalized, err := normalizeConcurrent(rawCmds, threads)
	if err != nil {
		return fmt.Errorf("normalize: %w", err)
	}
	progress(fmt.Sprintf("normalized, "))

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
		state := []string{prev1, prev2}
		if cmd != "" {
			g.Record(state, cmd, now)
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

	tmpFile, err := os.CreateTemp("", "hunch-seed-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := json.NewEncoder(tmpFile).Encode(seed); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write seed: %w", err)
	}
	tmpFile.Close()

	req := ipc.Request{
		Op:   "import",
		Next: tmpFile.Name(),
	}
	_, err = sendRequest(req)
	return err
}

func buildRawMappings(rawCmds, normalized []string) map[string]map[string]int {
	m := make(map[string]map[string]int)
	for i, tmpl := range normalized {
		if tmpl == "" || rawCmds[i] == "" {
			continue
		}
		inner, ok := m[tmpl]
		if !ok {
			inner = make(map[string]int)
			m[tmpl] = inner
		}
		inner[rawCmds[i]]++
	}
	return m
}

func sendRawExamples(examples map[string]map[string]int) error {
	type example struct {
		Template string `json:"template"`
		Raw      string `json:"raw"`
		Count    int    `json:"count"`
	}
	var list []example
	for tmpl, inner := range examples {
		for raw, count := range inner {
			list = append(list, example{
				Template: tmpl,
				Raw:      raw,
				Count:    count,
			})
		}
	}

	data, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("marshal raw examples: %w", err)
	}

	req := ipc.Request{
		Op:   "record_raws",
		Next: string(data),
	}
	_, err = sendRequest(req)
	return err
}

func parseZshHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cmds []string
	sc := bufio.NewScanner(f)
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

func parseBashHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cmds []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmds = append(cmds, line)
	}
	return cmds, sc.Err()
}

func parseFishHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cmds []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if after, ok := strings.CutPrefix(line, "- cmd:"); ok {
			cmd := strings.TrimSpace(after)
			if cmd != "" {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds, sc.Err()
}

func parsePowerShellHistory() ([]string, error) {
	psCmd := `Get-History | ForEach-Object { $_.CommandLine }`
	cmd := exec.Command("pwsh", "-NoLogo", "-NoProfile", "-Command", psCmd)
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok || errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("pwsh not found; install PowerShell 7.4+")
		}
		return nil, err
	}

	var cmds []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			cmds = append(cmds, line)
		}
	}
	return cmds, sc.Err()
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
