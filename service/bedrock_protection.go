package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/go-redis/redis/v8"
)

type BedrockUsage struct {
	RPM            int64 `json:"rpm"`
	TPM            int64 `json:"tpm"`
	Available      bool  `json:"available"`
	TPMUnavailable bool  `json:"tpm_unavailable,omitempty"`
}

var lastBedrockProtectionRedisWarning atomic.Int64

const reserveBedrockUsageScript = `
local now = tonumber(redis.call('TIME')[1])
-- Keep the boundary second in full: a request is never forgotten before
-- 60 seconds. Each channel/model uses at most 61 second buckets.
local function usage(key)
  local values = redis.call('HMGET', key, 'rpm', 'tpm', 'last', 'unknown')
  local rpm = tonumber(values[1] or '0')
  local tpm = tonumber(values[2] or '0')
  local last = tonumber(values[3] or now)
  local unknown = tonumber(values[4] or '0')
  for second = last - 60, math.min(now - 61, last) do
    local field = tostring(second)
    local expired = redis.call('HMGET', key, field .. ':r', field .. ':t', field .. ':u')
    rpm = rpm - tonumber(expired[1] or '0')
    tpm = tpm - tonumber(expired[2] or '0')
    unknown = unknown - tonumber(expired[3] or '0')
    redis.call('HDEL', key, field .. ':r', field .. ':t', field .. ':u')
  end
  redis.call('HSET', key, 'rpm', rpm, 'tpm', tpm, 'last', now, 'unknown', unknown)
  redis.call('EXPIRE', key, 120)
  return rpm, tpm, unknown
end
local function reserve(key, tokens)
  redis.call('HINCRBY', key, 'rpm', 1)
  redis.call('HINCRBY', key, 'tpm', tokens)
  redis.call('HINCRBY', key, tostring(now) .. ':r', 1)
  redis.call('HINCRBY', key, tostring(now) .. ':t', tokens)
  if ARGV[6] == '1' then
    redis.call('HINCRBY', key, 'unknown', 1)
    redis.call('HINCRBY', key, tostring(now) .. ':u', 1)
  end
end
local rpm, tpm, unknown = usage(KEYS[1])
local add_tokens = tonumber(ARGV[1])
if add_tokens < 0 then return {1, rpm, tpm, unknown} end
local rpm_limit = tonumber(ARGV[2])
local tpm_limit = tonumber(ARGV[3])
if #KEYS == 2 then
  local model_rpm, model_tpm, model_unknown = usage(KEYS[2])
  local model_rpm_limit = tonumber(ARGV[4])
  local model_tpm_limit = tonumber(ARGV[5])
  if (model_rpm_limit > 0 and model_rpm + 1 > model_rpm_limit) or (model_tpm_limit > 0 and (model_unknown > 0 or ARGV[6] == '1' or model_tpm + add_tokens > model_tpm_limit)) then
    return {0, rpm, tpm, unknown}
  end
end
if (rpm_limit > 0 and rpm + 1 > rpm_limit) or (tpm_limit > 0 and (unknown > 0 or ARGV[6] == '1' or tpm + add_tokens > tpm_limit)) then
  return {0, rpm, tpm, unknown}
end
reserve(KEYS[1], add_tokens)
if #KEYS == 2 then reserve(KEYS[2], add_tokens) end
return {1, rpm + 1, tpm + add_tokens, unknown + tonumber(ARGV[6])}
`

// BedrockRampPercent scales configured ceilings, never channel selection weights.
// The last ramp day reaches 100% of the administrator's configured safe ceiling.
func BedrockRampPercent(settings *dto.BedrockProtectionSettings, now time.Time) int {
	if settings == nil || !settings.RampUpEnabled {
		return 100
	}
	days, initial := settings.RampUpDays, settings.RampUpInitialPercent
	if days == 0 {
		days = 7
	}
	if initial == 0 {
		initial = 5
	}
	if days <= 1 {
		return 100
	}
	elapsed := max(int64(0), now.Unix()-settings.RampUpStartUnix) / 86400
	if elapsed >= int64(days-1) {
		return 100
	}
	return initial + int(elapsed)*(100-initial)/(days-1)
}

// BedrockRetryDelay uses capped exponential full jitter. It is separate from the
// retry decision: enabling it does not grant extra attempts.
func BedrockRetryDelay(settings *dto.BedrockProtectionSettings, attempt int) time.Duration {
	if settings == nil || !settings.RetryBackoffEnabled {
		return 0
	}
	baseMS, maxMS := settings.RetryBackoffBaseMS, settings.RetryBackoffMaxMS
	if baseMS <= 0 {
		baseMS = 200
	}
	if maxMS <= 0 {
		maxMS = 2000
	}
	maxMS = min(maxMS, 10000)
	ceiling := min(int64(baseMS), int64(maxMS))
	for range min(max(attempt, 0), 14) {
		ceiling = min(ceiling*2, int64(maxMS))
	}
	return time.Duration(rand.Int64N(ceiling*int64(time.Millisecond) + 1))
}

