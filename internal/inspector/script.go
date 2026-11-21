package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const scriptTimeout = 60 * time.Second

type evaluation struct {
	WasThrown bool `json:"wasThrown"`
	Result    *struct {
		ObjectID    string          `json:"objectId"`
		Description string          `json:"description"`
		Type        string          `json:"type"`
		Value       json.RawMessage `json:"value"`
	} `json:"result"`
}

func (s *session) runScript(ctx context.Context, source []byte) error {
	ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	// Always return a native Promise so the body can use top-level await. The
	// WebKit protocol only exposes awaitPromise as a separate command.
	promise, err := s.evaluate(ctx, "Runtime.evaluate", struct {
		Expression string `json:"expression"`
		NoPause    bool   `json:"doNotPauseOnExceptionsAndMuteConsole"`
	}{"(async () => {\n" + string(source) + "\n})()", true})
	if err != nil {
		return err
	}
	if promise.Result.ObjectID == "" {
		return fmt.Errorf("workload script: WebKit returned no promise object ID")
	}
	_, err = s.evaluate(ctx, "Runtime.awaitPromise", struct {
		PromiseObjectID string `json:"promiseObjectId"`
	}{promise.Result.ObjectID})
	return err
}

func (s *session) evaluate(ctx context.Context, method string, params any) (evaluation, error) {
	var r evaluation
	data, err := s.exchange(ctx, method, params)
	if err != nil {
		return r, fmt.Errorf("workload script %s: %w", method, err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("decode workload script %s reply: %w", method, err)
	}
	if r.Result == nil {
		return r, fmt.Errorf("workload script %s returned no result", method)
	}
	if r.WasThrown {
		detail := r.Result.Description
		if detail == "" && len(r.Result.Value) != 0 {
			if err := json.Unmarshal(r.Result.Value, &detail); err != nil || string(r.Result.Value) == "null" {
				detail = string(r.Result.Value)
			}
			return r, fmt.Errorf("workload script threw %q", detail)
		}
		if detail == "" {
			detail = r.Result.Type
		}
		return r, fmt.Errorf("workload script threw %q", detail)
	}
	return r, nil
}
