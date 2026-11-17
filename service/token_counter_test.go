package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountTextTokenContextBoundsLargeOpenAIInput(t *testing.T) {
	InitTokenEncoders()
	text := strings.Repeat("short words 中文 ", exactTokenizerTextLimit/8)
	got, err := CountTextTokenContext(context.Background(), text, "gpt-5")
	require.NoError(t, err)
	assert.Equal(t, EstimateTokenByModel("gpt-5", text), got)
}

func TestCountTextTokenContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CountTextTokenContext(ctx, strings.Repeat("a", exactTokenizerTextLimit+1), "gpt-5")
	require.ErrorIs(t, err, context.Canceled)
}

func TestCountTextTokenContextLargeContinuousInputHasQuotaFloor(t *testing.T) {
	text := strings.Repeat("a", exactTokenizerTextLimit+1)
	got, err := CountTextTokenContext(context.Background(), text, "gpt-5")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, got, (len(text)+7)/8)
}
