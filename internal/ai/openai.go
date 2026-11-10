package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openAIProvider speaks the OpenAI chat completions API. It works with any
// compatible server (OpenAI, Ollama, LM Studio, vLLM, OpenRouter) by setting
// BaseURL; the API key is optional for local servers.
type openAIProvider struct {
	cfg     Config
	client  *http.Client
	backoff time.Duration
}

func (o *openAIProvider) Name() string { return ProviderOpenAI }

type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			// Content is normally a string; some servers return a list of
			// {type,text} parts.
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (o *openAIProvider) Complete(ctx context.Context, p Prompt) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.cfg.Timeout)
	defer cancel()

	var msgs []openAIMessage
	if p.System != "" {
		msgs = append(msgs, openAIMessage{Role: "system", Content: p.System})
	}
	msgs = append(msgs, openAIMessage{Role: "user", Content: p.User})
	body, err := json.Marshal(openAIRequest{Model: o.cfg.Model, Messages: msgs, MaxTokens: o.cfg.MaxTokens})
	if err != nil {
		return "", fmt.Errorf("openai: encode request: %w", err)
	}

	headers := map[string]string{}
	if o.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + o.cfg.APIKey
	}
	data, err := postJSON(ctx, o.client, "openai", o.cfg.BaseURL+"/chat/completions", headers, body, o.cfg.APIKey, o.backoff)
	if err != nil {
		return "", err
	}

	var r openAIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("openai: decode response: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("openai: API error %s: %s", r.Error.Type, truncate(redact(r.Error.Message, o.cfg.APIKey), maxErrBody))
	}
	if len(r.Choices) == 0 {
		return "", errors.New("openai: response contained no choices")
	}
	text := openAIText(r.Choices[0].Message.Content)
	if text == "" {
		return "", errors.New("openai: response contained no text content")
	}
	return text, nil
}

func openAIText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}
