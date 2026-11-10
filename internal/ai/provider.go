package ai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Provider completes a prompt with a language model.
type Provider interface {
	Name() string
	Complete(ctx context.Context, p Prompt) (string, error)
}

// Config selects and configures a provider.
type Config struct {
	Provider  string // "anthropic" | "openai" (any OpenAI-compatible server)
	Model     string
	BaseURL   string
	APIKey    string
	MaxTokens int
	Timeout   time.Duration
}

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"

	defaultAnthropicModel   = "claude-sonnet-4-5"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultOpenAIModel      = "gpt-4o"
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	defaultMaxTokens        = 4096
	defaultTimeout          = 180 * time.Second

	maxErrBody       = 512
	maxResponseBytes = 8 << 20
	retryBackoff     = 500 * time.Millisecond
	retryBackoffCap  = 2 * time.Second
)

// ConfigFromEnv builds a Config for provider ("anthropic" or "openai"; empty
// means anthropic) from ANTHROPIC_API_KEY / OPENAI_API_KEY, WKPERF_AI_MODEL
// and WKPERF_AI_BASE_URL.
func ConfigFromEnv(provider string) Config {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		p = ProviderAnthropic
	}
	c := Config{
		Provider: p,
		Model:    os.Getenv("WKPERF_AI_MODEL"),
		BaseURL:  os.Getenv("WKPERF_AI_BASE_URL"),
	}
	switch p {
	case ProviderAnthropic:
		c.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	case ProviderOpenAI:
		c.APIKey = os.Getenv("OPENAI_API_KEY")
	}
	return c
}

// NewProvider validates c, applies defaults and returns the provider.
func NewProvider(c Config) (Provider, error) {
	if c.MaxTokens <= 0 {
		c.MaxTokens = defaultMaxTokens
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.APIKey = strings.TrimSpace(c.APIKey)

	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case ProviderAnthropic:
		if c.APIKey == "" {
			return nil, errors.New("ai: anthropic provider needs an API key: set ANTHROPIC_API_KEY")
		}
		if c.Model == "" {
			c.Model = defaultAnthropicModel
		}
		if c.BaseURL == "" {
			c.BaseURL = defaultAnthropicBaseURL
		}
		return &anthropicProvider{cfg: c, client: &http.Client{}, backoff: retryBackoff}, nil
	case ProviderOpenAI:
		if c.Model == "" {
			c.Model = defaultOpenAIModel
		}
		if c.BaseURL == "" {
			c.BaseURL = defaultOpenAIBaseURL
		}
		return &openAIProvider{cfg: c, client: &http.Client{}, backoff: retryBackoff}, nil
	default:
		return nil, fmt.Errorf("ai: unknown provider %q (want %q or %q)", c.Provider, ProviderAnthropic, ProviderOpenAI)
	}
}

// httpErr is a non-2xx response.
type httpErr struct {
	provider string
	status   int
	body     string
}

func (e *httpErr) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s: %s", e.provider, e.status, http.StatusText(e.status), e.body)
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// postJSON POSTs body to url with headers, retrying once on 429/5xx, and
// returns the response body of the first 2xx response. Errors never contain
// secret (the API key), even if the server echoes it back.
func postJSON(ctx context.Context, client *http.Client, provider, url string, headers map[string]string,
	body []byte, secret string, backoff time.Duration) ([]byte, error) {

	const attempts = 2
	var lastErr error
	for attempt := range attempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %s", provider, redact(err.Error(), secret))
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%s: %w", provider, ctx.Err())
			}
			return nil, fmt.Errorf("%s: request failed: %s", provider, redact(err.Error(), secret))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("%s: read response: %s", provider, redact(readErr.Error(), secret))
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		lastErr = &httpErr{
			provider: provider,
			status:   resp.StatusCode,
			body:     truncate(redact(strings.TrimSpace(string(data)), secret), maxErrBody),
		}
		if !retryable(resp.StatusCode) || attempt == attempts-1 {
			return nil, lastErr
		}
		if err := sleep(ctx, retryDelay(resp, backoff)); err != nil {
			return nil, fmt.Errorf("%s: %w (after %v)", provider, err, lastErr)
		}
	}
	return nil, lastErr
}

// retryDelay honours Retry-After (seconds) but never exceeds the small cap.
func retryDelay(resp *http.Response, backoff time.Duration) time.Duration {
	d := backoff
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			d = time.Duration(secs) * time.Second
		}
	}
	if d > retryBackoffCap {
		d = retryBackoffCap
	}
	return d
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…(truncated)"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
