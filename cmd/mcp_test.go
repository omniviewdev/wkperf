package cmd

import (
	"bytes"
	"encoding/json"
	"runtime/debug"
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

func TestMCPInitializeResolvedVersion(t *testing.T) {
	savedVersion, savedCommit, savedDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = savedVersion, savedCommit, savedDate })
	for _, override := range []string{"", "v4.5.6"} {
		t.Run("override="+override, func(t *testing.T) {
			Version, Commit, Date = override, "abc123", "2026-11-10"
			want := override
			if want == "" {
				want = "dev"
				if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
					want = info.Main.Version
				}
			}
			command := newMCPCmd()
			command.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"))
			var output bytes.Buffer
			command.SetOut(&output)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Result struct {
					ServerInfo struct {
						Name    string `json:"name"`
						Version string `json:"version"`
					} `json:"serverInfo"`
				} `json:"result"`
			}
			if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if got := reply.Result.ServerInfo; got.Name != "wkperf" || got.Version != want {
				t.Fatalf("initialize version = %+v, want wkperf %s", got, want)
			}
			output.Reset()
			command = newVersionCmd()
			command.SetOut(&output)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != "wkperf "+want+" (abc123) built 2026-11-10\n" {
				t.Fatalf("version command = %q", got)
			}
		})
	}
}
