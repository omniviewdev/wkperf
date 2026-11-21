//go:build unix

package workload

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOwnClosesDescendantOutput(t *testing.T) {
	for _, mode := range []string{"cancel", "parent exits"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			script := "sleep 15 & echo ready"
			if mode == "cancel" {
				script += "; wait"
			}
			cmd := exec.CommandContext(ctx, "sh", "-c", script)
			cmd.Stdout = writer
			cleanup := Own(cmd)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait(); close(done) }()
			defer func() { cleanup(); <-done }()
			writer.Close()
			if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			output := bufio.NewReader(reader)
			if line, err := output.ReadString('\n'); line != "ready\n" || err != nil {
				t.Fatalf("workload readiness: %q (%v)", line, err)
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if (mode == "cancel") != (err != nil) {
					t.Fatalf("workload exit: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("workload did not exit")
			}
			if mode != "cancel" {
				cleanup()
			}
			if _, err := output.ReadByte(); err != io.EOF {
				t.Fatalf("descendant kept output open: %v", err)
			}
		})
	}
}
