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
)

func cmdClient(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hunch client <op>\n\nops: record, predict, reset, export")
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
	default:
		return fmt.Errorf("unknown client op: %q\n\nops: record, predict, reset, export", args[0])
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

func sendRequest(req map[string]interface{}) (map[string]interface{}, error) {
	conn, _, err := dialDaemon()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if errMsg, ok := resp["error"]; ok {
		return nil, fmt.Errorf("daemon error: %v", errMsg)
	}

	return resp, nil
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

	req := map[string]interface{}{
		"op":    "record",
		"state": splitState(*stateStr),
		"next":  *next,
	}
	if *outcome != "" {
		req["outcome"] = *outcome
	}
	if *cwd != "" {
		req["cwd"] = *cwd
	}
	if *at != "" {
		req["at"] = *at
	}

	if _, err := sendRequest(req); err != nil {
		return err
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	req := map[string]interface{}{
		"op":    "predict",
		"state": splitState(*stateStr),
		"limit": *limit,
	}
	if *prefix != "" {
		req["prefix"] = *prefix
	}
	if *at != "" {
		req["at"] = *at
	}

	resp, err := sendRequest(req)
	if err != nil {
		return err
	}

	suggestions, ok := resp["suggestions"].([]interface{})
	if !ok {
		// If no suggestions, just print empty array.
		fmt.Println("[]")
		return nil
	}

	b, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal suggestions: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func cmdClientReset() error {
	req := map[string]interface{}{
		"op": "reset",
	}
	if _, err := sendRequest(req); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

func cmdClientExport() error {
	req := map[string]interface{}{
		"op": "export",
	}
	resp, err := sendRequest(req)
	if err != nil {
		return err
	}

	transitions, ok := resp["transitions"].([]interface{})
	if !ok {
		fmt.Println("[]")
		return nil
	}

	b, err := json.MarshalIndent(transitions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transitions: %w", err)
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
