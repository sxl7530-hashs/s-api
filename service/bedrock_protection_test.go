package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockModelAndChannelCeilingsReserveTogether(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	settings := &dto.BedrockProtectionSettings{ModelLimits: map[string]dto.BedrockModelLimit{"opus": {RPM: 1, TPM: 60}, "sonnet": {RPM: 2}}}
	allowed, _ := ReserveBedrockUsage(45, 60, settings, "opus")
	require.True(t, allowed)
	assert.Empty(t, server.Keys(), "disabled switches must not access counters")
	settings.RateLimitEnabled, settings.ModelRateLimitEnabled = true, true
	settings.RPM = 3
	allowed, _ = ReserveBedrockUsage(45, 60, settings, "opus")
	require.True(t, allowed)
	allowed, usage := ReserveBedrockUsage(45, 1, settings, "opus")
	assert.False(t, allowed)
	assert.Equal(t, int64(1), usage.RPM)
	allowed, _ = ReserveBedrockUsage(45, 40, settings, "sonnet")
	require.True(t, allowed)
	allowed, _ = ReserveBedrockUsage(45, 10, settings, "unlisted")
	require.True(t, allowed, "unlisted models use only the channel ceiling")
	allowed, _ = ReserveBedrockUsage(45, 10, settings, "sonnet")
	assert.False(t, allowed, "model capacity cannot bypass the channel ceiling")
	settings.RPM = 4
	allowed, usage = ReserveBedrockUsage(45, 10, settings, "sonnet")
	assert.True(t, allowed, "channel rejection must not consume model capacity")
	assert.Equal(t, int64(4), usage.RPM)
	assert.Equal(t, int64(120), usage.TPM)
	settings.RateLimitEnabled = false
	allowed, usage = ReserveBedrockUsage(45, 10, settings, "unlisted")
	assert.True(t, allowed)
	assert.Equal(t, int64(5), usage.RPM, "model-only protection still reports aggregate channel traffic")
	assert.Equal(t, int64(130), usage.TPM)
}

func TestBedrockRampProgressAndEffectiveCeilings(t *testing.T) {
	useBedrockProtectionRedis(t)
	start := time.Unix(1800000000, 0)
	fiveDayRamp := &dto.BedrockProtectionSettings{
		RampUpEnabled:        true,
		RampUpStartUnix:      start.Unix(),
		RampUpDays:           5,
		RampUpInitialPercent: 10,
	}
	for _, tc := range []struct {
		day     int
		percent int
	}{{0, 10}, {1, 32}, {2, 55}, {3, 77}, {4, 100}} {
		assert.Equal(t, tc.percent, BedrockRampPercent(fiveDayRamp, start.Add(time.Duration(tc.day)*24*time.Hour)))
	}
	settings := &dto.BedrockProtectionSettings{RampUpEnabled: true, RampUpStartUnix: start.Unix()}
	for _, tc := range []struct {
		elapsed time.Duration
		percent int
	}{
		{-time.Hour, 5}, {0, 5}, {24*time.Hour - time.Second, 5}, {24 * time.Hour, 20},
		{5 * 24 * time.Hour, 84}, {6 * 24 * time.Hour, 100}, {10 * 24 * time.Hour, 100},
	} {
		assert.Equal(t, tc.percent, BedrockRampPercent(settings, start.Add(tc.elapsed)))
	}
	settings.RampUpDays = 2
	assert.Equal(t, 100, BedrockRampPercent(settings, start.Add(24*time.Hour)))
	settings.RampUpEnabled = false
	assert.Equal(t, 100, BedrockRampPercent(settings, start))
	settings.RampUpEnabled, settings.RateLimitEnabled, settings.ModelRateLimitEnabled = true, true, true
	settings.RampUpDays, settings.RampUpStartUnix = 7, time.Now().Unix()
	settings.RPM, settings.TPM = 40, 1000
	settings.ModelLimits = map[string]dto.BedrockModelLimit{"opus": {RPM: 20, TPM: 500}}
	allowed, _ := ReserveBedrockUsage(46, 25, settings, "opus")
	require.True(t, allowed)
	allowed, _ = ReserveBedrockUsage(46, 1, settings, "opus")
	assert.False(t, allowed, "the initial model ceiling is 1 RPM / 25 TPM")
	allowed, _ = ReserveBedrockUsage(46, 25, settings, "unlisted")
	require.True(t, allowed)
	allowed, _ = ReserveBedrockUsage(46, 0, settings, "unlisted")
	assert.False(t, allowed, "the initial aggregate ceiling is 2 RPM / 50 TPM")
}

