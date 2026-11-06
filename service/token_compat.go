package service

import (
	"context"
	"strings"
)

// Keep the legacy estimator API available for provider adapters that have not
// migrated to tokenkit yet. New billing paths use tokenkit directly.
const exactTokenizerTextLimit = 64 << 10

func CountTextToken(text string, model string) int {
	tokens, _ := CountTextTokenContext(context.Background(), text, model)
	return tokens
}

func CountTextTokenContext(ctx context.Context, text string, model string) (int, error) {
	if text == "" {
		return 0, nil
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	if !isOpenAITextModel(model) || len(text) > exactTokenizerTextLimit {
		tokens := EstimateTokenByModel(model, text)
		byteFloor := (len(text) + 7) / 8
		if byteFloor > tokens {
			tokens = byteFloor
		}
		return tokens, nil
	}
	return getTokenNum(getTokenEncoder(model), text), nil
}

func isOpenAITextModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") ||
		strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") ||
		strings.HasPrefix(lower, "chatgpt-")
}
