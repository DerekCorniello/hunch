package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
	"github.com/DerekCorniello/hunch/ipc"
)

func cmdClient(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch client <op>\n\nops: record, predict, reset, export, normalize, stats, config, import, serve")
	}

	switch args[0] {
	case "record":
		return cmdClientRecord(args[1:])
	case "predict":
		return cmdClientPredict(args[1:])
	case "reset":
		return cmdClientReset()
	case "export":
		return cmdClientExport()
	case "normalize":
		return cmdClientNormalize(args[1:])
	case "stats":
		return cmdClientStats()
	case "config":
		return cmdClientConfig()
	case "import":
		return cmdClientImport(args[1:])
	case "serve":
		return cmdClientServe(args[1:])
	default:
		return fmt.Errorf("unknown client op: %q\n\nops: record, predict, reset, export, normalize, stats, config, import, serve", args[0])
	}
}

func dialDaemon() (net.Conn, string, error) {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return nil, "", fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}
	conn, err := daemon.Dial(opts.Socket, 2*time.Second)
	if err != nil {
		return nil, "", fmt.Errorf("connect to daemon at %s: %w (is the daemon running?)", opts.Socket, err)
	}
	return conn, opts.Socket, nil
}

// sendRequest sends an IPC request and returns the raw JSON response.
func sendRequest(req ipc.Request) (json.RawMessage, error) {
	conn, _, err := dialDaemon()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(conn).Decode(&raw); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var errResp ipc.ErrorResponse
	if json.Unmarshal(raw, &errResp) == nil && errResp.Error != "" {
		return nil, fmt.Errorf("daemon error: %s", errResp.Error)
	}

	return raw, nil
}