func TestBedrockBackoffDefaultsBoundsAndCancellation(t *testing.T) {
	assert.Zero(t, BedrockRetryDelay(nil, 0))
	assert.Zero(t, BedrockRetryDelay(&dto.BedrockProtectionSettings{}, 0))
	settings := &dto.BedrockProtectionSettings{RetryBackoffEnabled: true, RetryBackoffBaseMS: 100, RetryBackoffMaxMS: 300}
	for _, tc := range []struct {
		attempt int
		ceiling time.Duration
	}{{0, 100 * time.Millisecond}, {1, 200 * time.Millisecond}, {2, 300 * time.Millisecond}, {100, 300 * time.Millisecond}} {
		delay := BedrockRetryDelay(settings, tc.attempt)
		assert.GreaterOrEqual(t, delay, time.Duration(0))
		assert.LessOrEqual(t, delay, tc.ceiling)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, WaitBedrockRetry(ctx, settings, 100), context.Canceled)
}

func useBedrockProtectionRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	previousClient := common.RDB
	previousEnabled := common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() {
		require.NoError(t, common.RDB.Close())
		common.RDB = previousClient
		common.RedisEnabled = previousEnabled
	})
	return server
}

func TestReserveBedrockUsageIsAtomicAndDefaultsOff(t *testing.T) {
	useBedrockProtectionRedis(t)
	allowed, usage := ReserveBedrockUsage(42, 90, nil, "opus")
	assert.True(t, allowed)
	assert.False(t, usage.Available)

	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 2, TPM: 100}
	allowed, usage = ReserveBedrockUsage(42, 60, settings, "opus")
	require.True(t, allowed)
	assert.Equal(t, int64(1), usage.RPM)
	assert.Equal(t, int64(60), usage.TPM)

	allowed, usage = ReserveBedrockUsage(42, 41, settings, "opus")
	assert.False(t, allowed)
	assert.Equal(t, int64(1), usage.RPM)
	assert.Equal(t, int64(60), usage.TPM)
}

func TestBedrockRollingWindowDoesNotResetAtMinuteBoundary(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	start := time.Unix(1800000059, 0)
	server.SetTime(start)
	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 2, TPM: 100, ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{"opus": {TPM: 60}}}
	allowed, _ := ReserveBedrockUsage(95, 60, settings, "opus")
	require.True(t, allowed)
	server.SetTime(start.Add(time.Second))
	allowed, _ = ReserveBedrockUsage(95, 1, settings, "opus")
	assert.False(t, allowed, "model TPM survives a minute boundary")
	allowed, _ = ReserveBedrockUsage(95, 40, settings, "sonnet")
	require.True(t, allowed)
	allowed, usage := ReserveBedrockUsage(95, 1, settings, "sonnet")
	assert.False(t, allowed, "channel RPM/TPM survive a minute boundary")
	assert.EqualValues(t, 2, usage.RPM)
	assert.EqualValues(t, 100, usage.TPM)
	server.SetTime(start.Add(60 * time.Second))
	allowed, _ = ReserveBedrockUsage(95, 1, settings, "opus")
	assert.False(t, allowed, "the boundary second is retained conservatively")
	server.SetTime(start.Add(61 * time.Second))
	allowed, usage = ReserveBedrockUsage(95, 60, settings, "opus")
	require.True(t, allowed)
	assert.EqualValues(t, 2, usage.RPM)
	assert.EqualValues(t, 100, usage.TPM)
	server.SetTime(start.Add(62 * time.Second))
	allowed, usage = ReserveBedrockUsage(95, 40, settings, "sonnet")
	require.True(t, allowed)
	assert.EqualValues(t, 2, usage.RPM)
	assert.EqualValues(t, 100, usage.TPM)
}

