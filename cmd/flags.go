package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/ai"
	"github.com/omniviewdev/wkperf/internal/analyze"
)

var windowKeys = []string{"trim-start", "trim-end", "from", "to"}

var aiKeys = []string{"ai", "app-name", "app-context", "ai-notes", "emit-prompt"}

// aiViperKeys maps AI flags to their viper config keys (ai.* in config files).
var aiViperKeys = map[string]string{
	"ai-provider":   "ai.provider",
	"ai-model":      "ai.model",
	"ai-base-url":   "ai.base_url",
	"ai-max-tokens": "ai.max_tokens",
	"ai-timeout":    "ai.timeout",
}

// bindFlags binds the named flags of cmd to viper keys of the same name.
func bindFlags(cmd *cobra.Command, keys ...string) error {
	for _, k := range keys {
		fl := cmd.Flags().Lookup(k)
		if fl == nil {
			continue
		}
		if err := viper.BindPFlag(k, fl); err != nil {
			return err
		}
	}
	return nil
}

func bindAIFlags(cmd *cobra.Command) error {
	if err := bindFlags(cmd, aiKeys...); err != nil {
		return err
	}
	for flag, key := range aiViperKeys {
		if fl := cmd.Flags().Lookup(flag); fl != nil {
			if err := viper.BindPFlag(key, fl); err != nil {
				return err
			}
		}
	}
	return nil
}

func addWindowFlags(c *cobra.Command) {
	f := c.Flags()
	f.Float64("trim-start", 0, "drop the first SEC seconds of every recording (warm-up)")
	f.Float64("trim-end", 0, "drop the last SEC seconds of every recording")
	f.Float64("from", 0, "analyze only from SEC seconds after the recording start")
	f.Float64("to", 0, "analyze only up to SEC seconds after the recording start (0 = end)")
}

func windowOpts() analyze.Options {
	return analyze.Options{
		TrimStartSec: viper.GetFloat64("trim-start"),
		TrimEndSec:   viper.GetFloat64("trim-end"),
		FromSec:      viper.GetFloat64("from"),
		ToSec:        viper.GetFloat64("to"),
	}
}

func addAIFlags(c *cobra.Command) {
	f := c.Flags()
	f.Bool("ai", false, "send the analysis to an LLM and print its Markdown answer (key from ANTHROPIC_API_KEY / OPENAI_API_KEY)")
	f.String("ai-provider", "anthropic", "LLM provider: anthropic|openai (openai = any OpenAI-compatible server)")
	f.String("ai-model", "", "model name (provider default when empty)")
	f.String("ai-base-url", "", "API base URL, e.g. http://localhost:11434/v1 for Ollama")
	f.Int("ai-max-tokens", 4096, "maximum tokens in the answer")
	f.Duration("ai-timeout", 180*time.Second, "timeout for the LLM request")
	f.String("app-name", "Omniview", "application name given to the model")
	f.String("app-context", "", "extra application context (e.g. 'Wails app, WKWebView, React 18')")
	f.String("ai-notes", "", "free-form notes for the model (what you are trying to fix)")
	f.String("emit-prompt", "", "write the LLM prompt as Markdown to FILE (or '-' for stdout) and exit; no network or key needed")
	f.Lookup("emit-prompt").NoOptDefVal = "-"
}

// aiContext builds the prompt context from flags/config.
func aiContext(top int) ai.Context {
	c := ai.Context{
		AppName:   viper.GetString("app-name"),
		Platform:  "macOS WKWebView (Wails)",
		Framework: "React",
		Notes:     viper.GetString("ai-notes"),
		TopN:      top,
	}
	if c.AppName == "" {
		c.AppName = "Omniview"
	}
	if extra := strings.TrimSpace(viper.GetString("app-context")); extra != "" {
		if c.Notes != "" {
			c.Notes = extra + "\n" + c.Notes
		} else {
			c.Notes = extra
		}
	}
	return c
}

// aiConfig resolves provider settings: flags > env > config file; the API
// key only ever comes from the environment.
func aiConfig() ai.Config {
	prov := strings.ToLower(strings.TrimSpace(viper.GetString("ai.provider")))
	c := ai.ConfigFromEnv(prov)
	c.Provider = prov
	if c.Provider == "" {
		c.Provider = ai.ProviderAnthropic
	}
	if m := viper.GetString("ai.model"); m != "" {
		c.Model = m
	}
	if u := viper.GetString("ai.base_url"); u != "" {
		c.BaseURL = u
	}
	c.MaxTokens = viper.GetInt("ai.max_tokens")
	c.Timeout = viper.GetDuration("ai.timeout")
	return c
}

// emitPrompt writes the prompt to dst ("-" = stdout).
func emitPrompt(stdout, stderr io.Writer, dst string, p ai.Prompt) error {
	md := p.Markdown()
	if dst == "-" {
		_, err := io.WriteString(stdout, md)
		return err
	}
	if err := writeFile(dst, func(w io.Writer) error { _, err := io.WriteString(w, md); return err }); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %s (~%d tokens)\n", dst, ai.EstimateTokens(md))
	return nil
}

// runAI sends p to the configured provider; Ctrl-C cancels the request.
func runAI(parent context.Context, p ai.Prompt) (string, error) {
	cfg := aiConfig()
	if cfg.Provider == ai.ProviderAnthropic && strings.TrimSpace(cfg.APIKey) == "" {
		return "", fmt.Errorf("--ai needs ANTHROPIC_API_KEY (keys are read from the environment only); use --emit-prompt to get the prompt for another tool, or --ai-provider openai with --ai-base-url for a local model")
	}
	prov, err := ai.NewProvider(cfg)
	if err != nil {
		return "", fmt.Errorf("%w (use --emit-prompt to get the prompt without calling a model)", err)
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()
	out, err := ai.Run(ctx, prov, p)
	if err != nil {
		return "", fmt.Errorf("ai: %w", err)
	}
	return out, nil
}

// expandEmitPrompt rewrites `--emit-prompt -` into `--emit-prompt=-`.
func expandEmitPrompt(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break
		}
		if args[i] == "--emit-prompt" && i+1 < len(args) && args[i+1] == "-" {
			out = append(out, "--emit-prompt=-")
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
