package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sk-ant-SECRET-1234567890"

func newTest(t *testing.T, cfg Config) Provider {
	t.Helper()
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	switch v := p.(type) {
	case *anthropicProvider:
		v.backoff = time.Millisecond
	case *openAIProvider:
		v.backoff = time.Millisecond
	}
	return p
}

func TestAnthropicRequestShapeAndMultiBlockResponse(t *testing.T) {
	var gotPath string
	var gotHdr http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("bad request json: %v", err)
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"Hello, "},{"type":"tool_use","id":"x"},{"type":"text","text":"world"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL + "/"})
	out, err := p.Complete(context.Background(), Prompt{System: "SYS", User: "USR"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello, world" {
		t.Errorf("out = %q", out)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotHdr.Get("x-api-key") != testKey {
		t.Errorf("x-api-key = %q", gotHdr.Get("x-api-key"))
	}
	if gotHdr.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version = %q", gotHdr.Get("anthropic-version"))
	}
	if gotHdr.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", gotHdr.Get("Content-Type"))
	}
	if gotBody["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v", gotBody["max_tokens"])
	}
	if gotBody["system"] != "SYS" {
		t.Errorf("system should be a top-level field, got %v", gotBody["system"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", gotBody["messages"])
	}
	m := msgs[0].(map[string]any)
	if m["role"] != "user" || m["content"] != "USR" {
		t.Errorf("message = %v", m)
	}
}

func TestAnthropicConfigurableModelAndMaxTokens(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL, Model: "claude-x", MaxTokens: 123})
	if _, err := p.Complete(context.Background(), Prompt{System: "s", User: "u"}); err != nil {
		t.Fatal(err)
	}
	if gotBody["model"] != "claude-x" || gotBody["max_tokens"] != float64(123) {
		t.Errorf("body = %v", gotBody)
	}
}

func TestOpenAIRequestShapeNoKey(t *testing.T) {
	var gotPath string
	var gotAuth string
	var hasAuth bool
	var gotBody struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct{ Role, Content string }
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, hasAuth = r.Header["Authorization"]
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"local answer"}}]}`)
	}))
	defer srv.Close()

	p := newTest(t, Config{Provider: "openai", BaseURL: srv.URL + "/v1", Model: "llama3"})
	out, err := p.Complete(context.Background(), Prompt{System: "SYS", User: "USR"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "local answer" {
		t.Errorf("out = %q", out)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if hasAuth {
		t.Errorf("Authorization header sent without a key: %q", gotAuth)
	}
	if gotBody.Model != "llama3" || gotBody.MaxTokens != 4096 {
		t.Errorf("body = %+v", gotBody)
	}
	if len(gotBody.Messages) != 2 ||
		gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "SYS" ||
		gotBody.Messages[1].Role != "user" || gotBody.Messages[1].Content != "USR" {
		t.Errorf("messages = %+v", gotBody.Messages)
	}
}

func TestOpenAIBearerKeyAndContentParts(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"choices":[{"message":{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}}]}`)
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "openai", APIKey: "k-123", BaseURL: srv.URL})
	out, err := p.Complete(context.Background(), Prompt{User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k-123" {
		t.Errorf("auth = %q", gotAuth)
	}
	if out != "ab" {
		t.Errorf("out = %q", out)
	}
}

func TestRetryOn429And5xxThenSucceed(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(status)
				io.WriteString(w, "try later")
				return
			}
			io.WriteString(w, `{"content":[{"type":"text","text":"recovered"}]}`)
		}))
		p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL})
		out, err := p.Complete(context.Background(), Prompt{User: "u"})
		srv.Close()
		if err != nil || out != "recovered" {
			t.Errorf("status %d: out=%q err=%v", status, out, err)
		}
		if calls.Load() != 2 {
			t.Errorf("status %d: calls = %d, want 2", status, calls.Load())
		}
	}
}

