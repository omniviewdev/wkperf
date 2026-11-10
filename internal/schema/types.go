// Package schema describes the JSON documents emitted by wkperf.
package schema

import (
	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

const (
	InsightsVersion   = "wkperf-insights/v2"
	AuditVersion      = "wkperf-audit/v1"
	ComparisonVersion = "wkperf/v1"
	SummaryVersion    = "wkperf-summary/v1"
	ErrorVersion      = "wkperf-error/v1"
)

// Error is one failed input or command. File is empty for command-level failures.
type Error struct {
	File    string `json:"file"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// SourceMapMetadata records mapping coverage without changing generated identities.
type SourceMapMetadata struct {
	File        string                `json:"file"`
	Diagnostics sourcemap.Diagnostics `json:"diagnostics"`
}

// Analysis pairs a complete profile with its optional scorecard and map coverage.
type Analysis struct {
	*insights.Report
	Scorecard *scorecard.Scorecard   `json:"scorecard,omitempty"`
	SourceMap *sourcemap.Diagnostics `json:"sourceMap,omitempty"`
}

type InsightsDocument struct {
	Schema  string     `json:"schema"`
	Reports []Analysis `json:"reports"`
	Errors  []Error    `json:"errors,omitempty"`
	AI      string     `json:"ai,omitempty"`
}

type AuditDocument struct {
	Schema     string              `json:"schema"`
	Report     *audit.Report       `json:"report"`
	SourceMaps []SourceMapMetadata `json:"sourceMaps,omitempty"`
	Errors     []Error             `json:"errors,omitempty"`
	AI         string              `json:"ai,omitempty"`
}

type ComparisonDocument struct {
	Schema string `json:"schema"`
	*compare.Result
	SourceMaps []SourceMapMetadata `json:"sourceMaps,omitempty"`
	Errors     []Error             `json:"errors,omitempty"`
}

type SummaryDocument struct {
	Schema     string              `json:"schema"`
	Sources    []string            `json:"sources"`
	Runs       int                 `json:"runs"`
	Metrics    []metrics.Metric    `json:"metrics"`
	SourceMaps []SourceMapMetadata `json:"sourceMaps,omitempty"`
	Errors     []Error             `json:"errors,omitempty"`
}

type ErrorDocument struct {
	Schema string  `json:"schema"`
	Errors []Error `json:"errors"`
}
