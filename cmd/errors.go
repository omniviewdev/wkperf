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

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/schema"
)

type reportWrittenKey struct{}
type partialFailure struct{ Errors []schema.Error }

func (e *partialFailure) Error() string {
	messages := make([]string, len(e.Errors))
	for i, failure := range e.Errors {
		messages[i] = failure.Message
		if failure.File != "" {
			messages[i] = failure.File + ": " + failure.Message
		}
	}
	return fmt.Sprintf("%d input(s) failed: %s", len(messages), strings.Join(messages, "; "))
}

func failuresFor(err error) []schema.Error {
	var partial *partialFailure
	if errors.As(err, &partial) {
		return partial.Errors
	}
	failure := schema.Error{Message: err.Error(), Code: diagnostic.UsageError}
	var input *diagnostic.Error
	var path *os.PathError
	var exit *ExitError
	switch {
	case errors.As(err, &input):
		failure.File, failure.Code, failure.Message = input.File, input.Code, input.Message()
		failure.Candidates = input.Candidates
	case errors.As(err, &path):
		failure.File, failure.Code = path.Path, diagnostic.InputError
		failure.Message = diagnostic.Wrap(diagnostic.InputError, path.Path, path).Message()
	case errors.As(err, &exit):
		switch exit.Code {
		case 2:
			failure.Code = diagnostic.Regression
		case 3:
			failure.Code = diagnostic.FixtureMismatch
		case 4:
			failure.Code = diagnostic.ScoreBelowMinimum
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
	if output.Len() == 0 && root.Context().Value(reportWrittenKey{}) == true {
		return err
	}
	failures := failuresFor(err)
	var envelope struct {
		Errors []schema.Error `json:"errors"`
	}
	data := bytes.TrimSpace(output.Bytes())
	if len(data) < 2 || data[0] != '{' || json.Unmarshal(data, &envelope) != nil {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if writeErr := enc.Encode(schema.ErrorDocument{Schema: schema.ErrorVersion, Errors: failures}); writeErr != nil {
			return writeErr
		}
		return err
	}
	// Commands attach partial-input and gate failures to their typed envelope
	// before rendering. Keep the original bytes and field order intact.
	if len(envelope.Errors) != 0 {
		_, writeErr := io.Copy(stdout, &output)
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	encoded, encodeErr := json.Marshal(failures)
	if encodeErr != nil {
		return encodeErr
	}
	// A late transport failure can occur after an otherwise valid document.
	// Append its typed errors without decoding/re-encoding unrelated fields.
	data = append(data[:len(data)-1], []byte(",\"errors\":")...)
	data = append(data, encoded...)
	data = append(data, '}', '\n')
	if _, writeErr := stdout.Write(data); writeErr != nil {
		return writeErr
	}
	return err
}
