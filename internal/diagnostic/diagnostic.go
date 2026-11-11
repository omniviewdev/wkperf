// Package diagnostic defines stable failure codes shared by command and query transports.
package diagnostic

import (
	"errors"
	"os"
)

// Code identifies the action a caller must take after a failed command or input.
type Code string

const (
	InputError        Code = "input_error"
	LoadError         Code = "load_error"
	SourceMapError    Code = "source_map_error"
	WindowError       Code = "window_error"
	SelectorError     Code = "selector_error"
	AmbiguousSelector Code = "ambiguous_selector"
	UnknownKey        Code = "unknown_key"
	UsageError        Code = "usage_error"
	Regression        Code = "regression"
	FixtureMismatch   Code = "fixture_mismatch"
	ScoreBelowMinimum Code = "score_below_minimum"
)

// Error attaches a stable code and optional source file to an underlying failure.
// Err must describe the failure without repeating File. Candidates are ordered
// suggestions for an unknown metric key, not an alternative error vocabulary.
type Error struct {
	File       string
	Code       Code
	Err        error
	Candidates []string
}

func (e *Error) Error() string {
	message := e.Message()
	if e.File != "" {
		return e.File + ": " + message
	}
	return message
}

func (e *Error) Unwrap() error { return e.Err }

// Message returns the failure reason without its separately recorded file path.
func (e *Error) Message() string {
	if e.Err == nil {
		return string(e.Code)
	}
	var nested *Error
	if errors.As(e.Err, &nested) {
		return nested.Message()
	}
	var path *os.PathError
	if errors.As(e.Err, &path) && path.Path == e.File {
		return path.Op + ": " + path.Err.Error()
	}
	return e.Err.Error()
}

// Wrap preserves an already typed failure and fills in missing source context.
func Wrap(code Code, file string, err error) *Error {
	var typed *Error
	if errors.As(err, &typed) {
		copy := *typed
		if copy.File == "" {
			copy.File = file
		}
		return &copy
	}
	return &Error{File: file, Code: code, Err: err}
}
