package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/schema"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func addSourceMapFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("sourcemap", nil, "source map path (repeatable); adjacent maps are also discovered before analysis")
}

func bindSourceMapFlags(cmd *cobra.Command) error { return bindFlags(cmd, "sourcemap") }

func addModuleGroupingFlag(cmd *cobra.Command) {
	cmd.Flags().String("module-grouping", "file", "module rollup: file|parent-directory (npm packages remain package-scoped)")
}

func moduleGrouping() (string, error) {
	grouping := formatName(viper.GetString("module-grouping"))
	if grouping != "file" && grouping != "parent-directory" {
		return "", fmt.Errorf("unknown --module-grouping %q (want file|parent-directory)", grouping)
	}
	return grouping, nil
}

func loadRecording(cmd *cobra.Command, path string) (*model.Recording, sourcemap.Diagnostics, error) {
	var diagnostics sourcemap.Diagnostics
	rec, err := model.Load(path)
	if err != nil {
		return nil, diagnostics, &inputError{File: path, Code: "load_error", Err: err}
	}
	diagnostics, err = sourcemap.Resolve(rec, viper.GetStringSlice("sourcemap"), path)
	if err != nil {
		return nil, diagnostics, &inputError{File: path, Code: "source_map_error", Err: err}
	}
	for _, warning := range diagnostics.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", path, warning)
	}
	return rec, diagnostics, nil
}

func expandPathsContinuing(paths []string) ([]string, []schema.Error) {
	var files []string
	var failures []schema.Error
	for _, path := range paths {
		expanded, err := expandPaths([]string{path})
		if err != nil {
			failures = append(failures, failuresFor(&inputError{File: path, Code: "input_error", Err: err})...)
			continue
		}
		files = append(files, expanded...)
	}
	return files, failures
}

func discoverScenesContinuing(paths []string, groupByPrefix bool) ([]sceneFiles, []schema.Error) {
	var scenes []sceneFiles
	byName := map[string]int{}
	var failures []schema.Error
	for _, path := range paths {
		found, err := discoverScenes([]string{path}, groupByPrefix)
		if err != nil {
			failures = append(failures, failuresFor(&inputError{File: path, Code: "input_error", Err: err})...)
			continue
		}
		for _, scene := range found {
			if index, exists := byName[scene.Name]; exists {
				scenes[index].Files = append(scenes[index].Files, scene.Files...)
			} else {
				byName[scene.Name] = len(scenes)
				scenes = append(scenes, scene)
			}
		}
	}
	return scenes, failures
}

func hasSourceMapMetadata(diagnostics sourcemap.Diagnostics) bool {
	return diagnostics.Minified || diagnostics.MappedFrames != 0 || diagnostics.UnmappedFrames != 0 || len(diagnostics.MissingMaps) != 0 || len(diagnostics.Warnings) != 0
}

func sourceMapMetadata(group []*loaded) []schema.SourceMapMetadata {
	var metadata []schema.SourceMapMetadata
	for _, input := range group {
		if hasSourceMapMetadata(input.SourceMap) {
			metadata = append(metadata, schema.SourceMapMetadata{File: input.Sum.Source, Diagnostics: input.SourceMap})
		}
	}
	return metadata
}

func combineFailures(failures []schema.Error, err error) error {
	if err != nil {
		failures = append(failures, failuresFor(err)...)
	}
	return partialError(failures)
}

func validateMarkdownOptions(format string, maxBytes int, ai bool) error {
	if format != "md" && format != "all" {
		return nil
	}
	if maxBytes < 0 {
		return fmt.Errorf("--max-bytes must be nonnegative")
	}
	if maxBytes > 0 && maxBytes < 64 {
		return fmt.Errorf("--max-bytes must be at least 64 to retain the truncation marker")
	}
	if maxBytes > 0 && ai {
		return fmt.Errorf("--max-bytes cannot be combined with --ai; omit --ai for a deterministic bounded Markdown report")
	}
	return nil
}

func formatName(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
