package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/DerekCorniello/hunch/ipc"
)

func TestServeQueryNoDaemon(t *testing.T) {
	got := serveQuery(filepath.Join(t.TempDir(), "nope.sock"), ipc.ServeRequest{Prefix: "x"})
	if len(got) != 0 {
		t.Errorf("serveQuery with no daemon = %v, want empty", got)
	}
}

// encodeRequests renders serve requests as the JSON-lines stream the loop reads.
func encodeRequests(t *testing.T, reqs ...ipc.ServeRequest) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range reqs {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	return &buf
}

// decodeResponses parses the JSON-lines stream the loop writes.
func decodeResponses(t *testing.T, out *bytes.Buffer) []ipc.ServeResponse {
	t.Helper()
	var resps []ipc.ServeResponse
	dec := json.NewDecoder(out)
	for dec.More() {
		var r ipc.ServeResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestServeRoundtrip(t *testing.T) {
	socket, stop := startTestDaemon(t)
	defer stop()

	if err := Run([]string{"client", "record", "--state", "", "--next", "git status"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Two queries on one persistent loop: a matching prefix and a miss.
	in := encodeRequests(t,
		ipc.ServeRequest{Prefix: "git st"},
		ipc.ServeRequest{Prefix: "zzz"},
	)
	var out bytes.Buffer
	if err := runServe(socket, in, &out); err != nil {
		t.Fatalf("runServe: %v", err)
	}

	resps := decodeResponses(t, &out)
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: %q", len(resps), out.String())
	}
	// Each response echoes its query prefix so the integration can drop stale
	// responses, followed by the ranked raw suggestions.
	if resps[0].Prefix != "git st" || len(resps[0].Raws) != 1 || resps[0].Raws[0] != "git status" {
		t.Errorf("response 0 = %+v, want {git st [git status]}", resps[0])
	}
	if resps[1].Prefix != "zzz" || len(resps[1].Raws) != 0 {
		t.Errorf("response 1 = %+v, want {zzz []}", resps[1])
	}
}

func TestServeEmptyPrefixUsesTopRaw(t *testing.T) {
	socket, stop := startTestDaemon(t)
	defer stop()

	if err := Run([]string{"client", "record", "--state", ",cd PATH", "--next", "ls -la"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	in := encodeRequests(t, ipc.ServeRequest{Prefix: "", State: []string{"", "cd PATH"}})
	var out bytes.Buffer
	if err := runServe(socket, in, &out); err != nil {
		t.Fatalf("runServe: %v", err)
	}

	resps := decodeResponses(t, &out)
	if len(resps) != 1 || len(resps[0].Raws) != 1 || resps[0].Raws[0] != "ls -la" {
		t.Errorf("responses = %+v, want [{<empty> [ls -la]}]", resps)
	}
}

// TestServeSkipsMalformedLine verifies a junk line doesn't break the loop.
func TestServeSkipsMalformedLine(t *testing.T) {
	socket, stop := startTestDaemon(t)
	defer stop()

	var in bytes.Buffer
	in.WriteString("this is not json\n")
	enc := json.NewEncoder(&in)
	if err := enc.Encode(ipc.ServeRequest{Prefix: "x"}); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out bytes.Buffer
	if err := runServe(socket, &in, &out); err != nil {
		t.Fatalf("runServe: %v", err)
	}
	resps := decodeResponses(t, &out)
	if len(resps) != 1 || resps[0].Prefix != "x" {
		t.Errorf("responses = %+v, want one response for the valid line", resps)
	}
}
