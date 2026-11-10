package query

import (
	"context"
	"fmt"
	"math"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func defaultLoad(ctx context.Context, file string, opts SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
	if err := ctx.Err(); err != nil {
		return nil, sourcemap.Diagnostics{}, err
	}
	rec, err := model.Load(file)
	if err != nil {
		return nil, sourcemap.Diagnostics{}, diagnostic.Wrap(diagnostic.LoadError, file, err)
	}
	diagnostics, err := sourcemap.Resolve(rec, opts.Sourcemaps, file)
	if err != nil {
		return nil, diagnostics, diagnostic.Wrap(diagnostic.SourceMapError, file, err)
	}
	return rec, diagnostics, nil
}

func (e *Engine) load(ctx context.Context, file string, req Request) (*model.Recording, *flame.Tree, Source, error) {
	loader := e.Loader
	if loader == nil {
		loader = defaultLoad
	}
	rec, diagnostics, err := loader(ctx, file, SourceOptions{Sourcemaps: req.Sourcemaps, Ownership: req.ownership()})
	if err != nil {
		return nil, nil, Source{}, diagnostic.Wrap(diagnostic.LoadError, file, err)
	}
	if rec == nil {
		return nil, nil, Source{}, diagnostic.Wrap(diagnostic.LoadError, file, fmt.Errorf("loader returned no recording"))
	}
	from, to := math.Max(req.From, req.TrimStart), rec.EndTime-rec.StartTime-req.TrimEnd
	if req.To > 0 {
		to = math.Min(to, req.To)
	}
	if math.IsNaN(to) || math.IsInf(to, 0) || to < from || (to == from && (from > 0 || req.To > 0 || req.TrimEnd > 0)) {
		return nil, nil, Source{}, diagnostic.Wrap(diagnostic.WindowError, file, fmt.Errorf("selected window is empty"))
	}
	tree := flame.Build(rec, flame.Options{Ownership: req.ownership()})
	if !profile.CoversRecording(rec, from, to) {
		tree = tree.Window(from, to)
	}
	return rec, tree, Source{
		File: file, From: from, To: to,
		UntimedSamples: tree.UntimedTotal.Count, UntimedMs: clean(tree.UntimedTotal.Ms),
		UntimedExcluded: tree.UntimedTotal.Count > 0 && !tree.UntimedIncluded,
		UntimedSpans:    tree.UntimedSpans, SourceMaps: diagnostics,
	}, nil
}