func TestBedrockUnknownTPMExpiresWithoutLosingRPM(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	start := time.Unix(1800000059, 0)
	server.SetTime(start)
	settings := &dto.BedrockProtectionSettings{UsageMetricsEnabled: true}
	allowed, usage := ReserveBedrockUsage(96, -1, settings, "opus")
	require.True(t, allowed)
	assert.Equal(t, BedrockUsage{RPM: 1, Available: true, TPMUnavailable: true}, usage)
	server.SetTime(start.Add(time.Second))
	allowed, usage = ReserveBedrockUsage(96, 20, settings, "opus")
	require.True(t, allowed)
	assert.Equal(t, BedrockUsage{RPM: 2, TPM: 20, Available: true, TPMUnavailable: true}, usage)
	settings.RateLimitEnabled, settings.TPM = true, 100
	allowed, _ = ReserveBedrockUsage(96, 1, settings, "opus")
	assert.False(t, allowed, "enabling TPM cannot treat unknown recent usage as zero")
	server.SetTime(start.Add(61 * time.Second))
	allowed, usage = ReserveBedrockUsage(96, 30, settings, "opus")
	require.True(t, allowed)
	assert.Equal(t, BedrockUsage{RPM: 2, TPM: 50, Available: true}, usage)
	allowed, _ = ReserveBedrockUsage(96, -1, settings, "opus")
	assert.False(t, allowed, "unknown estimates cannot bypass an active TPM ceiling")
}

func TestBedrockUsageMetricsCanRunWithoutEnablingLimits(t *testing.T) {
	useBedrockProtectionRedis(t)
	db := setupChannelSelectAutoGroupsTest(t)
	settings := &dto.BedrockProtectionSettings{UsageMetricsEnabled: true}
	for _, id := range []int{43, 44, 45, 46} {
		channel := &model.Channel{Id: id, Type: constant.ChannelTypeAws}
		channel.SetSetting(dto.ChannelSettings{BedrockProtection: settings})
		if id == 44 {
			channel.SetSetting(dto.ChannelSettings{})
		}
		if id == 45 {
			channel.Type = constant.ChannelTypeOpenAI
		}
		require.NoError(t, db.Create(channel).Error)
	}
	model.InitChannelCache()

	allowed, usage := ReserveBedrockUsage(43, 120, settings, "opus")
	require.True(t, allowed)
	assert.Equal(t, BedrockUsage{RPM: 1, TPM: 120, Available: true}, usage)
	for _, cached := range []bool{true, false} {
		common.MemoryCacheEnabled = cached
		usages := GetBedrockUsage([]int{43, 44, 45, 46})
		assert.Equal(t, usage, usages[43])
		assert.NotContains(t, usages, 44, "disabled collection must be unavailable")
		assert.NotContains(t, usages, 45, "other providers must be unavailable")
		assert.Equal(t, BedrockUsage{Available: true}, usages[46], "enabled idle channels display zero")
	}
	allowed, usage = ReserveBedrockUsage(43, -1, settings, "opus")
	require.True(t, allowed)
	assert.True(t, usage.TPMUnavailable)
	assert.Equal(t, usage, GetBedrockUsage([]int{43})[43], "the metrics endpoint preserves unknown TPM independently of RPM")
}

func TestBedrockCapacityRejectionSelectsAnotherChannelInSameTier(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	for _, id := range []int{71, 72, 73} {
		createChannelSelectAutoGroupsChannel(t, db, id, "default", "capacity-model")
	}
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 73).Update("priority", -1).Error)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 73).Update("priority", -1).Error)
	model.InitChannelCache()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "capacity-model"}
	first, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Contains(t, []int{71, 72}, first.Id)
	param.ExcludeCapacityRejectedChannel(first.Id)
	param.IncreaseRetry()
	second, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Contains(t, []int{71, 72}, second.Id, "capacity failure must not skip a healthy peer")
	assert.NotEqual(t, first.Id, second.Id)
	assert.Equal(t, 1, param.GetRetry(), "capacity failure still consumes the retry budget")
	param.ExcludeCapacityRejectedChannel(second.Id)
	param.IncreaseRetry()
	third, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, third)
	assert.Equal(t, 73, third.Id)
	param.ExcludeCapacityRejectedChannel(third.Id)
	param.IncreaseRetry()
	exhausted, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	assert.Nil(t, exhausted, "an exhausted pool must not select a rejected channel again")
	fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: fresh, TokenGroup: "default", ModelName: "capacity-model"})
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Contains(t, []int{71, 72}, selected.Id, "capacity exclusions must stay within the original request")
}

