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

const anthropicVersion = "2023-06-01"

type anthropicProvider struct {
	cfg     Config
	client  *http.Client
	backoff time.Duration
}

func (a *anthropicProvider) Name() string { return ProviderAnthropic }

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *anthropicProvider) Complete(ctx context.Context, p Prompt) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	body, err := json.Marshal(anthropicRequest{
		Model:     a.cfg.Model,
		MaxTokens: a.cfg.MaxTokens,
		System:    p.System,
		Messages:  []anthropicMessage{{Role: "user", Content: p.User}},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: encode request: %w", err)
	}
	data, err := postJSON(ctx, a.client, "anthropic", a.cfg.BaseURL+"/v1/messages", map[string]string{
		"x-api-key":         a.cfg.APIKey,
		"anthropic-version": anthropicVersion,
	}, body, a.cfg.APIKey, a.backoff)
	if err != nil {
		return "", err
	}

	var r anthropicResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("anthropic: decode response: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("anthropic: API error %s: %s", r.Error.Type, truncate(redact(r.Error.Message, a.cfg.APIKey), maxErrBody))
	}
	var b strings.Builder
	for _, blk := range r.Content {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("anthropic: response contained no text content")
	}
	return b.String(), nil
}