func WaitBedrockRetry(ctx context.Context, settings *dto.BedrockProtectionSettings, attempt int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := BedrockRetryDelay(settings, attempt)
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func bedrockUsageKey(channelID int) string {
	// Keep channel and model counters in the same Redis Cluster hash slot.
	return fmt.Sprintf("bedrock:usage:v2:{%d}", channelID)
}

func bedrockActualUsageKey(channelID int) string {
	return fmt.Sprintf("bedrock:actual-usage:v1:{%d}", channelID)
}

const recordBedrockActualUsageScript = `
local now = tonumber(redis.call('TIME')[1])
local key = KEYS[1]
local last = tonumber(redis.call('HGET', key, 'last') or now)
local rpm = tonumber(redis.call('HGET', key, 'rpm') or '0')
local tpm = tonumber(redis.call('HGET', key, 'tpm') or '0')
local unknown = tonumber(redis.call('HGET', key, 'unknown') or '0')
local invalid = tonumber(redis.call('HGET', key, 'invalid') or '0')
for second = last - 60, math.min(now - 61, last) do
  local field = tostring(second)
  local expired = redis.call('HMGET', key, field .. ':r', field .. ':t', field .. ':u', field .. ':i')
  unknown = unknown - tonumber(expired[3] or '0')
  invalid = invalid - tonumber(expired[4] or '0')
  rpm = rpm - tonumber(expired[1] or '0')
  tpm = tpm - tonumber(expired[2] or '0')
  redis.call('HDEL', key, field .. ':r', field .. ':t', field .. ':u', field .. ':i')
end
local add_rpm = tonumber(ARGV[1])
local add_tpm = tonumber(ARGV[2])
-- A Redis pipeline may be retried after AWS metrics were already persisted
-- but its response was lost. Keep the most recent batch per process, rather
-- than a single channel-wide batch ID, because instances write concurrently.
local writer_field = nil
local duplicate = false
if ARGV[5] and ARGV[5] ~= '' then
  writer_field = 'b:' .. ARGV[5]
  local previous = redis.call('HGET', key, writer_field)
  duplicate = previous and string.match(previous, '^([^:]+):') == ARGV[6]
  -- Remove metadata belonging to stopped processes, even on busy channels
  -- whose statistics key is continually refreshed and never expires.
  local last_gc = tonumber(redis.call('HGET', key, 'batch_gc') or '0')
  if now - last_gc >= 60 then
    local fields = redis.call('HGETALL', key)
    for i = 1, #fields, 2 do
      if string.sub(fields[i], 1, 2) == 'b:' then
        local seen = tonumber(string.match(fields[i + 1], ':(%d+)$') or '0')
        if now - seen > 120 then redis.call('HDEL', key, fields[i]) end
      end
    end
    redis.call('HSET', key, 'batch_gc', now)
  end
end
if duplicate then
  add_rpm = 0
  add_tpm = 0
end
if add_rpm > 0 then
  redis.call('HINCRBY', key, tostring(now) .. ':r', add_rpm)
  rpm = rpm + add_rpm
end
if add_tpm > 0 then
  redis.call('HINCRBY', key, tostring(now) .. ':t', add_tpm)
  tpm = tpm + add_tpm
end
if not duplicate and ARGV[3] == '1' then
  unknown = unknown + 1
  redis.call('HINCRBY', key, tostring(now) .. ':u', 1)
end
if not duplicate and ARGV[4] == '1' then
  invalid = invalid + 1
  redis.call('HINCRBY', key, tostring(now) .. ':i', 1)
end
if writer_field and not duplicate then
  redis.call('HSET', key, writer_field, ARGV[6] .. ':' .. tostring(now))
end
redis.call('HSET', key, 'rpm', rpm, 'tpm', tpm, 'last', now, 'unknown', unknown, 'invalid', invalid)
redis.call('EXPIRE', key, 120)
return {rpm, tpm, unknown, invalid}
`

type bedrockActualUsageEvent struct {
	channelID int
	rpm       int
	tpm       int
	unknown   bool
}

type bedrockActualUsageBatch struct {
	rpm, tpm int
	unknown  bool
	invalid  bool
}

const bedrockActualUsageQueueSize = 32768

var (
	bedrockActualUsageQueue    = make(chan bedrockActualUsageEvent, bedrockActualUsageQueueSize)
	bedrockActualUsageDropped  = make(map[int]struct{})
	bedrockActualUsageDropMu   sync.Mutex
	bedrockActualUsageWorker   sync.Once
	bedrockActualUsageFlushMu  sync.Mutex
	bedrockActualUsageWriterID = common.GetUUID()
)

// RecordBedrockActualAttempt counts one HTTP attempt sent to AWS. It is kept
// separate from limiter reservations, which remain conservative estimates.
func RecordBedrockActualAttempt(channelID int) {
	recordBedrockActualUsage(channelID, 1, 0, false)
}

// RecordBedrockActualTokens records only authoritative upstream token usage.
// Missing usage marks TPM unavailable instead of substituting an estimate.
func RecordBedrockActualTokens(channelID, tokens int, known bool) {
	if !known {
		tokens = 0
	}
	recordBedrockActualUsage(channelID, 0, max(tokens, 0), !known)
}

func recordBedrockActualUsage(channelID, rpm, tpm int, unknown bool) {
	// Statistics are optional. When Redis is unavailable, skip the event at
	// the producer boundary instead of filling the queue with data that cannot
	// be persisted; request forwarding remains fail-open.
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	bedrockActualUsageWorker.Do(func() { go runBedrockActualUsageWorker() })
	select {
	case bedrockActualUsageQueue <- bedrockActualUsageEvent{channelID: channelID, rpm: rpm, tpm: tpm, unknown: unknown}:
	default:
		markBedrockActualUsageDropped(channelID)
	}
}

func markBedrockActualUsageDropped(channelID int) {
	bedrockActualUsageDropMu.Lock()
	bedrockActualUsageDropped[channelID] = struct{}{}
	bedrockActualUsageDropMu.Unlock()
}

func runBedrockActualUsageWorker() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		flushBedrockActualUsageBatch()
	}
}

