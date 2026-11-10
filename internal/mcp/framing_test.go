package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

type framingPadding byte

func (padding framingPadding) Read(data []byte) (int, error) {
	for i := range data {
		data[i] = byte(padding)
	}
	return len(data), nil
}

func TestMessageReaderBoundedStorageAndReuse(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	input := io.MultiReader(
		io.LimitReader(framingPadding('x'), 8*MaxMessageBytes),
		strings.NewReader("\n"+ping),
		io.LimitReader(framingPadding(' '), int64(MaxMessageBytes-len(ping))),
		strings.NewReader("\n"+ping+"\n"),
	)
	reader := messageReader{reader: bufio.NewReader(input)}
	if line, oversized, err := reader.read(); err != nil || !oversized || line != nil {
		t.Fatalf("oversized line was not discarded: bytes=%d oversized=%v err=%v", len(line), oversized, err)
	}
	if cap(reader.buffer) == 0 || cap(reader.buffer) > MaxMessageBytes {
		t.Fatalf("unbounded message storage: capacity=%d", cap(reader.buffer))
	}
	storage := &reader.buffer[0]
	if line, oversized, err := reader.read(); err != nil || oversized || len(line) != MaxMessageBytes || !bytes.Equal(bytes.TrimSpace(line), []byte(ping)) {
		t.Fatalf("message after discard changed: bytes=%d oversized=%v err=%v", len(line), oversized, err)
	}
	if &reader.buffer[0] != storage {
		t.Fatal("large message storage was not reused")
	}
	if line, oversized, err := reader.read(); err != nil || oversized || string(line) != ping {
		t.Fatalf("short message after large message changed: %q oversized=%v err=%v", line, oversized, err)
	}
	if _, _, err := reader.read(); err != io.EOF {
		t.Fatalf("missing end of stream: %v", err)
	}
}

func TestProtocolReadErrorDuringDiscard(t *testing.T) {
	want := errors.New("broken input stream")
	input := io.MultiReader(io.LimitReader(framingPadding('x'), MaxMessageBytes+1), iotest.ErrReader(want))
	var output bytes.Buffer
	if err := (&Server{}).Serve(context.Background(), input, &output); !errors.Is(err, want) {
		t.Fatalf("lost read error while discarding: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("incomplete line produced a response: %s", output.String())
	}
}

func TestValidIDBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		want bool
	}{
		{"notification", "", true},
		{"empty string", `""`, true},
		{"maximum string", `"` + strings.Repeat("x", 1022) + `"`, true},
		{"oversized string", `"` + strings.Repeat("x", 1023) + `"`, false},
		{"maximum negative integer", "-" + strings.Repeat("9", 1023), true},
		{"oversized negative integer", "-" + strings.Repeat("9", 1024), false},
		{"zero", "0", true},
		{"negative zero", "-0", true},
		{"minus only", "-", false},
		{"plus sign", "+1", false},
		{"leading zero", "01", false},
		{"negative leading zero", "-01", false},
		{"fraction", "0.5", false},
		{"integer decimal", "2.0", false},
		{"exponent", "2e0", false},
		{"null", "null", false},
		{"boolean", "true", false},
		{"object", "{}", false},
		{"array", "[]", false},
		{"unterminated string", `"open`, false},
		{"adjacent strings", `"a""b"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validID([]byte(test.id)); got != test.want {
				t.Fatalf("validID(%q) = %v, want %v", test.id, got, test.want)
			}
		})
	}
}
