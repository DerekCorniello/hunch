package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DerekCorniello/hunch/daemon"
	"github.com/DerekCorniello/hunch/ipc"
)

func cmdClient(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch client <op>\n\nops: record, predict, reset, export, normalize, stats")
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
	default:
		return fmt.Errorf("unknown client op: %q\n\nops: record, predict, reset, export, normalize, stats", args[0])
	}
}

func dialDaemon() (net.Conn, string, error) {
	opts := daemon.LoadConfig()
	if opts.Socket == "" {
		return nil, "", fmt.Errorf("could not determine socket path; set HUNCH_SOCKET")
	}
	conn, err := net.DialTimeout("unix", opts.Socket, 2*time.Second)
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
	outcome := fs.String("outcome", "", "success or failure")
	cwd := fs.String("cwd", "", "working directory")
	at := fs.String("at", "", "RFC 3339 timestamp")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *next == "" {
		return errors.New("--next is required")
	}

	req := ipc.Request{
		Op:    "record",
		State: splitState(*stateStr),
		Next:  *next,
	}
	if *outcome != "" {
		req.Outcome = *outcome
	}
	if *cwd != "" {
		req.CWD = *cwd
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
	templateOnly := fs.Bool("template", false, "output only the first template string (no JSON)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	req := ipc.Request{
		Op:    "predict",
		State: splitState(*stateStr),
		Limit: *limit,
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
		if *templateOnly {
			return nil
		}
		fmt.Println("[]")
		return nil
	}

	if *templateOnly {
		fmt.Println(resp.Suggestions[0].Template)
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
