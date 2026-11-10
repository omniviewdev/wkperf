package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/schema"
)

type inputError struct {
	File string
	Code string
	Err  error
}

func (e *inputError) Error() string {
	if strings.Contains(e.Err.Error(), e.File) {
		return e.Err.Error()
	}
	return e.File + ": " + e.Err.Error()
}

func (e *inputError) Unwrap() error { return e.Err }

type partialFailure struct{ Errors []schema.Error }

func (e *partialFailure) Error() string {
	messages := make([]string, len(e.Errors))
	for i, failure := range e.Errors {
		messages[i] = failure.Message
	}
	return fmt.Sprintf("%d input(s) failed: %s", len(messages), strings.Join(messages, "; "))
}

func failuresFor(err error) []schema.Error {
	var partial *partialFailure
	if errors.As(err, &partial) {
		return partial.Errors
	}
	failure := schema.Error{Message: err.Error(), Code: "command_error"}
	var input *inputError
	var path *os.PathError
	var exit *ExitError
	switch {
	case errors.As(err, &input):
		failure.File, failure.Code = input.File, input.Code
	case errors.As(err, &path):
		failure.File, failure.Code = path.Path, "input_error"
	case errors.As(err, &exit):
		switch exit.Code {
		case 2:
			failure.Code = "regression"
		case 3:
			failure.Code = "fixture_mismatch"
		case 4:
			failure.Code = "score_below_minimum"
		}
	}
	return []schema.Error{failure}
}

func partialError(failures []schema.Error) error {
	if len(failures) == 0 {
		return nil
	}
	return &partialFailure{Errors: failures}
}

func requestedJSON(root *cobra.Command, args []string) bool {
	format := viper.GetString("format")
	if selected, _, err := root.Find(args); err == nil {
		if flag := selected.Flags().Lookup("format"); flag != nil && flag.DefValue == "json" {
			format = "json"
		}
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if strings.HasPrefix(args[i], "--format=") {
			format = strings.TrimPrefix(args[i], "--format=")
		}
		if args[i] == "--format" && i+1 < len(args) {
			format = args[i+1]
			i++
		}
	}
	return strings.EqualFold(format, "json")
}

// executeCommand owns diagnostics and buffers JSON until the command completes.
// Late gate failures enrich the existing object instead of appending a second
// document; errors before rendering still produce a machine-readable object.
func executeCommand(root *cobra.Command, args []string) error {
	args = expandEmitPrompt(expandVS(args))
	root.SetArgs(args)
	jsonMode := requestedJSON(root, args)
	stdout := root.OutOrStdout()
	var output bytes.Buffer
	if jsonMode {
		root.SetOut(&output)
		defer root.SetOut(stdout)
	}
	err := root.Execute()
	if err != nil {
		fmt.Fprintln(root.ErrOrStderr(), "wkperf:", err)
	}
	if !jsonMode {
		return err
	}
	if err == nil {
		_, writeErr := io.Copy(stdout, &output)
		return writeErr
	}
	failures := failuresFor(err)
	var document map[string]json.RawMessage
	if json.Unmarshal(output.Bytes(), &document) != nil || document == nil {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if writeErr := enc.Encode(schema.ErrorDocument{Schema: schema.ErrorVersion, Errors: failures}); writeErr != nil {
			return writeErr
		}
		return err
	}
	var existing []schema.Error
	_ = json.Unmarshal(document["errors"], &existing)
	for _, failure := range failures {
		found := false
		for _, prior := range existing {
			if prior == failure {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, failure)
		}
	}
	encoded, encodeErr := json.Marshal(existing)
	if encodeErr != nil {
		return encodeErr
	}
	document["errors"] = encoded
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if writeErr := enc.Encode(document); writeErr != nil {
		return writeErr
	}
	return err
}
