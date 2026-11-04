package service

import (
	"context"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"net/http/httptest"
	"os"
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
		FlushBedrockActualUsage()
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
	previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
	server := useBedrockProtectionRedis(t)
	start := time.Unix(1800000059, 0)
	server.SetTime(start)
	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 100}
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
	previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
	useBedrockProtectionRedis(t)
	db := setupChannelSelectAutoGroupsTest(t)
	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 100}
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
	RecordBedrockActualAttempt(43)
	RecordBedrockActualTokens(43, 12, true)
	FlushBedrockActualUsage()
	t.Cleanup(func() { FlushBedrockActualUsage() })
	for _, cached := range []bool{true, false} {
		common.MemoryCacheEnabled = cached
		usages := GetBedrockUsage([]int{43, 44, 45, 46})
		assert.Equal(t, BedrockUsage{RPM: 1, TPM: 12, Available: true}, usages[43])
		assert.Equal(t, BedrockUsage{Available: true}, usages[44], "global collection includes unconfigured AWS channels")
		assert.NotContains(t, usages, 45, "other providers must be unavailable")
		assert.Equal(t, BedrockUsage{Available: true}, usages[46], "enabled idle channels display zero")
	}
	allowed, usage = ReserveBedrockUsage(43, -1, settings, "opus")
	require.True(t, allowed)
	assert.True(t, usage.TPMUnavailable)
	assert.Equal(t, BedrockUsage{RPM: 1, TPM: 12, Available: true}, GetBedrockUsage([]int{43})[43], "limiter estimates never leak into the actual usage display")
	RecordBedrockActualTokens(43, 999, false)
	FlushBedrockActualUsage()
	assert.True(t, GetBedrockUsage([]int{43})[43].TPMUnavailable)
	assert.Equal(t, int64(12), GetBedrockUsage([]int{43})[43].TPM, "incomplete usage must not inflate authoritative totals")
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = false
	assert.Empty(t, GetBedrockUsage([]int{43}))
}

func TestBedrockMetricsSkipDisabledRedisWithoutReplayingAfterRecovery(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	FlushBedrockActualUsage()
	common.RedisEnabled = false
	RecordBedrockActualAttempt(105)
	RecordBedrockActualTokens(105, 99, true)
	common.RedisEnabled = true
	FlushBedrockActualUsage()
	assert.Empty(t, server.Keys(), "disabled Redis must not buffer statistics for later replay")
	RecordBedrockActualAttempt(105)
	RecordBedrockActualTokens(105, 25, true)
	FlushBedrockActualUsage()
	assert.Equal(t, "1", server.HGet(bedrockActualUsageKey(105), "rpm"))
	assert.Equal(t, "25", server.HGet(bedrockActualUsageKey(105), "tpm"))
}

func TestBedrockUsageBatchReplayDoesNotDoubleCountAcrossWriters(t *testing.T) {
	useBedrockProtectionRedis(t)
	for _, tc := range []struct {
		writer, batch string
		rpm, tpm      int
		unknown       int
		invalid       int
		want          []int64
	}{
		{"instance-a", "batch-a1", 2, 12, 1, 0, []int64{2, 12, 1, 0}},
		{"instance-b", "batch-b1", 3, 20, 0, 1, []int64{5, 32, 1, 1}},
		{"instance-a", "batch-a1", 2, 12, 1, 0, []int64{5, 32, 1, 1}},
		{"instance-b", "batch-b1", 3, 20, 0, 1, []int64{5, 32, 1, 1}},
		{"instance-a", "batch-a2", 1, 7, 0, 0, []int64{6, 39, 1, 1}},
	} {
		values, err := common.RDB.Eval(t.Context(), recordBedrockActualUsageScript,
			[]string{bedrockActualUsageKey(91)}, tc.rpm, tc.tpm, tc.unknown, tc.invalid, tc.writer, tc.batch).Int64Slice()
		require.NoError(t, err)
		assert.Equal(t, tc.want, values, "%s/%s", tc.writer, tc.batch)
	}
}

func TestBedrockSharedRedisScripts(t *testing.T) {
	addr := os.Getenv("TEST_BEDROCK_REDIS_ADDR")
	if addr == "" {
		t.Skip("set TEST_BEDROCK_REDIS_ADDR to verify shared counters against real Redis")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := redis.NewClient(&redis.Options{Addr: addr})
	second := redis.NewClient(&redis.Options{Addr: addr})
	key := "bedrock:test:{" + common.GetUUID() + "}"
	modelKey, actualKey := key+":model", key+":actual"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		assert.NoError(t, first.Del(cleanupCtx, key, modelKey, actualKey).Err())
		assert.NoError(t, first.Close())
		assert.NoError(t, second.Close())
	})
	require.NoError(t, first.Ping(ctx).Err())

	// Two independent instances compete for one remaining channel/model slot.
	type reservation struct {
		values []int64
		err    error
	}
	results := make(chan reservation, 2)
	start := make(chan struct{})
	for _, client := range []*redis.Client{first, second} {
		go func() {
			<-start
			values, err := client.Eval(ctx, reserveBedrockUsageScript,
				[]string{key, modelKey}, 40, 1, 100, 1, 100, 0).Int64Slice()
			results <- reservation{values, err}
		}()
	}
	close(start)
	accepted := int64(0)
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.Len(t, result.values, 4)
		accepted += result.values[0]
	}
	assert.EqualValues(t, 1, accepted, "instances must share one atomic capacity reservation")
	for _, counterKey := range []string{key, modelKey} {
		values, err := first.HMGet(ctx, counterKey, "rpm", "tpm").Result()
		require.NoError(t, err)
		assert.Equal(t, []any{"1", "40"}, values)
	}

	// A lost pipeline reply may replay after another instance has written.
	for _, step := range []struct {
		client *redis.Client
		writer string
		want   []int64
	}{
		{first, "instance-a", []int64{1, 25, 0, 0}},
		{second, "instance-b", []int64{2, 50, 0, 0}},
		{first, "instance-a", []int64{2, 50, 0, 0}},
		{second, "instance-b", []int64{2, 50, 0, 0}},
	} {
		values, err := step.client.Eval(ctx, recordBedrockActualUsageScript,
			[]string{actualKey}, 1, 25, 0, 0, step.writer, "batch-1").Int64Slice()
		require.NoError(t, err)
		assert.Equal(t, step.want, values)
	}
	ttl, err := first.TTL(ctx, actualKey).Result()
	require.NoError(t, err)
	assert.Positive(t, ttl, "statistics must expire without future requests")
}

