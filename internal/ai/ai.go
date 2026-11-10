package ai

import (
	"context"
	"errors"
	"strings"
)

// Run sends p to prov and returns the trimmed Markdown answer.
func Run(ctx context.Context, prov Provider, p Prompt) (string, error) {
	if prov == nil {
		return "", errors.New("ai: no provider")
	}
	out, err := prov.Complete(ctx, p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
