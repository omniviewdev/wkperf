// Package inspector records a WebKit Inspector target in frontend export format.
package inspector

import (
	"context"
	"io"

	"github.com/omniviewdev/wkperf/internal/inspector"
)

// Options configures Inspector capture and its optional workload.
type Options = inspector.Options

// Capture records a WebKit Inspector target and writes the resulting JSON.
func Capture(ctx context.Context, options Options, out io.Writer) error {
	return inspector.Capture(ctx, options, out)
}