// unmarshalResponse sends a request and unmarshals the response into v.
func unmarshalResponse(req ipc.Request, v interface{}) error {
	raw, err := sendRequest(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func cmdClientRecord(args []string) error {
	fs := flag.NewFlagSet("hunch client record", flag.ContinueOnError)
	stateStr := fs.String("state", "", "comma-separated list of state templates")
	next := fs.String("next", "", "the template that followed")
	at := fs.String("at", "", "RFC 3339 timestamp")
	cwd := fs.String("cwd", "", "working directory the command ran in")
	outcome := fs.String("outcome", "", "outcome of the command: success, failure, or empty")
	priorOutcome := fs.String("prior-outcome", "", "outcome of the preceding command")
	suggested := fs.String("suggested", "", "raw suggestion hunch had shown (for acceptance detection)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *next == "" {
		return errors.New("--next is required")
	}

	req := ipc.Request{
		Op:           "record",
		State:        splitState(*stateStr),
		Next:         *next,
		CWD:          *cwd,
		Outcome:      *outcome,
		PriorOutcome: *priorOutcome,
		Suggested:    *suggested,
	}
	if *at != "" {
		req.At = *at
	}

	raw, err := sendRequest(req)
	if err != nil {
		return err
	}
	var okResp ipc.OKResponse
	if json.Unmarshal(raw, &okResp) != nil || !okResp.OK {
		return fmt.Errorf("daemon returned unexpected response")
	}
	fmt.Println("ok")
	return nil
}

func cmdClientPredict(args []string) error {
	fs := flag.NewFlagSet("hunch client predict", flag.ContinueOnError)
	stateStr := fs.String("state", "", "comma-separated list of state templates")
	prefix := fs.String("prefix", "", "prefix filter for suggestions")
	limit := fs.Int("limit", 3, "max suggestions to return")
	at := fs.String("at", "", "RFC 3339 timestamp")
	cwd := fs.String("cwd", "", "current working directory (boosts same-directory suggestions)")
	priorOutcome := fs.String("prior-outcome", "", "outcome of the most recent command")
	templateOnly := fs.Bool("template", false, "output only the first template string (no JSON)")
	rawOnly := fs.Bool("raw", false, "output only the first raw command string (no JSON)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	req := ipc.Request{
		Op:           "predict",
		State:        splitState(*stateStr),
		Limit:        *limit,
		CWD:          *cwd,
		PriorOutcome: *priorOutcome,
	}
	if *prefix != "" {
		req.Prefix = *prefix
	}
	if *at != "" {
		req.At = *at
	}

	var resp ipc.SuggestionsResponse
	if err := unmarshalResponse(req, &resp); err != nil {
		return err
	}

	if len(resp.Suggestions) == 0 {
		if *templateOnly || *rawOnly {
			return nil
		}
		fmt.Println("[]")
		return nil
	}

	if *templateOnly {
		fmt.Println(resp.Suggestions[0].Template)
		return nil
	}
	if *rawOnly {
		if cmd := ipc.DisplayCommand(resp.Suggestions[0].Raw, resp.Suggestions[0].Template); cmd != "" {
			fmt.Println(cmd)
		}
		return nil
	}

	b, err := json.MarshalIndent(resp.Suggestions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suggestions: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func cmdClientReset() error {
	req := ipc.Request{Op: "reset"}
	raw, err := sendRequest(req)
	if err != nil {
		return err
	}
	var okResp ipc.OKResponse
	if json.Unmarshal(raw, &okResp) != nil || !okResp.OK {
		return fmt.Errorf("daemon returned unexpected response")
	}
	fmt.Println("ok")
	return nil
}

func cmdClientExport() error {
	req := ipc.Request{Op: "export"}

	var resp ipc.TransitionsResponse
	if err := unmarshalResponse(req, &resp); err != nil {
		return err
	}

	b, err := json.MarshalIndent(resp.Transitions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transitions: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func cmdClientNormalize(args []string) error {
	fs := flag.NewFlagSet("hunch client normalize", flag.ContinueOnError)
	cmd := fs.String("cmd", "", "raw command to normalize")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *cmd == "" {
		return errors.New("--cmd is required")
	}

	req := ipc.Request{
		Op:   "normalize",
		Next: *cmd,
	}

	var resp ipc.NormalizeResponse
	if err := unmarshalResponse(req, &resp); err != nil {
		return err
	}
	fmt.Println(resp.Template)
	return nil
}

func cmdClientStats() error {
	req := ipc.Request{Op: "stats"}

	var resp ipc.StatsResponse
	if err := unmarshalResponse(req, &resp); err != nil {
		return err
	}

	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func splitState(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func cmdClientConfig() error {
	req := ipc.Request{Op: "config"}

	var resp ipc.ConfigResponse
	if err := unmarshalResponse(req, &resp); err != nil {
		return err
	}

	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func cmdClientImport(args []string) error {
	fs := flag.NewFlagSet("hunch client import", flag.ContinueOnError)
	path := fs.String("path", "", "path to seed JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *path == "" {
		return errors.New("--path is required")
	}

	req := ipc.Request{
		Op:   "import",
		Next: *path,
	}
	if _, err := sendRequest(req); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

// cmdClientServe runs a persistent prediction loop. It reads one JSON request
// object per line from stdin and writes one JSON response object per line to
// stdout, keeping daemon configuration in memory so shell integrations avoid a
// process spawn per keystroke. See ipc.ServeRequest / ipc.ServeResponse.
//
// A missing daemon yields an empty suggestion rather than an error, so the
// loop survives daemon restarts; the next query reconnects.
func cmdClientServe(args []string) error {
	fs := flag.NewFlagSet("hunch client serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return errors.New("could not determine socket path; set HUNCH_SOCKET")
	}
	return runServe(opts.Socket, os.Stdin, os.Stdout)
}

func runServe(socket string, r io.Reader, w io.Writer) error {
	in := bufio.NewScanner(r)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := bufio.NewWriter(w)
	enc := json.NewEncoder(out)

	for in.Scan() {
		var req ipc.ServeRequest
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			// Skip a malformed line rather than breaking the loop; the next
			// well-formed request still gets served.
			continue
		}
		raws := serveQuery(socket, req)
		if err := enc.Encode(ipc.ServeResponse{Prefix: req.Prefix, Raws: raws}); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
	return in.Err()
}

// serveSuggestions is how many ranked suggestions serve requests, so the
// integration can offer a few to cycle through without re-querying.
const serveSuggestions = 5

// serveQuery dials the daemon and returns up to serveSuggestions ranked raw
// commands for the request (each falling back to its template). It returns nil
// on any error so the serve loop never breaks.
func serveQuery(socket string, req ipc.ServeRequest) []string {
	conn, err := daemon.Dial(socket, time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	pr := ipc.Request{
		Op:           "predict",
		State:        req.State,
		Prefix:       req.Prefix,
		CWD:          req.CWD,
		PriorOutcome: req.PriorOutcome,
		Limit:        serveSuggestions,
	}
	if err := json.NewEncoder(conn).Encode(pr); err != nil {
		return nil
	}
	var resp ipc.SuggestionsResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil
	}
	raws := make([]string, 0, len(resp.Suggestions))
	for _, s := range resp.Suggestions {
		if cmd := ipc.DisplayCommand(s.Raw, s.Template); cmd != "" {
			raws = append(raws, cmd)
		}
	}
	return raws
}