func flushBedrockActualUsageBatch() {
	bedrockActualUsageFlushMu.Lock()
	defer bedrockActualUsageFlushMu.Unlock()
	batch := make(map[int]bedrockActualUsageBatch)
	for range bedrockActualUsageQueueSize {
		select {
		case event := <-bedrockActualUsageQueue:
			current := batch[event.channelID]
			current.rpm += event.rpm
			current.tpm += event.tpm
			current.unknown = current.unknown || event.unknown
			batch[event.channelID] = current
		default:
			goto drained
		}
	}

drained:
	bedrockActualUsageDropMu.Lock()
	dropped := bedrockActualUsageDropped
	if len(dropped) > 0 {
		bedrockActualUsageDropped = make(map[int]struct{})
	} else {
		dropped = nil
	}
	bedrockActualUsageDropMu.Unlock()
	for channelID := range dropped {
		current := batch[channelID]
		current.invalid = true
		batch[channelID] = current
	}
	if len(batch) == 0 {
		return
	}
	if !common.RedisEnabled || common.RDB == nil {
		for channelID := range batch {
			markBedrockActualUsageDropped(channelID)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	pipe := common.RDB.Pipeline()
	commands := make(map[int]*redis.Cmd, len(batch))
	batchID := common.GetUUID()
	for channelID, value := range batch {
		missing, invalid := 0, 0
		if value.unknown {
			missing = 1
		}
		if value.invalid {
			invalid = 1
		}
		commands[channelID] = pipe.Eval(ctx, recordBedrockActualUsageScript, []string{bedrockActualUsageKey(channelID)}, value.rpm, value.tpm, missing, invalid, bedrockActualUsageWriterID, batchID)
	}
	// go-redis attaches transport failures to the affected commands. A Redis
	// command error alone does not invalidate successful replies in the batch.
	_, _ = pipe.Exec(ctx)
	for channelID, command := range commands {
		values, commandErr := command.Int64Slice()
		if commandErr == nil && len(values) != 4 {
			commandErr = fmt.Errorf("unexpected Bedrock usage result")
		}
		if commandErr != nil {
			warnBedrockProtectionRedis(commandErr)
			markBedrockActualUsageDropped(channelID)
		}
	}
}

// FlushBedrockActualUsage persists queued statistics before a controlled shutdown.
func FlushBedrockActualUsage() {
	flushBedrockActualUsageBatch()
}

func warnBedrockProtectionRedis(err error) {
	now := time.Now().Unix()
	last := lastBedrockProtectionRedisWarning.Load()
	if now-last < 60 || !lastBedrockProtectionRedisWarning.CompareAndSwap(last, now) {
		return
	}
	common.SysLog(common.LogText("Bedrock protection Redis unavailable; request allowed: %s", err.Error()))
}

// ReserveBedrockUsage atomically reserves one request. Redis failures fail open
// so an optional protection mechanism cannot become a customer-facing outage.
// Negative tokens record an unknown estimate: RPM remains usable, TPM is
// unavailable until that request expires, and active TPM ceilings reject it.
func ReserveBedrockUsage(channelID, tokens int, settings *dto.BedrockProtectionSettings, modelName string) (bool, BedrockUsage) {
	if settings == nil {
		return true, BedrockUsage{}
	}
	limitEnabled := settings.RateLimitEnabled && (settings.RPM > 0 || settings.TPM > 0)
	modelLimit, modelConfigured := settings.ModelLimits[modelName]
	modelEnabled := settings.ModelRateLimitEnabled && modelConfigured && (modelLimit.RPM > 0 || modelLimit.TPM > 0)
	collectingModelUsage := modelEnabled
	if settings.ModelRateLimitEnabled && !collectingModelUsage {
		for _, limit := range settings.ModelLimits {
			if limit.RPM > 0 || limit.TPM > 0 {
				collectingModelUsage = true
				break
			}
		}
	}
	if !limitEnabled && !collectingModelUsage {
		return true, BedrockUsage{}
	}
	if !common.RedisEnabled || common.RDB == nil {
		warnBedrockProtectionRedis(fmt.Errorf("Redis is disabled"))
		return true, BedrockUsage{}
	}
	unknown := 0
	if tokens < 0 {
		tokens = 0
		unknown = 1
	}
	now := time.Now()
	usageKey := bedrockUsageKey(channelID)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rpmLimit, tpmLimit := 0, 0
	if limitEnabled {
		rpmLimit, tpmLimit = settings.RPM, settings.TPM
	}
	percent := BedrockRampPercent(settings, now)
	if percent < 100 {
		if rpmLimit > 0 {
			rpmLimit = max(1, rpmLimit*percent/100)
		}
		if tpmLimit > 0 {
			tpmLimit = max(1, tpmLimit*percent/100)
		}
		if modelLimit.RPM > 0 {
			modelLimit.RPM = max(1, modelLimit.RPM*percent/100)
		}
		if modelLimit.TPM > 0 {
			modelLimit.TPM = max(1, modelLimit.TPM*percent/100)
		}
	}
	keys := []string{usageKey}
	if modelEnabled {
		// Bound key size and avoid model-name delimiters influencing Redis keys.
		keys = append(keys, fmt.Sprintf("%s:model:%x", usageKey, sha256.Sum256([]byte(modelName))))
	}
	result, err := common.RDB.Eval(ctx, reserveBedrockUsageScript, keys, tokens, rpmLimit, tpmLimit, modelLimit.RPM, modelLimit.TPM, unknown).Int64Slice()
	if err != nil || len(result) != 4 {
		if err == nil {
			err = fmt.Errorf("unexpected limiter result")
		}
		warnBedrockProtectionRedis(err)
		return true, BedrockUsage{}
	}
	return result[0] == 1, BedrockUsage{RPM: result[1], TPM: result[2], Available: true, TPMUnavailable: result[3] > 0}
}

func GetBedrockUsage(channelIDs []int) map[int]BedrockUsage {
	result := make(map[int]BedrockUsage, len(channelIDs))
	if !model_setting.IsAwsUsageMetricsEnabled() || !common.RedisEnabled || common.RDB == nil || len(channelIDs) == 0 {
		return result
	}
	// Reuse channel snapshots; with the cache disabled, load configuration in
	// one existing batch query rather than querying once per displayed row.
	var channels []*model.Channel
	if common.MemoryCacheEnabled {
		for _, id := range channelIDs {
			channel, err := model.CacheGetChannel(id)
			if err == nil {
				channels = append(channels, channel)
			}
		}
	} else {
		var err error
		channels, err = model.GetChannelsByIds(channelIDs)
		if err != nil {
			return result
		}
	}
	eligibleIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeAws {
			continue
		}
		eligibleIDs = append(eligibleIDs, channel.Id)
	}
	if len(eligibleIDs) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	pipe := common.RDB.Pipeline()
	commandByID := make(map[int]*redis.Cmd, len(channelIDs))
	for _, id := range eligibleIDs {
		// The display path must not reserve usage, but the script still requires
		// its complete argument vector to read and return the current counters.
		commandByID[id] = pipe.Eval(ctx, recordBedrockActualUsageScript, []string{bedrockActualUsageKey(id)}, 0, 0, 0, 0)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		warnBedrockProtectionRedis(err)
	}
	for id, command := range commandByID {
		values, err := command.Int64Slice()
		if err != nil || len(values) != 4 {
			continue
		}
		result[id] = BedrockUsage{RPM: values[0], TPM: values[1], Available: values[3] == 0, TPMUnavailable: values[2] > 0}
	}
	return result
}

func bedrockCooldownKey(channelID int) string { return "bedrock:cooldown:" + strconv.Itoa(channelID) }
func bedrockResultKey(channelID int) string   { return "bedrock:result:" + strconv.Itoa(channelID) }
func bedrock429Key(channelID int) string      { return "bedrock:429:" + strconv.Itoa(channelID) }
func bedrockStreakKey(channelID int) string   { return "bedrock:429-streak:" + strconv.Itoa(channelID) }

const recordBedrockResultScript = `
local cutoff = tonumber(ARGV[1]) - tonumber(ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', cutoff)
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])

local consecutive_ready = false
if ARGV[3] == '1' then
  redis.call('ZADD', KEYS[2], ARGV[1], ARGV[2])
  local consecutive = redis.call('HINCRBY', KEYS[3], 'count', 1)
  if consecutive == 1 then
    redis.call('HSET', KEYS[3], 'first', ARGV[1])
  end
  local first = tonumber(redis.call('HGET', KEYS[3], 'first') or ARGV[1])
  if first < cutoff then
    consecutive = 1
    first = tonumber(ARGV[1])
    redis.call('HSET', KEYS[3], 'count', consecutive, 'first', first)
  end
  consecutive_ready = consecutive >= tonumber(ARGV[5]) and tonumber(ARGV[1]) - first >= tonumber(ARGV[6])
else
  redis.call('DEL', KEYS[3])
end

local total = redis.call('ZCARD', KEYS[1])
local throttled = redis.call('ZCARD', KEYS[2])
local ratio_ready = total >= tonumber(ARGV[7]) and throttled * 100 >= total * tonumber(ARGV[8])
local ttl = math.ceil(tonumber(ARGV[4]) / 1000) + 5
redis.call('EXPIRE', KEYS[1], ttl)
redis.call('EXPIRE', KEYS[2], ttl)
redis.call('EXPIRE', KEYS[3], ttl)

if ARGV[3] == '1' and (consecutive_ready or ratio_ready) then
  redis.call('SET', KEYS[4], '1', 'EX', ARGV[9])
  return 1
end
return 0
`

func IsBedrockCoolingDown(channelID int, settings *dto.BedrockProtectionSettings) bool {
	if settings == nil || !settings.ThrottlingCooldownEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	exists, err := common.RDB.Exists(ctx, bedrockCooldownKey(channelID)).Result()
	if err != nil {
		warnBedrockProtectionRedis(err)
		return false
	}
	return exists > 0
}

func recordBedrockResult(channelID int, throttled bool, settings *dto.BedrockProtectionSettings) {
	if settings == nil || !settings.ThrottlingCooldownEnabled || !common.RedisEnabled || common.RDB == nil {
		return
	}
	threshold := settings.Consecutive429Threshold
	if threshold == 0 {
		threshold = 3
	}
	window := settings.ObservationWindowSeconds
	if window == 0 {
		window = 60
	}
	minimumSpan := settings.Minimum429SpanSeconds
	if minimumSpan == 0 {
		minimumSpan = 1
	}
	minimumSamples := settings.MinimumSampleSize
	if minimumSamples == 0 {
		minimumSamples = 20
	}
	ratioPercent := settings.ThrottlingRatioPercent
	if ratioPercent == 0 {
		ratioPercent = 50
	}
	cooldown := settings.CooldownSeconds
	if cooldown == 0 {
		cooldown = 900
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	isThrottled := 0
	if throttled {
		isThrottled = 1
	}
	member := fmt.Sprintf("%d:%d", now.UnixMilli(), now.UnixNano())
	_, err := common.RDB.Eval(ctx, recordBedrockResultScript, []string{
		bedrockResultKey(channelID),
		bedrock429Key(channelID),
		bedrockStreakKey(channelID),
		bedrockCooldownKey(channelID),
	}, now.UnixMilli(), member, isThrottled, window*1000, threshold, minimumSpan*1000, minimumSamples, ratioPercent, cooldown).Int()
	if err != nil {
		warnBedrockProtectionRedis(err)
	}
}

func RecordBedrockThrottle(channelID int, settings *dto.BedrockProtectionSettings) {
	recordBedrockResult(channelID, true, settings)
}

func RecordBedrockNonThrottle(channelID int, settings *dto.BedrockProtectionSettings) {
	recordBedrockResult(channelID, false, settings)
}