func TestBedrockUsageIsUnavailableAfterMetricsQueueOverflowUntilWindowExpires(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	db := setupChannelSelectAutoGroupsTest(t)
	channel := &model.Channel{Id: 97, Type: constant.ChannelTypeAws}
	require.NoError(t, db.Create(channel).Error)
	model.InitChannelCache()
	previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
	RecordBedrockActualAttempt(97)
	FlushBedrockActualUsage()
	markBedrockActualUsageDropped(97)
	FlushBedrockActualUsage()
	usage := GetBedrockUsage([]int{97})[97]
	assert.False(t, usage.Available, "lost events must not be reported as exact RPM/TPM")
	// A queue overflow can only be cleared after the whole Redis key expires.
	server.FastForward(121 * time.Second)
	assert.Empty(t, server.HGet("bedrock:actual-usage:v1:{97}", "invalid"))
	assert.Equal(t, BedrockUsage{Available: true}, GetBedrockUsage([]int{97})[97])

	// A failed Redis write must stay visibly incomplete after Redis recovers,
	// while subsequent successful writes still contribute authoritative usage.
	server.Del("bedrock:actual-usage:v1:{97}")
	require.NoError(t, server.Set("bedrock:actual-usage:v1:{97}", "wrong-type"))
	RecordBedrockActualAttempt(97)
	FlushBedrockActualUsage()
	server.Del("bedrock:actual-usage:v1:{97}")
	RecordBedrockActualAttempt(97)
	RecordBedrockActualTokens(97, 25, true)
	FlushBedrockActualUsage()
	usage = GetBedrockUsage([]int{97})[97]
	assert.False(t, usage.Available, "recovery must not hide lost statistics")
	assert.Equal(t, int64(1), usage.RPM)
	assert.Equal(t, int64(25), usage.TPM)
	server.FastForward(121 * time.Second)
	assert.Equal(t, BedrockUsage{Available: true}, GetBedrockUsage([]int{97})[97])
}

func TestBedrockMetricsFailureIsIsolatedToAffectedChannel(t *testing.T) {
	server := useBedrockProtectionRedis(t)
	db := setupChannelSelectAutoGroupsTest(t)
	for _, id := range []int{103, 104} {
		require.NoError(t, db.Create(&model.Channel{Id: id, Type: constant.ChannelTypeAws}).Error)
	}
	model.InitChannelCache()
	previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
	require.NoError(t, server.Set(bedrockActualUsageKey(103), "wrong-type"))
	// Both channels must be in the same batch, even if the worker wakes up.
	bedrockActualUsageFlushMu.Lock()
	RecordBedrockActualAttempt(103)
	RecordBedrockActualAttempt(104)
	RecordBedrockActualTokens(104, 25, true)
	bedrockActualUsageFlushMu.Unlock()
	FlushBedrockActualUsage()
	// Persist pending failure markers; a healthy channel must not acquire one.
	FlushBedrockActualUsage()

	usage := GetBedrockUsage([]int{103, 104})
	assert.NotContains(t, usage, 103, "the failing channel must not report exact counters")
	assert.Equal(t, BedrockUsage{RPM: 1, TPM: 25, Available: true}, usage[104], "one Redis command error must not hide healthy channels in the same batch")

	server.Del(bedrockActualUsageKey(103))
	FlushBedrockActualUsage()
	usage = GetBedrockUsage([]int{103, 104})
	assert.False(t, usage[103].Available, "recovery must retain the affected channel's lost-data marker")
	assert.Equal(t, BedrockUsage{RPM: 1, TPM: 25, Available: true}, usage[104])
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

	// Affinity fallback and a capacity rejection both consume attempts without
	// skipping another healthy channel at the highest priority.
	fallback, _ := gin.CreateTestContext(httptest.NewRecorder())
	fallback.Set(ginKeyChannelAffinityRetryOffset, 1)
	fallbackParam := &RetryParam{Ctx: fallback, TokenGroup: "default", ModelName: "capacity-model"}
	fallbackParam.SetRetry(1)
	preferred, _, err := CacheGetRandomSatisfiedChannel(fallbackParam)
	require.NoError(t, err)
	require.NotNil(t, preferred)
	assert.Contains(t, []int{71, 72}, preferred.Id)
	fallbackParam.ExcludeCapacityRejectedChannel(preferred.Id)
	fallbackParam.IncreaseRetry()
	peer, _, err := CacheGetRandomSatisfiedChannel(fallbackParam)
	require.NoError(t, err)
	require.NotNil(t, peer)
	assert.Contains(t, []int{71, 72}, peer.Id)
	assert.NotEqual(t, preferred.Id, peer.Id)
	assert.Equal(t, 2, fallbackParam.GetRetry(), "the combined offsets must not grant extra attempts")
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