func TestRetryExhaustedReportsStatusAndBody(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream exploded")
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "openai", BaseURL: srv.URL})
	_, err := p.Complete(context.Background(), Prompt{User: "u"})
	if err == nil {
		t.Fatal("want error")
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want exactly one retry", calls.Load())
	}
	for _, want := range []string{"502", "upstream exploded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestClientErrorNotRetriedAndBodyTruncated(t *testing.T) {
	var calls atomic.Int32
	long := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, long)
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL})
	_, err := p.Complete(context.Background(), Prompt{User: "u"})
	if err == nil {
		t.Fatal("want error")
	}
	if calls.Load() != 1 {
		t.Errorf("400 must not be retried, calls = %d", calls.Load())
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error lacks status: %v", err)
	}
	if len(err.Error()) > 700 {
		t.Errorf("error body not truncated: %d bytes", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("truncation not marked: %v", err)
	}
}

func TestAPIKeyNeverLeaksInErrors(t *testing.T) {
	// Server echoes the key it received, in a 401 body and in a 200 API error.
	for _, mode := range []string{"401", "200-error"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("x-api-key")
			if mode == "401" {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"error":"invalid key `+key+`"}`)
				return
			}
			io.WriteString(w, `{"error":{"type":"auth","message":"bad key `+key+`"}}`)
		}))
		p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL})
		_, err := p.Complete(context.Background(), Prompt{User: "u"})
		srv.Close()
		if err == nil {
			t.Fatalf("%s: want error", mode)
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: error leaks API key: %v", mode, err)
		}
		if !strings.Contains(err.Error(), "[redacted]") {
			t.Errorf("%s: expected redaction marker: %v", mode, err)
		}
	}
}

func TestNetworkErrorDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused
	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: url})
	_, err := p.Complete(context.Background(), Prompt{User: "u"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("leak: %v", err)
	}
}

func TestTimeoutAndContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	p := newTest(t, Config{Provider: "openai", BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	start := time.Now()
	_, err := p.Complete(context.Background(), Prompt{User: "u"})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout not honoured: %v", time.Since(start))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p2 := newTest(t, Config{Provider: "openai", BaseURL: srv.URL})
	if _, err := p2.Complete(ctx, Prompt{User: "u"}); err == nil {
		t.Error("want error for cancelled context")
	}
}

func TestEmptyContentIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[]}`)
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL})
	if _, err := p.Complete(context.Background(), Prompt{User: "u"}); err == nil {
		t.Error("want error for empty content")
	}
}

func TestNewProviderValidation(t *testing.T) {
	_, err := NewProvider(Config{Provider: "anthropic"})
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("missing key error = %v", err)
	}
	if _, err := NewProvider(Config{Provider: "nope"}); err == nil {
		t.Error("unknown provider should fail")
	}
	p, err := NewProvider(Config{Provider: "OpenAI"})
	if err != nil || p.Name() != "openai" {
		t.Errorf("openai without key should work: %v %v", p, err)
	}
	p, err = NewProvider(Config{Provider: "anthropic", APIKey: "k"})
	if err != nil || p.Name() != "anthropic" {
		t.Errorf("anthropic: %v %v", p, err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ak")
	t.Setenv("OPENAI_API_KEY", "ok")
	t.Setenv("WKPERF_AI_MODEL", "m1")
	t.Setenv("WKPERF_AI_BASE_URL", "http://localhost:11434/v1")

	a := ConfigFromEnv("anthropic")
	if a.Provider != "anthropic" || a.APIKey != "ak" || a.Model != "m1" || a.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("anthropic cfg = %+v", a)
	}
	o := ConfigFromEnv("OPENAI")
	if o.Provider != "openai" || o.APIKey != "ok" {
		t.Errorf("openai cfg = %+v", o)
	}
	if d := ConfigFromEnv(""); d.Provider != "anthropic" {
		t.Errorf("default provider = %q", d.Provider)
	}
}

func TestRunTrimsAndPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"\n  answer \n"}]}`)
	}))
	defer srv.Close()
	p := newTest(t, Config{Provider: "anthropic", APIKey: testKey, BaseURL: srv.URL})
	out, err := Run(context.Background(), p, Prompt{User: "u"})
	if err != nil || out != "answer" {
		t.Errorf("out=%q err=%v", out, err)
	}
	if _, err := Run(context.Background(), nil, Prompt{}); err == nil {
		t.Error("nil provider should error")
	}
}
