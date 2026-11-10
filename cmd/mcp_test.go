package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPCommandProtocolOnly(t *testing.T) {
	command := newMCPCmd()
	command.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":"ping","method":"ping"}` + "\n"))
	var out, stderr bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&stderr)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(out.Bytes(), &message); err != nil {
		t.Fatalf("not protocol-only stdout: %s", out.String())
	}
	if message["id"] != "ping" || stderr.Len() != 0 {
		t.Fatalf("unexpected streams: %s / %s", out.String(), stderr.String())
	}
}
