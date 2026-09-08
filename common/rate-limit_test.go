package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryRateLimiterBoundsInitialAllocation(t *testing.T) {
	limiter := &InMemoryRateLimiter{}
	limiter.Init(0)

	require.True(t, limiter.Request("one-shot-ip", 1_000_000, 60))
	queue := limiter.shardForKey("one-shot-ip").store["one-shot-ip"]
	require.NotNil(t, queue)
	assert.Len(t, *queue, 1)
	assert.LessOrEqual(t, cap(*queue), maxRateLimiterInitialCapacity)

	require.True(t, limiter.Request("small-limit", 2, 60))
	smallQueue := limiter.shardForKey("small-limit").store["small-limit"]
	require.NotNil(t, smallQueue)
	assert.Equal(t, 2, cap(*smallQueue))
}
