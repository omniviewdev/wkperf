package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

type protocolReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func serveProtocolReplies(t *testing.T, input io.Reader) []protocolReply {
	t.Helper()
	var output bytes.Buffer
	if err := (&Server{}).Serve(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var replies []protocolReply
	for {
		var reply protocolReply
		if err := decoder.Decode(&reply); err == io.EOF {
			return replies
		} else if err != nil {
			t.Fatal(err)
		}
		if reply.JSONRPC != "2.0" {
			t.Fatalf("missing JSON-RPC version: %+v", reply)
		}
		replies = append(replies, reply)
	}
}

func TestProtocolOversizedLineRecovery(t *testing.T) {
	input := io.MultiReader(
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"),
		strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"top","arguments":{"file":"unused","module":"`),
		strings.NewReader(strings.Repeat("x", 5<<20)),
		strings.NewReader(`"}}}`+"\n"+`{"jsonrpc":"2.0","id":3,"method":"ping"}`+"\n"),
	)
	replies := serveProtocolReplies(t, input)
	if len(replies) != 3 {
		t.Fatalf("want initialize, oversize error, ping; got %+v", replies)
	}
	if replies[0].Error != nil || len(replies[0].Result) == 0 {
		t.Fatalf("initialize failed: %+v", replies[0])
	}
	if got := replies[1]; string(got.ID) != "null" || got.Error == nil || got.Error.Code != -32600 || !strings.Contains(got.Error.Message, "4 MiB") {
		t.Fatalf("missing oversized request error: %+v", got)
	}
	if got := replies[2]; string(got.ID) != "3" || got.Error != nil || string(got.Result) != "{}" {
		t.Fatalf("ping after oversized line failed: %+v", got)
	}
}

func TestProtocolMessageSizeBoundary(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":"boundary","method":"ping"}`
	for _, size := range []int{MaxMessageBytes - 1, MaxMessageBytes, MaxMessageBytes + 1} {
		for _, ending := range []string{"\n", ""} {
			name := map[int]string{-1: "below", 0: "at", 1: "above"}[size-MaxMessageBytes]
			if ending == "" {
				name += "/eof"
			} else {
				name += "/newline"
			}
			t.Run(name, func(t *testing.T) {
				input := io.MultiReader(strings.NewReader(ping), strings.NewReader(strings.Repeat(" ", size-len(ping))), strings.NewReader(ending))
				replies := serveProtocolReplies(t, input)
				if len(replies) != 1 {
					t.Fatalf("want one reply, got %+v", replies)
				}
				got := replies[0]
				if size > MaxMessageBytes {
					if string(got.ID) != "null" || got.Error == nil || got.Error.Code != -32600 {
						t.Fatalf("accepted oversized message: %+v", got)
					}
				} else if string(got.ID) != `"boundary"` || got.Error != nil || string(got.Result) != "{}" {
					t.Fatalf("rejected bounded message: %+v", got)
				}
			})
		}
	}
}

func TestProtocolInvalidIDsDoNotInitialize(t *testing.T) {
	for _, id := range []string{"null", "1.5", "-1.5", "1.0", "1e0", "1E3", "true", "false", "{}", "[]", strings.Repeat("9", 1025)} {
		t.Run(id[:min(len(id), 24)], func(t *testing.T) {
			input := `{"jsonrpc":"2.0","id":` + id + `,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
				`{"jsonrpc":"2.0","id":"valid","method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
			replies := serveProtocolReplies(t, strings.NewReader(input))
			if len(replies) != 2 {
				t.Fatalf("want invalid request and initialize replies: %+v", replies)
			}
			if got := replies[0]; string(got.ID) != "null" || got.Error == nil || got.Error.Code != -32600 {
				t.Fatalf("invalid ID accepted: %+v", got)
			}
			if got := replies[1]; string(got.ID) != `"valid"` || got.Error != nil || len(got.Result) == 0 {
				t.Fatalf("invalid ID dispatched initialize: %+v", got)
			}
		})
	}
}

func TestProtocolPreservesIDsAndNotificationSilence(t *testing.T) {
	ids := []string{`"string"`, `""`, `"escaped\u0030"`, "0", "-0", "-42", "9007199254740993", "18446744073709551616", strings.Repeat("9", 1024)}
	var input strings.Builder
	input.WriteString("\r\n" + `{"jsonrpc":"2.0","method":"ping"}` + "\r\n")
	for _, id := range ids {
		input.WriteString(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}` + "\n")
	}
	replies := serveProtocolReplies(t, strings.NewReader(input.String()))
	if len(replies) != len(ids) {
		t.Fatalf("notification or ID handling changed response count: %+v", replies)
	}
	for i, id := range ids {
		if got := replies[i]; string(got.ID) != id || got.Error != nil || string(got.Result) != "{}" {
			t.Fatalf("ID %s did not round-trip exactly: %+v", id, got)
		}
	}
}

func TestProtocolNonRegularRecordingKeepsSessionAlive(t *testing.T) {
	for _, file := range []string{t.TempDir(), os.DevNull} {
		t.Run(file, func(t *testing.T) {
			arguments, err := json.Marshal(map[string]string{"file": file})
			if err != nil {
				t.Fatal(err)
			}
			input := strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"top","arguments":` + string(arguments) + `}}`,
				`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
			}, "\n") + "\n"
			replies := serveProtocolReplies(t, strings.NewReader(input))
			if len(replies) != 3 {
				t.Fatalf("non-regular input stopped session: %+v", replies)
			}
			var result callResult
			if err := json.Unmarshal(replies[1].Result, &result); err != nil {
				t.Fatal(err)
			}
			if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "not a regular file") {
				t.Fatalf("missing non-regular input tool error: %+v", result)
			}
			if got := replies[2]; string(got.ID) != "3" || got.Error != nil || string(got.Result) != "{}" {
				t.Fatalf("ping after non-regular input failed: %+v", got)
			}
		})
	}
}
