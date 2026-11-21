package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/viper"
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

func TestCLIAndMCPWindowReturnSameEvidence(t *testing.T) {
	file := queryFile(t)
	cli := newQueryCmd()
	cli.SetArgs([]string{file, "window", "0", "0.5", "metrics", "overview.duration_s"})
	var cliOut bytes.Buffer
	cli.SetOut(&cliOut)
	if err := cli.Execute(); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(cliOut.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	path, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	mcp := newMCPCmd()
	mcp.SetIn(strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"window","arguments":{"file":` + string(path) + `,"from":0,"to":0.5,"query_operation":"metrics","selector":"overview.duration_s"}}}` + "\n"))
	var mcpOut bytes.Buffer
	mcp.SetOut(&mcpOut)
	if err := mcp.Execute(); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&mcpOut)
	if err := decoder.Decode(new(json.RawMessage)); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Result.IsError || !reflect.DeepEqual(response.Result.StructuredContent, want) {
		t.Fatalf("CLI/MCP window evidence differs:\nCLI: %s\nMCP: %v", cliOut.Bytes(), response.Result.StructuredContent)
	}
}

type mcpReadFunc func([]byte) (int, error)

func (read mcpReadFunc) Read(p []byte) (int, error) { return read(p) }

func TestMCPCommandStreamsWithJSONFormat(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("WKPERF_FORMAT", "json")
	viper.SetEnvPrefix("WKPERF")
	viper.AutomaticEnv()

	var stdout, stderr bytes.Buffer
	root := newRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(io.MultiReader(
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"),
		mcpReadFunc(func([]byte) (int, error) {
			var response struct {
				ID     int `json:"id"`
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.ID != 1 || response.Result.ProtocolVersion != "2025-06-18" {
				t.Fatalf("initialize reply must already be on stdout: %q, %v", stdout.String(), err)
			}
			return 0, io.EOF
		}),
	))
	if err := executeCommand(root, []string{"mcp"}); err != nil || stderr.Len() != 0 {
		t.Fatalf("unexpected MCP diagnostics: %v, %q", err, stderr.String())
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