func TestBedrockProtectionFailsOpenWhenRedisIsUnavailable(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	server.Close()
	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 1}

	allowed, usage := ReserveBedrockUsage(44, 1, settings, "opus")
	assert.True(t, allowed)
	assert.False(t, usage.Available)
}

func TestBedrockCooldownRequiresConfiguredTimeSpan(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	settings := &dto.BedrockProtectionSettings{ThrottlingCooldownEnabled: true, Consecutive429Threshold: 2, ObservationWindowSeconds: 60, Minimum429SpanSeconds: 5, CooldownSeconds: 30}

	RecordBedrockThrottle(7, settings)
	RecordBedrockThrottle(7, settings)
	assert.False(t, IsBedrockCoolingDown(7, settings))

	ctx := t.Context()
	require.NoError(t, common.RDB.HSet(ctx, bedrockStreakKey(7), "first", time.Now().Add(-10*time.Second).UnixMilli()).Err())
	RecordBedrockThrottle(7, settings)
	assert.True(t, IsBedrockCoolingDown(7, settings))

	RecordBedrockNonThrottle(7, settings)
	assert.True(t, IsBedrockCoolingDown(7, settings), "successful calls reset the observation window, not an active cooldown")
	settings.ThrottlingCooldownEnabled = false
	assert.False(t, IsBedrockCoolingDown(7, settings), "turning off cooldown must restore channel availability immediately")
	settings.ThrottlingCooldownEnabled = true
	server.FastForward(29 * time.Second)
	assert.True(t, IsBedrockCoolingDown(7, settings), "cooldown must retain the configured duration")
	server.FastForward(time.Second)
	assert.False(t, IsBedrockCoolingDown(7, settings), "expired cooldown must not keep the channel unavailable")
}

func TestBedrockThrottleSuccessResetsConsecutiveSequence(t *testing.T) {
	useBedrockProtectionRedis(t)
	settings := &dto.BedrockProtectionSettings{ThrottlingCooldownEnabled: true, Consecutive429Threshold: 2, ObservationWindowSeconds: 60, Minimum429SpanSeconds: 1, CooldownSeconds: 30}
	ctx := t.Context()

	RecordBedrockThrottle(8, settings)
	require.NoError(t, common.RDB.HSet(ctx, bedrockStreakKey(8), "first", time.Now().Add(-2*time.Second).UnixMilli()).Err())
	RecordBedrockNonThrottle(8, settings)
	RecordBedrockThrottle(8, settings)

	assert.False(t, IsBedrockCoolingDown(8, settings))
}

func TestBedrockCooldownIgnoresStreakOutsideObservationWindow(t *testing.T) {
	useBedrockProtectionRedis(t)
	settings := &dto.BedrockProtectionSettings{ThrottlingCooldownEnabled: true, Consecutive429Threshold: 2, ObservationWindowSeconds: 60, Minimum429SpanSeconds: 1, CooldownSeconds: 30}
	RecordBedrockThrottle(10, settings)
	require.NoError(t, common.RDB.HSet(t.Context(), bedrockStreakKey(10), "first", time.Now().Add(-61*time.Second).UnixMilli()).Err())
	RecordBedrockThrottle(10, settings)
	assert.False(t, IsBedrockCoolingDown(10, settings))
}

func TestBedrockCooldownCanUseWindowRatio(t *testing.T) {
	useBedrockProtectionRedis(t)
	settings := &dto.BedrockProtectionSettings{ThrottlingCooldownEnabled: true, Consecutive429Threshold: 100, ObservationWindowSeconds: 60, Minimum429SpanSeconds: 1, MinimumSampleSize: 4, ThrottlingRatioPercent: 50, CooldownSeconds: 30}

	RecordBedrockNonThrottle(9, settings)
	RecordBedrockThrottle(9, settings)
	RecordBedrockNonThrottle(9, settings)
	RecordBedrockThrottle(9, settings)

	assert.True(t, IsBedrockCoolingDown(9, settings))
}
