package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/alicebob/miniredis/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const awsTestModel = "anthropic.claude-3-5-sonnet-20240620-v1:0"

func TestAwsPassthroughCompatibilityPreservesNativeFieldsAndMappedOperation(t *testing.T) {
	const input = `{"model":"client-alias","stream":true,"max_tokens":128,"temperature":0,"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","future":false},"future":9007199254740993}]}],"future":{"zero":0,"enabled":false,"nullable":null},"anthropic_beta":["existing"]}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			c.Request.Header.Set("anthropic-beta", "existing, extra")
			info := newAwsTestRelayInfo()
			info.ApiKey = "access-key|secret-key|us-east-1"
			info.IsStream = stream
			info.AwsPassthroughCompatibility = true
			for range 2 { // Retry uses a fresh reader and the same mapped model.
				a := &Adaptor{}
				_, err := doAwsClientRequest(c, info, a, strings.NewReader(input))
				require.NoError(t, err)
				var body []byte
				var modelID string
				switch request := a.AwsReq.(type) {
				case *bedrockruntime.InvokeModelInput:
					require.False(t, stream)
					body, modelID = request.Body, aws.ToString(request.ModelId)
				case *bedrockruntime.InvokeModelWithResponseStreamInput:
					require.True(t, stream)
					body, modelID = request.Body, aws.ToString(request.ModelId)
				default:
					t.Fatalf("unexpected operation %T", a.AwsReq)
				}
				assert.Equal(t, "us."+awsTestModel, modelID)
				var payload map[string]json.RawMessage
				require.NoError(t, common.Unmarshal(body, &payload))
				assert.NotContains(t, payload, "model")
				assert.NotContains(t, payload, "stream")
				assert.JSONEq(t, `{"zero":0,"enabled":false,"nullable":null}`, string(payload["future"]))
				assert.Contains(t, string(payload["messages"]), "9007199254740993")
				assert.JSONEq(t, `["existing","extra"]`, string(payload["anthropic_beta"]))
				assert.Equal(t, `"bedrock-2023-05-31"`, string(payload["anthropic_version"]))
				assert.Equal(t, "0", string(payload["temperature"]))
			}
		})
	}
}

func TestAwsCompatibilityNeverRestoresUnconvertedMessages(t *testing.T) {
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	c.Request.Body = io.NopCloser(strings.NewReader(`{"messages":[{"role":"user","content":"original"}],"max_tokens":999,"anthropic_beta":["extension"],"metadata":{"user_id":"example"}}`))
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	info := newAwsTestRelayInfo()
	info.AwsCompatibilityConverted = true
	body, err := prepareAwsCompatibilityBody(c, info, strings.NewReader(`{"max_tokens":128,"messages":[{"role":"user","content":"converted"}]}`), http.Header{}, false)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(body, &payload))
	assert.Equal(t, "128", string(payload["max_tokens"]))
	assert.Contains(t, string(payload["messages"]), "converted")
	assert.NotContains(t, string(payload["messages"]), "original")
	assert.JSONEq(t, `["extension"]`, string(payload["anthropic_beta"]))
	assert.JSONEq(t, `{"user_id":"example"}`, string(payload["metadata"]))
}

func TestAwsCompatibilityInvalidBodiesReturnNonRetryable400(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{"messages":[],"max_tokens":-1}`, `{"messages":[],"max_tokens":18446744073709551615}`, `{"messages":[],"max_tokens":1,"anthropic_beta":"invalid"}`} {
		t.Run(input, func(t *testing.T) {
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			info := newAwsTestRelayInfo()
			info.ApiKey = "access-key|secret-key|us-east-1"
			info.AwsPassthroughCompatibility = true
			_, err := doAwsClientRequest(c, info, &Adaptor{}, strings.NewReader(input))
			var apiErr *relaytypes.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, relaytypes.IsSkipRetryError(apiErr))
		})
	}
}

func TestBedrockEstimationPreservesRequestCacheOwnership(t *testing.T) {
	previousConfig := common.GetDiskCacheConfig()
	previousMedia, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	common.SetDiskCacheConfig(common.DiskCacheConfig{Enabled: true, ThresholdMB: 0, MaxSizeMB: 100, Path: t.TempDir(), CriticalWatermarkPercent: 90})
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() {
		common.SetDiskCacheConfig(previousConfig)
		constant.GetMediaToken, constant.GetMediaTokenNotStream = previousMedia, previousNonStream
	})
	const gif = "R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="
	for _, tc := range []struct {
		name    string
		shared  bool
		invalid bool
		stream  bool
		retry   bool
	}{
		{name: "new cache"},
		{name: "stream new cache", stream: true},
		{name: "shared cache", shared: true},
		{name: "estimation failure", invalid: true},
		{name: "failure with shared cache", shared: true, invalid: true},
		{name: "retry with shared cache", shared: true, retry: true},
		{name: "retry after estimation failure", shared: true, invalid: true, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "billing-model")
			common.SetContextKey(c, constant.ContextKeyPromptTokens, 321)
			t.Cleanup(func() {
				service.CleanupFileSources(c)
				count, _, err := common.GetDiskCacheInfo()
				require.NoError(t, err)
				assert.Zero(t, count, "request completion must clean estimation files even on failure")
			})
			var shared *relaytypes.CachedFileData
			var unrelated *relaytypes.CachedFileData
			if tc.shared {
				var err error
				shared, err = service.LoadFileSource(c, relaytypes.NewBase64FileSource(gif, "image/gif"))
				require.NoError(t, err)
				unrelated, err = service.LoadFileSource(c, relaytypes.NewBase64FileSource("b3JpZ2luYWw=", "text/plain"))
				require.NoError(t, err)
			}
			content := fmt.Sprintf(`{"type":"image","source":{"type":"base64","media_type":"image/gif","data":%q}}`, gif)
			if tc.invalid {
				content += `,{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW52YWxpZA=="}}`
			}
			body := []byte(`{"max_tokens":128,"messages":[{"role":"user","content":[` + content + `]}]}`)
			info := newAwsTestRelayInfo()
			info.IsStream = tc.stream
			a := &Adaptor{AwsReq: &bedrockruntime.InvokeModelInput{Body: body}}
			if tc.stream {
				a.AwsReq = &bedrockruntime.InvokeModelWithResponseStreamInput{Body: body}
			}
			attempts := 1
			if tc.retry {
				attempts = 2
			}
			for range attempts {
				_, err := a.estimateProtectionTokens(c, info)
				if tc.invalid {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, "billing-model", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
				assert.Equal(t, 321, common.GetContextKeyInt(c, constant.ContextKeyPromptTokens))
			}
			if shared != nil {
				data, err := shared.GetBase64Data()
				require.NoError(t, err, "estimation must not close the original request's cache")
				assert.Equal(t, gif, data)
				data, err = unrelated.GetBase64Data()
				require.NoError(t, err, "unrelated original cache must survive estimation")
				assert.Equal(t, "b3JpZ2luYWw=", data)
			}
			_, err := service.LoadFileSource(c, relaytypes.NewBase64FileSource(gif, "image/gif"))
			require.NoError(t, err, "the original request must still load media after estimation")
		})
	}
}

func TestBedrockOptionalEstimationDoesNotBlockRequests(t *testing.T) {
	previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	previousMedia, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
		constant.GetMediaToken, constant.GetMediaTokenNotStream = previousMedia, previousNonStream
	})
	for _, tc := range []struct {
		name     string
		settings dto.BedrockProtectionSettings
		blocked  bool
	}{
		{"metrics", dto.BedrockProtectionSettings{UsageMetricsEnabled: true}, false},
		{"metrics and RPM", dto.BedrockProtectionSettings{UsageMetricsEnabled: true, RateLimitEnabled: true, RPM: 1}, false},
		{"RPM only", dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 1}, false},
		{"other model TPM", dto.BedrockProtectionSettings{ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{"other": {TPM: 100}}}, false},
		{"channel TPM", dto.BedrockProtectionSettings{RateLimitEnabled: true, TPM: 1000}, true},
		{"model TPM", dto.BedrockProtectionSettings{ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{awsTestModel: {TPM: 1000}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server.FlushAll()
			info := newAwsTestRelayInfo()
			info.IsStream, info.ChannelId = false, 98
			info.ChannelSetting.BedrockProtection = &tc.settings
			calls := 0
			a := &Adaptor{AwsClient: newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				// The upstream owns payload validation; optional metrics must not
				// replace its response with a local image decoding failure.
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"message":"invalid image"}`)), Request: request}, nil
			})), AwsReq: &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsTestModel), Body: []byte(`{"max_tokens":128,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW52YWxpZA=="}}]}]}`)}}
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			_, estimateErr := a.estimateProtectionTokens(c, info)
			require.Error(t, estimateErr)
			protectionErr := bedrockProtectionError(c, info, a)
			if tc.blocked {
				require.NotNil(t, protectionErr)
				assert.Equal(t, relaytypes.ErrorCodeBadRequestBody, protectionErr.GetErrorCode())
				assert.Empty(t, server.Keys())
				return
			}
			require.Nil(t, protectionErr)
			_, err := a.AwsClient.InvokeModel(t.Context(), a.AwsReq.(*bedrockruntime.InvokeModelInput))
			require.Error(t, err)
			service.FlushBedrockActualUsage()
			assert.Equal(t, 1, calls)
			assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{98}", "rpm"))
			if tc.settings.RateLimitEnabled {
				require.NotNil(t, bedrockProtectionError(c, info, a), "unknown TPM must not bypass RPM protection")
			}
		})
	}
}

func TestBedrockHeadersAndPhysicalAttemptMetrics(t *testing.T) {
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = redisClient, true
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	for _, key := range []string{"test-key|us-east-1", "access-key|secret-key|us-east-1"} {
		for _, mode := range []string{"off", "metrics", "protection", "compatibility"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", key, mode, stream), func(t *testing.T) {
					service.FlushBedrockActualUsage()
					server.FlushAll()
					previous := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
					model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = mode != "off"
					t.Cleanup(func() { model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previous })
					info := newAwsTestRelayInfo()
					info.ApiKey, info.ChannelId, info.IsStream = key, 97, stream
					info.AwsPassthroughCompatibility = mode == "compatibility"
					info.UseRuntimeHeadersOverride = true
					info.RuntimeHeadersOverride = map[string]any{
						"X-Channel-Tag": "custom-value", "Authorization": "invalid", "X-Amz-Date": "invalid", "Host": "invalid",
						"X-Forwarded-For": "10.0.0.1", "X-Forwarded-Proto": "https", "X-Real-IP": "10.0.0.1", "Forwarded": "for=10.0.0.1",
						"x-amzn-bedrock-trace": "DISABLED",
					}
					if mode == "protection" {
						info.ChannelSetting.BedrockProtection = &dto.BedrockProtectionSettings{RateLimitEnabled: true, RPM: 100}
					}
					c := newAwsTestContext(httptest.NewRecorder(), t.Context())
					c.Request.Header.Set("X-Amzn-Bedrock-Trace", "ENABLED")
					c.Request.Header.Set("X-Amzn-Bedrock-GuardrailIdentifier", "test-guardrail")
					c.Request.Header.Set("X-Client-Secret", "must-not-forward")
					a := &Adaptor{}
					_, err := a.DoRequest(c, info, strings.NewReader(`{"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`))
					require.NoError(t, err)

					if mode != "off" {
						_, err = a.estimateProtectionTokens(c, info)
						require.NoError(t, err)
					}
					attempts := 0
					a.AwsClient = bedrockruntime.New(a.AwsClient.Options(), func(options *bedrockruntime.Options) {
						// Remove delay from the real SDK retry policy, keeping its
						// default attempt budget and retryable-error decisions.
						if mode != "protection" {
							options.Retryer = retry.NewStandard(func(options *retry.StandardOptions) {
								options.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
							})
						}
						options.HTTPClient = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
							attempts++
							assert.Equal(t, "custom-value", request.Header.Get("X-Channel-Tag"))
							assert.Equal(t, "DISABLED", request.Header.Get("X-Amzn-Bedrock-Trace"))
							assert.Empty(t, request.Header.Get("X-Forwarded-For"))
							assert.Empty(t, request.Header.Get("X-Forwarded-Proto"))
							assert.Empty(t, request.Header.Get("X-Real-IP"))
							assert.Empty(t, request.Header.Get("Forwarded"))
							assert.Empty(t, request.Header.Get("X-Client-Secret"))
							if mode == "compatibility" {
								assert.Equal(t, "test-guardrail", request.Header.Get("X-Amzn-Bedrock-GuardrailIdentifier"))
							} else {
								assert.Empty(t, request.Header.Get("X-Amzn-Bedrock-GuardrailIdentifier"))
							}
							assert.Equal(t, "bedrock-runtime.us-east-1.amazonaws.com", request.URL.Host)
							if strings.HasPrefix(key, "test-key|") {
								assert.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
							} else {
								assert.Contains(t, request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access-key/")
								assert.Contains(t, request.Header.Get("Authorization"), "x-channel-tag")
								assert.NotEqual(t, "invalid", request.Header.Get("X-Amz-Date"))
							}
							return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Amzn-Errortype": []string{"ThrottlingException"}}, Body: io.NopCloser(strings.NewReader(`{"message":"throttled"}`)), Request: request}, nil
						})
					})
					require.Nil(t, bedrockProtectionError(c, info, a))
					if stream {
						_, err = a.AwsClient.InvokeModelWithResponseStream(t.Context(), a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput))
					} else {
						_, err = a.AwsClient.InvokeModel(t.Context(), a.AwsReq.(*bedrockruntime.InvokeModelInput))
					}
					require.Error(t, err)
					wantAttempts := 3
					if mode == "protection" {
						wantAttempts = 1
					}
					assert.Equal(t, wantAttempts, attempts)
					service.FlushBedrockActualUsage()
					if mode == "off" {
						assert.Empty(t, server.Keys(), "disabled metrics must not write Redis")
					} else {
						assert.Equal(t, fmt.Sprint(attempts), server.HGet("bedrock:actual-usage:v1:{97}", "rpm"))
						assert.Equal(t, "0", server.HGet("bedrock:actual-usage:v1:{97}", "tpm"), "declared max_tokens is not actual usage")
					}
				})
			}
		}
	}
}

func TestAwsAPIKeyProtectionRejectsBeforeSendingAndRecordsThrottle(t *testing.T) {
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	service.InitHttpClient()
	httpClient := service.GetHttpClient()
	previousTransport := httpClient.Transport
	t.Cleanup(func() { httpClient.Transport = previousTransport })
	calls := 0
	status := http.StatusOK
	httpClient.Transport = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_tokens":128`)
		assert.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		assert.Equal(t, "bedrock-runtime.us-east-1.amazonaws.com", request.URL.Host)
		responseBody := `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`
		if status == http.StatusTooManyRequests {
			responseBody = `{"__type":"ThrottlingException","message":"slow down"}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(responseBody)), Request: request}, nil
	})
	info := newAwsTestRelayInfo()
	info.IsStream, info.ChannelId = false, 96
	info.ApiKey = "test-key|us-east-1"
	info.ChannelOtherSettings.AwsKeyType = dto.AwsKeyTypeApiKey
	settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, TPM: 127}
	info.ChannelSetting.BedrockProtection = settings
	adaptor := &Adaptor{}
	body := `{"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	_, err := adaptor.DoRequest(c, info, strings.NewReader(body))
	require.NoError(t, err)
	invokeErr, _ := awsHandler(c, info, adaptor)
	require.NotNil(t, invokeErr)
	wrapped := relaytypes.NewOpenAIError(invokeErr, relaytypes.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	assert.Equal(t, relaytypes.ErrorCodeChannelBedrockRateLimited, wrapped.GetErrorCode())
	assert.Equal(t, http.StatusTooManyRequests, wrapped.StatusCode)
	assert.Zero(t, calls)
	settings.TPM = 1000
	_, err = adaptor.DoRequest(c, info, strings.NewReader(body))
	require.NoError(t, err)
	_, invokeErr = adaptor.DoResponse(c, nil, info)
	require.Nil(t, invokeErr)
	assert.Equal(t, 1, calls)
	settings.RateLimitEnabled, settings.ThrottlingCooldownEnabled = false, true
	settings.MinimumSampleSize, settings.ThrottlingRatioPercent = 2, 100
	status = http.StatusTooManyRequests
	for range 2 {
		_, err = adaptor.DoRequest(c, info, strings.NewReader(body))
		require.NoError(t, err)
		_, invokeErr = adaptor.DoResponse(c, nil, info)
		require.NotNil(t, invokeErr)
		assert.Equal(t, relaytypes.ErrorCodeBedrockThrottled, invokeErr.GetErrorCode())
	}
	require.True(t, service.IsBedrockCoolingDown(info.ChannelId, settings))
	_, err = adaptor.DoRequest(c, info, strings.NewReader(body))
	require.NoError(t, err)
	_, invokeErr = adaptor.DoResponse(c, nil, info)
	require.NotNil(t, invokeErr)
	assert.Equal(t, relaytypes.ErrorCodeChannelBedrockCoolingDown, invokeErr.GetErrorCode())
	assert.Equal(t, 3, calls, "cooling channels must not reach the transport")
}

func TestAwsProtectionUsesFinalPayloadWithoutChangingBillingEstimate(t *testing.T) {
	server := miniredis.RunT(t)
	previousClient, previousEnabled, previousCount := common.RDB, common.RedisEnabled, constant.CountToken
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled, constant.CountToken = previousClient, previousEnabled, previousCount
	})
	for _, count := range []bool{false, true} {
		for _, source := range []string{"model default", "parameter override"} {
			t.Run(fmt.Sprintf("%s/local count=%t", source, count), func(t *testing.T) {
				server.FlushAll()
				constant.CountToken = count
				info := newAwsTestRelayInfo()
				info.IsStream = false
				info.ChannelId = 91
				info.SetEstimatePromptTokens(1)
				c := newAwsTestContext(httptest.NewRecorder(), t.Context())
				common.SetContextKey(c, constant.ContextKeyOriginalModel, awsTestModel)
				common.SetContextKey(c, constant.ContextKeyPromptTokens, 1)
				request := &dto.ClaudeRequest{Model: awsTestModel, System: "final system prompt", Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}}
				adaptor := &Adaptor{}
				_, err := adaptor.ConvertClaudeRequest(c, info, request)
				require.NoError(t, err)
				require.NotNil(t, request.MaxTokens)
				body, err := common.Marshal(request)
				require.NoError(t, err)
				if source == "parameter override" {
					info.ParamOverride = map[string]any{"max_tokens": 128}
					body, err = relaycommon.ApplyParamOverrideWithRelayInfo(body, info)
					require.NoError(t, err)
					require.NoError(t, common.Unmarshal(body, request))
				}
				wantTokens := tokenkit.Count(awsTestModel, "final system prompt\nuser\nhello") + int(*request.MaxTokens)
				settings := &dto.BedrockProtectionSettings{RateLimitEnabled: true, TPM: wantTokens - 1}
				info.ChannelSetting.BedrockProtection = settings
				invoked := false
				adaptor.AwsReq = &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsTestModel), Body: body}
				adaptor.AwsClient = newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					invoked = true
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)), Request: request}, nil
				}))
				handlerErr, _ := awsHandler(c, info, adaptor)
				require.NotNil(t, handlerErr)
				assert.Equal(t, relaytypes.ErrorCodeChannelBedrockRateLimited, handlerErr.GetErrorCode())
				assert.False(t, invoked, "over-budget requests must not reach AWS")

				settings.TPM = wantTokens
				handlerErr, _ = awsHandler(c, info, adaptor)
				require.Nil(t, handlerErr)
				assert.True(t, invoked)
				_, usage := service.ReserveBedrockUsage(info.ChannelId, 0, settings, info.UpstreamModelName)
				assert.EqualValues(t, wantTokens, usage.TPM)
				assert.Equal(t, 1, info.GetEstimatePromptTokens())
				assert.Equal(t, 1, common.GetContextKeyInt(c, constant.ContextKeyPromptTokens))
			})
		}
	}
}

func TestAwsProtectionCoversStreamNovaAndOversizedOverrides(t *testing.T) {
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	for _, tc := range []struct {
		name   string
		model  string
		body   string
		stream bool
		limit  int
		code   relaytypes.ErrorCode
	}{
		{"stream final output", awsTestModel, `{"messages":[{"role":"user","content":"hello"}],"max_tokens":128}`, true, 127, relaytypes.ErrorCodeChannelBedrockRateLimited},
		{"Nova dynamic output", "amazon.nova-pro-v1:0", `{"schemaVersion":"messages-v1","messages":[{"role":"user","content":[{"text":"hello"}]}]}`, false, 4999, relaytypes.ErrorCodeChannelBedrockRateLimited},
		{"Nova explicit output", "amazon.nova-pro-v1:0", `{"messages":[],"inferenceConfig":{"maxTokens":128}}`, false, 127, relaytypes.ErrorCodeChannelBedrockRateLimited},
		{"oversized final override", awsTestModel, `{"max_tokens":18446744073709551615}`, false, 10000, relaytypes.ErrorCodeBadRequestBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server.FlushAll()
			info := newAwsTestRelayInfo()
			info.UpstreamModelName = tc.model
			info.ChannelSetting.BedrockProtection = &dto.BedrockProtectionSettings{ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{tc.model: {TPM: tc.limit}}}
			adaptor := &Adaptor{AwsReq: &bedrockruntime.InvokeModelInput{Body: []byte(tc.body)}}
			if tc.stream {
				adaptor.AwsReq = &bedrockruntime.InvokeModelWithResponseStreamInput{Body: []byte(tc.body)}
			}
			err := bedrockProtectionError(newAwsTestContext(httptest.NewRecorder(), t.Context()), info, adaptor)
			require.NotNil(t, err)
			assert.Equal(t, tc.code, err.GetErrorCode())
		})
	}
}

func TestAwsStreamFailureDoesNotEmitSuccessfulTerminalEvent(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousMetrics := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	for _, tc := range []struct {
		name      string
		partial   bool
		throttled bool
		format    relaytypes.RelayFormat
	}{
		{"initial throttle", false, true, relaytypes.RelayFormatOpenAI},
		{"partial throttle", true, true, relaytypes.RelayFormatOpenAI},
		{"truncated stream", true, false, relaytypes.RelayFormatOpenAI},
		{"Claude initial throttle", false, true, relaytypes.RelayFormatClaude},
		{"Claude partial throttle", true, true, relaytypes.RelayFormatClaude},
		{"Claude truncated stream", true, false, relaytypes.RelayFormatClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server.FlushAll()
			info := newAwsTestRelayInfo()
			info.RelayFormat = tc.format
			info.ChannelId = 81
			info.ChannelSetting.BedrockProtection = &dto.BedrockProtectionSettings{
				ThrottlingCooldownEnabled: true, MinimumSampleSize: 1, ThrottlingRatioPercent: 100,
			}
			awsClient := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
				var body bytes.Buffer
				if tc.partial {
					require.NoError(t, writeAwsStreamEvent(&body, `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`))
					require.NoError(t, writeAwsStreamEvent(&body, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`))
				}
				if tc.throttled {
					require.NoError(t, eventstream.NewEncoder().Encode(&body, eventstream.Message{
						Headers: eventstream.Headers{
							{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.ExceptionMessageType)},
							{Name: eventstreamapi.ExceptionTypeHeader, Value: eventstream.StringValue("throttlingException")},
							{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
						}, Payload: []byte(`{"message":"throttled"}`),
					}))
				}
				return newAwsStreamResponse(request, io.NopCloser(bytes.NewReader(body.Bytes()))), nil
			}))
			recorder := httptest.NewRecorder()
			handlerErr, usage := awsStreamHandler(newAwsTestContext(recorder, t.Context()), info, &Adaptor{AwsClient: awsClient, AwsReq: newAwsStreamInput()})
			if tc.partial {
				require.Nil(t, handlerErr, "partial output must not replay or refund observed usage")
				require.NotNil(t, usage)
				require.NotNil(t, usage.BillingUsage)
				assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
				assert.Contains(t, recorder.Body.String(), "partial")
			} else {
				require.NotNil(t, handlerErr)
				assert.Equal(t, relaytypes.ErrorCodeBedrockThrottled, handlerErr.GetErrorCode())
				assert.Equal(t, http.StatusTooManyRequests, handlerErr.StatusCode)
			}
			assert.True(t, info.StreamStatus.HasErrors())
			assert.Equal(t, string(relaycommon.ResponseOutcomeFailed), info.StreamStatus.ResponseOutcome())
			assert.NotContains(t, recorder.Body.String(), "[DONE]")
			assert.NotContains(t, recorder.Body.String(), "event: message_stop")
			service.FlushBedrockActualUsage()
			assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{81}", "unknown"), "failed streams must mark TPM incomplete")
			assert.Equal(t, "0", server.HGet("bedrock:actual-usage:v1:{81}", "tpm"), "partial usage must not be presented as a final total")
			assert.Equal(t, tc.throttled, service.IsBedrockCoolingDown(info.ChannelId, info.ChannelSetting.BedrockProtection))
		})
	}
}

func TestBedrockProtectionSendsOnlyOneSDKAttempt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings dto.BedrockProtectionSettings
	}{
		{"channel limit", dto.BedrockProtectionSettings{RateLimitEnabled: true}},
		{"model limit", dto.BedrockProtectionSettings{ModelRateLimitEnabled: true}},
		{"cooldown", dto.BedrockProtectionSettings{ThrottlingCooldownEnabled: true}},
		{"jitter", dto.BedrockProtectionSettings{RetryBackoffEnabled: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := newAwsTestRelayInfo()
			info.ApiKey = "access-key|secret-key|us-east-1"
			info.ChannelSetting.BedrockProtection = &tc.settings
			client, err := newAwsClient(newAwsTestContext(httptest.NewRecorder(), t.Context()), info)
			require.NoError(t, err)
			attempts := 0
			client = bedrockruntime.New(client.Options(), func(options *bedrockruntime.Options) {
				options.HTTPClient = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					attempts++
					return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Amzn-Errortype": []string{"ThrottlingException"}}, Body: io.NopCloser(strings.NewReader(`{"message":"throttled"}`)), Request: request}, nil
				})
			})
			_, err = client.InvokeModel(t.Context(), newAwsInvokeModelInput())
			require.Error(t, err)
			assert.Equal(t, 1, attempts, "SDK retries must not bypass gateway reservations and cooldown")
		})
	}
}

type awsHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f awsHTTPClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f awsHTTPClientFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type awsNotifyingResponseWriter struct {
	*httptest.ResponseRecorder
	notifyOn []byte
	notified chan int
	once     sync.Once
}

func newAwsNotifyingResponseWriter(notifyOn string) *awsNotifyingResponseWriter {
	return &awsNotifyingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		notifyOn:         []byte(notifyOn),
		notified:         make(chan int, 1),
	}
}

func (w *awsNotifyingResponseWriter) Write(data []byte) (int, error) {
	return w.ResponseRecorder.Write(data)
}

func (w *awsNotifyingResponseWriter) Flush() {
	w.ResponseRecorder.Flush()
	if bytes.Contains(w.Body.Bytes(), w.notifyOn) {
		w.once.Do(func() {
			w.notified <- w.Body.Len()
		})
	}
}

func newAwsTestClient(httpClient bedrockruntime.HTTPClient) *bedrockruntime.Client {
	return bedrockruntime.New(bedrockruntime.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String("https://bedrock.test"),
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			"access-key", "secret-key", "",
		)),
		HTTPClient: httpClient,
		Retryer:    aws.NopRetryer{},
	})
}

func newAwsTestContext(writer http.ResponseWriter, requestContext context.Context) *gin.Context {
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestContext)
	return c
}

func newAwsTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		StartTime:          time.Now(),
		IsStream:           true,
		OriginModelName:    awsTestModel,
		RelayFormat:        relaytypes.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: awsTestModel,
		},
	}
}

func newAwsInvokeModelInput() *bedrockruntime.InvokeModelInput {
	return &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(awsTestModel),
		Body:        []byte(`{}`),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}
}

func newAwsStreamInput() *bedrockruntime.InvokeModelWithResponseStreamInput {
	return &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(awsTestModel),
		Body:        []byte(`{}`),
		Accept:      aws.String("application/json"),
		ContentType: aws.String("application/json"),
	}
}

func writeAwsStreamEvent(writer io.Writer, data string) error {
	payload, err := common.Marshal(struct {
		Bytes []byte `json:"bytes"`
	}{Bytes: []byte(data)})
	if err != nil {
		return err
	}

	return eventstream.NewEncoder().Encode(writer, eventstream.Message{
		Headers: eventstream.Headers{
			{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(eventstreamapi.EventMessageType)},
			{Name: eventstreamapi.EventTypeHeader, Value: eventstream.StringValue("chunk")},
			{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
		},
		Payload: payload,
	})
}

func newAwsStreamResponse(request *http.Request, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type":                []string{"application/vnd.amazon.eventstream"},
			"X-Amzn-Bedrock-Content-Type": []string{"application/json"},
		},
		Body:    body,
		Request: request,
	}
}

func TestDoAwsClientRequest_AppliesRuntimeHeaderOverrideToAnthropicBeta(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName:           "claude-3-5-sonnet-20240620",
		IsStream:                  false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"anthropic-beta": "computer-use-2025-01-24",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "access-key|secret-key|us-east-1",
			UpstreamModelName: "claude-3-5-sonnet-20240620",
		},
	}

	requestBody := bytes.NewBufferString(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":128}`)
	adaptor := &Adaptor{}

	_, err := doAwsClientRequest(ctx, info, adaptor, requestBody)
	require.NoError(t, err)

	awsReq, ok := adaptor.AwsReq.(*bedrockruntime.InvokeModelInput)
	require.True(t, ok)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(awsReq.Body, &payload))

	anthropicBeta, exists := payload["anthropic_beta"]
	require.True(t, exists)

	values, ok := anthropicBeta.([]any)
	require.True(t, ok)
	require.Equal(t, []any{"computer-use-2025-01-24"}, values)
}

func TestAwsFormattedRequestFiltersBodyBetasAndHonorsHeaderOverride(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	common.OptionMapRWMutex.Lock()
	previousEnabled, previousBlacklist := settings.AwsBetaFilterEnabled, settings.AwsBetaBlacklist
	settings.AwsBetaFilterEnabled, settings.AwsBetaBlacklist = true, []string{"blocked"}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		settings.AwsBetaFilterEnabled, settings.AwsBetaBlacklist = previousEnabled, previousBlacklist
	})
	for _, tc := range []struct {
		name, body, header string
		want               []string
		disabled           bool
	}{
		{"body blacklist", `["blocked","allowed"]`, "", []string{"allowed"}, false},
		{"fully blocked body", `["blocked"]`, "", nil, false},
		{"blocked header must not restore body", `["allowed"]`, "blocked", nil, false},
		{"header override", `["body-beta"]`, "header-beta", []string{"header-beta"}, false},
		{"disabled filter keeps body", `["blocked","allowed"]`, "", []string{"blocked", "allowed"}, true},
		{"disabled filter keeps header override", `["allowed"]`, "blocked", []string{"blocked"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.OptionMapRWMutex.Lock()
			settings.AwsBetaFilterEnabled = !tc.disabled
			common.OptionMapRWMutex.Unlock()
			headers := http.Header{}
			headers.Set("anthropic-beta", tc.header)
			formatted, err := formatRequest(strings.NewReader(`{"max_tokens":128,"messages":[],"anthropic_beta":`+tc.body+`}`), headers)
			require.NoError(t, err)
			encoded, err := common.Marshal(formatted)
			require.NoError(t, err)
			var payload struct {
				Betas []string `json:"anthropic_beta"`
			}
			require.NoError(t, common.Unmarshal(encoded, &payload))
			assert.Equal(t, tc.want, payload.Betas)
		})
	}
}

func TestDoAwsClientRequest_ForwardsThinkingBlockBindingAndBetaList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Request.Header.Set("anthropic-beta", "fast-mode-2026-02-01, thinking-binding-controls-2026-08-01")

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-5-5",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "access-key|secret-key|us-east-1",
			UpstreamModelName: "claude-opus-5-5",
		},
	}
	requestBody := bytes.NewBufferString(`{"max_tokens":128,"messages":[{"role":"user","content":"hello"}],` +
		`"thinking":{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}}`)
	adaptor := &Adaptor{}

	_, err := doAwsClientRequest(ctx, info, adaptor, requestBody)
	require.NoError(t, err)
	awsReq, ok := adaptor.AwsReq.(*bedrockruntime.InvokeModelInput)
	require.True(t, ok)

	var payload struct {
		AnthropicBeta []string        `json:"anthropic_beta"`
		Thinking      json.RawMessage `json:"thinking"`
	}
	require.NoError(t, common.Unmarshal(awsReq.Body, &payload))
	assert.Equal(t, []string{"fast-mode-2026-02-01", "thinking-binding-controls-2026-08-01"}, payload.AnthropicBeta)
	assert.JSONEq(t, `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, string(payload.Thinking))
}

// InvokeModel takes the model and streaming from the request, so the
// pass-through body keeps no copy of either, even when the client repeats them.
func TestBuildAwsRequestBodyPassThroughDropsRepeatedModelAndStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"a","stream":true,"max_tokens":8,"stream":false,"model":"b"}`))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.ChannelSetting.PassThroughBodyEnabled = true

	body, err := buildAwsRequestBody(c, info, nil)

	require.NoError(t, err)
	assert.Equal(t, `{"max_tokens":8}`, string(body))
}

func TestDoAwsClientRequest_PreservesSafeguards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := model_setting.GetGlobalSettings()
	originalPassthrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = originalPassthrough })

	const legacyBody = `{"anthropic_version":"bedrock-2023-05-31","anthropic_beta":["dangerous-tool-use-2026-09-03"],"messages":[{"role":"user","content":"hello"}],"max_tokens":512}`
	tests := []struct {
		name       string
		safeguards string
	}{
		{name: "omitted"},
		{name: "empty_array", safeguards: `[]`},
		{name: "dangerous_tool_use", safeguards: `[{"type":"dangerous_tool_use","classifier_context":{"v":1,"permission_mode":"auto"}}]`},
		// Preserve invalid values so AWS can reject them instead of silently disabling the feature.
		{name: "invalid_object", safeguards: `{"enabled":true,"policy":"strict","blocked_actions":["rm -rf","sudo"]}`},
	}
	for _, tt := range tests {
		for _, stream := range []bool{false, true} {
			name := tt.name + "/nonstream"
			if stream {
				name = tt.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				input := map[string]any{
					"model": "claude-sonnet-5", "max_tokens": 512, "stream": stream,
					"messages": []map[string]any{{"role": "user", "content": "hello"}},
				}
				if tt.safeguards != "" {
					input["safeguards"] = json.RawMessage(tt.safeguards)
				}
				raw, err := common.Marshal(input)
				require.NoError(t, err)
				var request dto.ClaudeRequest
				require.NoError(t, common.Unmarshal(raw, &request))

				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(raw))
				ctx.Request.Header.Set("anthropic-beta", "dangerous-tool-use-2026-09-03")
				info := &relaycommon.RelayInfo{
					OriginModelName: "claude-sonnet-5",
					IsStream:        stream,
					RelayFormat:     relaytypes.RelayFormatClaude,
					ChannelMeta: &relaycommon.ChannelMeta{
						ApiKey:            "access-key|secret-key|us-east-1",
						UpstreamModelName: "global.anthropic.claude-sonnet-5",
					},
				}
				adaptor := &Adaptor{}
				converted, err := adaptor.ConvertClaudeRequest(ctx, info, &request)
				require.NoError(t, err)
				raw, err = common.Marshal(converted)
				require.NoError(t, err)
				_, err = doAwsClientRequest(ctx, info, adaptor, bytes.NewReader(raw))
				require.NoError(t, err)

				var outbound []byte
				if stream {
					sdkRequest, ok := adaptor.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput)
					require.True(t, ok)
					outbound = sdkRequest.Body
				} else {
					sdkRequest, ok := adaptor.AwsReq.(*bedrockruntime.InvokeModelInput)
					require.True(t, ok)
					outbound = sdkRequest.Body
				}
				if tt.safeguards == "" {
					assert.Equal(t, legacyBody, string(outbound), "legacy requests must remain byte-for-byte unchanged")
					return
				}
				var expected map[string]json.RawMessage
				require.NoError(t, common.UnmarshalJsonStr(legacyBody, &expected))
				expected["safeguards"] = json.RawMessage(tt.safeguards)
				want, err := common.Marshal(expected)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), string(outbound))
			})
		}
	}
}

func TestNewAwsInvokeContextInheritsParent(t *testing.T) {
	originalRelayTimeout := common.RelayTimeout
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
	})

	tests := []struct {
		name         string
		relayTimeout int
		wantDeadline bool
	}{
		{name: "without relay timeout", relayTimeout: 0, wantDeadline: false},
		{name: "with relay timeout", relayTimeout: 30, wantDeadline: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			common.RelayTimeout = test.relayTimeout
			parent, cancelParent := context.WithCancel(context.Background())
			invokeContext, cancelInvoke := newAwsInvokeContext(parent)
			defer cancelInvoke()

			_, hasDeadline := invokeContext.Deadline()
			assert.Equal(t, test.wantDeadline, hasDeadline)

			cancelParent()
			require.ErrorIs(t, invokeContext.Err(), context.Canceled)
		})
	}
}

func TestAwsValidationErrorsPreserveCapabilityRejectionWording(t *testing.T) {
	for _, message := range []string{
		"Claude Code 2.1.215 does not support this model; version 2.1.280 or newer is required",
		`Invalid signature in thinking block; set "thinking.block_binding.prefix_mismatch_behavior" to "drop_block"`,
		"context_management: Extra inputs are not permitted",
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, message), func(t *testing.T) {
				body, err := common.Marshal(map[string]string{"message": message})
				require.NoError(t, err)
				client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Header:     http.Header{"Content-Type": {"application/json"}, "X-Amzn-Errortype": {"ValidationException"}},
						Body:       io.NopCloser(bytes.NewReader(body)),
						Request:    request,
					}, nil
				}))
				info := newAwsTestRelayInfo()
				info.RelayFormat = relaytypes.RelayFormatClaude
				recorder := httptest.NewRecorder()
				c := newAwsTestContext(recorder, t.Context())
				adaptor := &Adaptor{AwsClient: client, AwsReq: newAwsInvokeModelInput()}
				var invokeErr *relaytypes.NewAPIError
				if stream {
					adaptor.AwsReq = newAwsStreamInput()
					invokeErr, _ = awsStreamHandler(c, info, adaptor)
				} else {
					invokeErr, _ = awsHandler(c, info, adaptor)
				}
				require.NotNil(t, invokeErr)
				assert.Equal(t, http.StatusBadRequest, invokeErr.StatusCode)
				assert.Contains(t, invokeErr.ToClaudeError().Message, message, "client recovery must receive the original rejection wording")
				assert.Empty(t, recorder.Body.String(), "initial errors must not start a successful stream")
			})
		}
	}
}

func TestNewAwsInvokeErrorSkipsRetryOnlyForClientCancellation(t *testing.T) {
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name           string
		requestContext context.Context
		err            error
		wantSkipRetry  bool
	}{
		{
			name:           "client context canceled",
			requestContext: canceledContext,
			err:            context.Canceled,
			wantSkipRetry:  true,
		},
		{
			name:           "relay timeout with live client context",
			requestContext: context.Background(),
			err:            context.DeadlineExceeded,
			wantSkipRetry:  false,
		},
		{
			name:           "upstream error with live client context",
			requestContext: context.Background(),
			err:            errors.New("upstream failed"),
			wantSkipRetry:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newAwsInvokeError(test.requestContext, test.err, "InvokeModel")
			assert.Equal(t, test.wantSkipRetry, relaytypes.IsSkipRetryError(err))
		})
	}
}

func TestAwsHandlersCancelSdkRequestAndSkipRetry(t *testing.T) {
	originalRelayTimeout := common.RelayTimeout
	common.RelayTimeout = 0
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
	})

	tests := []struct {
		name    string
		request any
		handle  func(*gin.Context, *relaycommon.RelayInfo, *Adaptor) (*relaytypes.NewAPIError, *dto.Usage)
	}{
		{name: "non-stream", request: newAwsInvokeModelInput(), handle: awsHandler},
		{name: "stream", request: newAwsStreamInput(), handle: awsStreamHandler},
		{name: "nova", request: newAwsInvokeModelInput(), handle: handleNovaRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancelRequest := context.WithCancel(context.Background())
			t.Cleanup(cancelRequest)

			upstreamContexts := make(chan context.Context, 1)
			client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
				upstreamContexts <- request.Context()
				<-request.Context().Done()
				return nil, request.Context().Err()
			}))
			adaptor := &Adaptor{AwsClient: client, AwsReq: test.request}
			c := newAwsTestContext(httptest.NewRecorder(), requestContext)
			info := newAwsTestRelayInfo()

			type handlerResult struct {
				err   *relaytypes.NewAPIError
				usage *dto.Usage
			}
			results := make(chan handlerResult, 1)
			go func() {
				err, usage := test.handle(c, info, adaptor)
				results <- handlerResult{err: err, usage: usage}
			}()

			var upstreamContext context.Context
			select {
			case upstreamContext = <-upstreamContexts:
			case result := <-results:
				t.Fatalf("handler returned before issuing AWS request: %v", result.err)
			case <-time.After(5 * time.Second):
				t.Fatal("AWS request did not start")
			}

			cancelRequest()

			var result handlerResult
			select {
			case result = <-results:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not stop after client cancellation")
			}

			require.ErrorIs(t, upstreamContext.Err(), context.Canceled)
			require.NotNil(t, result.err)
			assert.True(t, relaytypes.IsSkipRetryError(result.err))
			assert.Nil(t, result.usage)
		})
	}
}

func TestAwsStreamHandlerUsesFinalUpstreamUsage(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	previousMetrics := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = redisClient, true
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, redisClient.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
		model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
	})
	originalRelayTimeout := common.RelayTimeout
	common.RelayTimeout = 0
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
	})

	events := []string{
		`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":423}}`,
		`{"type":"message_stop"}`,
	}
	client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		var body bytes.Buffer
		for _, event := range events {
			if err := writeAwsStreamEvent(&body, event); err != nil {
				return nil, err
			}
		}
		return newAwsStreamResponse(request, io.NopCloser(bytes.NewReader(body.Bytes()))), nil
	}))
	adaptor := &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()}
	recorder := httptest.NewRecorder()
	c := newAwsTestContext(recorder, context.Background())
	info := newAwsTestRelayInfo()
	info.ChannelId = 98

	require.Nil(t, bedrockProtectionError(c, info, adaptor))
	handlerErr, usage := awsStreamHandler(c, info, adaptor)
	service.FlushBedrockActualUsage()

	require.Nil(t, handlerErr)
	require.NotNil(t, usage)
	require.NotNil(t, usage.BillingUsage)
	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	assert.Equal(t, 423, usage.BillingUsage.ClaudeUsage.OutputTokens)
	assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{98}", "rpm"))
	assert.Equal(t, "523", server.HGet("bedrock:actual-usage:v1:{98}", "tpm"))
	assert.Contains(t, recorder.Body.String(), "[DONE]")
}

func TestBedrockUsageUsesOneSettingSnapshotPerRequest(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousClient, previousRedis := common.RDB, common.RedisEnabled
	previousMetrics := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousRedis
		model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
	})
	for _, stream := range []bool{false, true} {
		for _, initiallyEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/initially_enabled=%t", stream, initiallyEnabled), func(t *testing.T) {
				service.FlushBedrockActualUsage()
				server.FlushAll()
				model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = initiallyEnabled
				upstream := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					// Simulate a settings update after dispatch but before usage arrives.
					model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = !initiallyEnabled
					if stream {
						var body bytes.Buffer
						for _, event := range []string{
							`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":0}}}`,
							`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":23}}`,
							`{"type":"message_stop"}`,
						} {
							require.NoError(t, writeAwsStreamEvent(&body, event))
						}
						return newAwsStreamResponse(request, io.NopCloser(bytes.NewReader(body.Bytes()))), nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request,
						Body: io.NopCloser(strings.NewReader(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":23}}`))}, nil
				}))
				adaptor := &Adaptor{AwsClient: upstream, AwsReq: &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsTestModel), Body: []byte(`{"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)}}
				info := newAwsTestRelayInfo()
				info.ChannelId, info.IsStream = 99, stream
				c := newAwsTestContext(httptest.NewRecorder(), t.Context())
				if stream {
					adaptor.AwsReq = newAwsStreamInput()
					handlerErr, _ := awsStreamHandler(c, info, adaptor)
					require.Nil(t, handlerErr)
				} else {
					handlerErr, _ := awsHandler(c, info, adaptor)
					require.Nil(t, handlerErr)
				}
				service.FlushBedrockActualUsage()
				if initiallyEnabled {
					assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{99}", "rpm"))
					assert.Equal(t, "123", server.HGet("bedrock:actual-usage:v1:{99}", "tpm"))
					// Reusing the adaptor after disabling metrics must also remove
					// its previous attempt observer, not just stop token collection.
					nextContext := newAwsTestContext(httptest.NewRecorder(), t.Context())
					if stream {
						handlerErr, _ := awsStreamHandler(nextContext, info, adaptor)
						require.Nil(t, handlerErr)
					} else {
						handlerErr, _ := awsHandler(nextContext, info, adaptor)
						require.Nil(t, handlerErr)
					}
					service.FlushBedrockActualUsage()
					assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{99}", "rpm"), "disabled requests must not retain an old observer")
					assert.Equal(t, "123", server.HGet("bedrock:actual-usage:v1:{99}", "tpm"))
				} else {
					assert.Empty(t, server.Keys(), "enabling during an in-flight request must not record TPM without RPM")
				}
			})
		}
	}
}

func TestBedrockActualUsageIgnoresOutputBudgetAndUsesAuthoritativeSnapshots(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	tests := []struct {
		name   string
		events []string
		total  int64
		known  bool
	}{
		{"nonstream with cache", []string{`{"usage":{"input_tokens":100,"output_tokens":23,"cache_read_input_tokens":50,"cache_creation_input_tokens":10}}`}, 183, true},
		{"cumulative stream usage", []string{`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":1}}}`, `{"type":"message_delta","usage":{"output_tokens":20}}`, `{"type":"message_delta","usage":{"output_tokens":23}}`}, 123, true},
		{"disconnected before final output usage", []string{`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":1}}}`}, 0, false},
		{"Nova", []string{`{"usage":{"inputTokens":100,"outputTokens":23,"totalTokens":123}}`}, 123, true},
		{"explicit zero", []string{`{"usage":{"input_tokens":0,"output_tokens":0}}`}, 0, true},
		{"missing usage", []string{`{"content":[]}`}, 0, false},
		{"content deltas are not usage events", []string{`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"usage\":{\"input_tokens\":999}}"},"usage":{"input_tokens":999,"output_tokens":999}}`}, 0, false},
		{"usage after content and ping", []string{`{"type":"message_start","message":{"usage":{"input_tokens":100,"output_tokens":1}}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"response text"}}`, `{"type":"ping"}`, `{"type":"message_delta","usage":{"output_tokens":23}}`}, 123, true},
		{"invalid final output must not reuse earlier snapshot", []string{`{"usage":{"input_tokens":100,"output_tokens":20}}`, `{"usage":{"output_tokens":-1}}`}, 0, false},
		{"fractional cache usage is not exact", []string{`{"usage":{"input_tokens":100,"output_tokens":23,"cache_read_input_tokens":1.5}}`}, 0, false},
		{"null cache usage is not zero", []string{`{"usage":{"input_tokens":100,"output_tokens":23,"cache_creation_input_tokens":null}}`}, 0, false},
		{"invalid total must not fall back to components", []string{`{"usage":{"inputTokens":100,"outputTokens":23,"totalTokens":"123"}}`}, 0, false},
		{"oversized sum must not be reported as exact", []string{`{"usage":{"input_tokens":2147483647,"output_tokens":1}}`}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service.FlushBedrockActualUsage()
			server.FlushAll()
			usage := bedrockActualTokenUsage{}
			for _, event := range tc.events {
				usage.observe([]byte(event))
			}
			usage.record(101)
			service.FlushBedrockActualUsage()
			assert.Equal(t, fmt.Sprint(tc.total), server.HGet("bedrock:actual-usage:v1:{101}", "tpm"))
			if tc.known {
				assert.Equal(t, "0", server.HGet("bedrock:actual-usage:v1:{101}", "unknown"))
			} else {
				assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{101}", "unknown"))
			}
		})
	}
}

func TestBedrockTransportFailureMarksTokenUsageUnknown(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousClient, previousRedis := common.RDB, common.RedisEnabled
	previousMetrics := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousRedis
		model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
	})
	for _, tc := range []struct {
		name    string
		request any
		handle  func(*gin.Context, *relaycommon.RelayInfo, *Adaptor) (*relaytypes.NewAPIError, *dto.Usage)
	}{
		{"Claude", newAwsInvokeModelInput(), awsHandler},
		{"stream", newAwsStreamInput(), awsStreamHandler},
		{"Nova", newAwsInvokeModelInput(), handleNovaRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service.FlushBedrockActualUsage()
			server.FlushAll()
			upstream := newAwsTestClient(awsHTTPClientFunc(func(*http.Request) (*http.Response, error) {
				return nil, io.ErrUnexpectedEOF
			}))
			info := newAwsTestRelayInfo()
			info.ChannelId = 106
			adaptor := &Adaptor{AwsClient: upstream, AwsReq: tc.request}
			handlerErr, usage := tc.handle(newAwsTestContext(httptest.NewRecorder(), t.Context()), info, adaptor)
			require.NotNil(t, handlerErr)
			assert.Nil(t, usage)
			service.FlushBedrockActualUsage()
			assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{106}", "rpm"))
			assert.Equal(t, "0", server.HGet("bedrock:actual-usage:v1:{106}", "tpm"))
			assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{106}", "unknown"), "a lost upstream response is not proof of zero token usage")
		})
	}
}

func TestNovaActualUsageSurvivesResponseConversionErrors(t *testing.T) {
	service.FlushBedrockActualUsage()
	server := miniredis.RunT(t)
	previousClient, previousRedis := common.RDB, common.RedisEnabled
	previousMetrics := model_setting.GetGlobalSettings().AwsUsageMetricsEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = true
	t.Cleanup(func() {
		service.FlushBedrockActualUsage()
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousRedis
		model_setting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
	})
	for _, tc := range []struct {
		name, body, tpm, unknown string
	}{
		{"missing content with valid usage", `{"output":{"message":{"content":[]}},"usage":{"inputTokens":100,"outputTokens":23,"totalTokens":123}}`, "123", "0"},
		{"invalid response without usage", `not-json`, "0", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service.FlushBedrockActualUsage()
			server.FlushAll()
			upstream := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			adaptor := &Adaptor{AwsClient: upstream, AwsReq: newAwsInvokeModelInput()}
			info := newAwsTestRelayInfo()
			info.ChannelId = 102
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			handlerErr, usage := handleNovaRequest(c, info, adaptor)
			require.NotNil(t, handlerErr)
			assert.Nil(t, usage)
			service.FlushBedrockActualUsage()
			assert.Equal(t, "1", server.HGet("bedrock:actual-usage:v1:{102}", "rpm"))
			assert.Equal(t, tc.tpm, server.HGet("bedrock:actual-usage:v1:{102}", "tpm"))
			assert.Equal(t, tc.unknown, server.HGet("bedrock:actual-usage:v1:{102}", "unknown"))
		})
	}
}

func TestAwsStreamHeartbeatHonorsSettingsAndPreservesProtocol(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
	})
	for _, tc := range []struct {
		name        string
		enabled     bool
		disablePing bool
		format      relaytypes.RelayFormat
	}{
		{"Claude heartbeat", true, false, relaytypes.RelayFormatClaude},
		{"OpenAI heartbeat", true, false, relaytypes.RelayFormatOpenAI},
		{"global switch off", false, false, relaytypes.RelayFormatClaude},
		{"request disables ping", true, true, relaytypes.RelayFormatClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.PingIntervalEnabled, settings.PingIntervalSeconds = tc.enabled, 1
			synctest.Test(t, func(t *testing.T) {
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					return newAwsStreamResponse(request, reader), nil
				}))
				info := newAwsTestRelayInfo()
				info.RelayFormat, info.DisablePing = tc.format, tc.disablePing
				recorder := httptest.NewRecorder()
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					err, usage := awsStreamHandler(newAwsTestContext(recorder, t.Context()), info, &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()})
					assert.Nil(t, err)
					assert.NotNil(t, usage)
				}()
				synctest.Wait()
				// Virtual time tests actual ticker behavior without a wall-clock wait.
				time.Sleep(time.Second)
				synctest.Wait()
				assert.Empty(t, recorder.Body.String(), "initial AWS errors must still be able to return an HTTP error")
				for _, event := range []string{
					`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
				} {
					require.NoError(t, writeAwsStreamEvent(writer, event))
				}
				synctest.Wait()
				time.Sleep(time.Second)
				synctest.Wait()
				assert.Equal(t, tc.enabled && !tc.disablePing, strings.Contains(recorder.Body.String(), ": PING\n\n"))
				for _, event := range []string{
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
					`{"type":"message_stop"}`,
				} {
					require.NoError(t, writeAwsStreamEvent(writer, event))
				}
				synctest.Wait()
				terminalBody := recorder.Body.String()
				time.Sleep(time.Second)
				synctest.Wait()
				assert.Equal(t, terminalBody, recorder.Body.String(), "message_stop must prevent further pings even before upstream EOF")
				require.NoError(t, writer.Close())
				<-finished
				assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
				assert.Equal(t, string(relaycommon.ResponseOutcomeCompleted), info.StreamStatus.ResponseOutcome())
				body := recorder.Body.String()
				time.Sleep(time.Second)
				synctest.Wait()
				assert.Equal(t, body, recorder.Body.String(), "completed streams must stop their heartbeat")
			})
		})
	}
}

func TestAwsStreamHandlerStopsAtClientCancellation(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 1
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
	})
	originalRelayTimeout := common.RelayTimeout
	common.RelayTimeout = 0
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
	})

	requestContext, cancelRequest := context.WithCancel(context.Background())
	t.Cleanup(cancelRequest)
	releaseFinal := make(chan struct{})
	var releaseFinalOnce sync.Once
	release := func() {
		releaseFinalOnce.Do(func() {
			close(releaseFinal)
		})
	}
	t.Cleanup(release)

	producerResults := make(chan error, 1)
	upstreamContexts := make(chan context.Context, 1)
	client := newAwsTestClient(awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		upstreamContexts <- request.Context()
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			initialEvents := []string{
				`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
			}
			for _, event := range initialEvents {
				if err := writeAwsStreamEvent(writer, event); err != nil {
					producerResults <- err
					return
				}
			}

			<-releaseFinal
			producerResults <- writeAwsStreamEvent(writer, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":423}}`)
		}()
		return newAwsStreamResponse(request, reader), nil
	}))

	responseWriter := newAwsNotifyingResponseWriter("partial")
	c := newAwsTestContext(responseWriter, requestContext)
	adaptor := &Adaptor{AwsClient: client, AwsReq: newAwsStreamInput()}

	type handlerResult struct {
		err   *relaytypes.NewAPIError
		usage *dto.Usage
	}
	results := make(chan handlerResult, 1)
	go func() {
		err, usage := awsStreamHandler(c, newAwsTestRelayInfo(), adaptor)
		results <- handlerResult{err: err, usage: usage}
	}()

	var upstreamContext context.Context
	select {
	case upstreamContext = <-upstreamContexts:
	case <-time.After(5 * time.Second):
		t.Fatal("AWS stream request did not start")
	}

	var bodyLengthBeforeCancel int
	select {
	case bodyLengthBeforeCancel = <-responseWriter.notified:
	case <-time.After(5 * time.Second):
		t.Fatal("partial response was not written")
	}
	cancelRequest()

	var result handlerResult
	select {
	case result = <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("stream handler did not stop after client cancellation")
	}

	require.ErrorIs(t, upstreamContext.Err(), context.Canceled)
	require.Nil(t, result.err)
	require.NotNil(t, result.usage)
	assert.Equal(t, bodyLengthBeforeCancel, responseWriter.Body.Len())
	assert.NotContains(t, responseWriter.Body.String(), "[DONE]")

	release()
	select {
	case producerErr := <-producerResults:
		require.Error(t, producerErr)
	case <-time.After(5 * time.Second):
		t.Fatal("upstream producer did not observe the closed stream")
	}
}

func TestBedrockFriendlyModelTPMUsesMappedTargetBeforeSending(t *testing.T) {
	server := miniredis.RunT(t)
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() { require.NoError(t, client.Close()); common.RDB, common.RedisEnabled = oldClient, oldEnabled })
	target := "global.anthropic.claude-sonnet-4-6"
	info := newAwsTestRelayInfo()
	info.ChannelId = 4979
	info.UpstreamModelName = target
	settings := &dto.BedrockProtectionSettings{ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{"claude-sonnet-4-6": {TPM: 99}}}
	info.ChannelSetting.BedrockProtection = settings
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	c.Set("model_mapping", `{"claude-sonnet-4-6":"global.anthropic.claude-sonnet-4-6"}`)
	a := &Adaptor{AwsClient: newAwsTestClient(awsHTTPClientFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("protection must not send network requests")
		return nil, nil
	})), AwsReq: &bedrockruntime.InvokeModelInput{ModelId: aws.String(target), Body: []byte(`{"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`)}}
	rejected := bedrockProtectionError(c, info, a)
	require.NotNil(t, rejected)
	assert.Equal(t, relaytypes.ErrorCodeChannelBedrockRateLimited, rejected.GetErrorCode(), "model TPM must estimate max_tokens rather than recording unknown")
	settings.ModelLimits["claude-sonnet-4-6"] = dto.BedrockModelLimit{TPM: 10000}
	require.Nil(t, bedrockProtectionError(c, info, a))
	assert.Equal(t, "0", server.HGet("bedrock:usage:v2:{4979}", "unknown"))
	assert.NotEqual(t, "0", server.HGet("bedrock:usage:v2:{4979}", "tpm"))
}

func newMantleTestInfo() *relaycommon.RelayInfo {
	info := newAwsTestRelayInfo()
	info.IsStream = false
	info.RelayFormat = relaytypes.RelayFormatClaude
	info.UpstreamModelName = "claude-opus-4-7"
	info.ApiKey = "test-api-key|us-east-1"
	info.ChannelOtherSettings.AwsApiType = dto.AwsApiTypeMantle
	return info
}

func TestMantleRequestPreservesNativeFieldsAndSignsFinalPayload(t *testing.T) {
	for _, key := range []string{"test-api-key|us-east-1", "access-key|secret-key|eu-west-1"} {
		t.Run(strings.Split(key, "|")[0], func(t *testing.T) {
			info := newMantleTestInfo()
			info.ApiKey = key
			info.UseRuntimeHeadersOverride = true
			info.RuntimeHeadersOverride = map[string]any{"anthropic-beta": "test-beta", "X-Test": "override"}
			info.ChannelSetting.PassThroughBodyEnabled = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := newAwsTestContext(httptest.NewRecorder(), ctx)
			c.Request.Header.Set("Authorization", "Bearer client-secret")
			c.Request.Header.Set("x-api-key", "client-secret")
			a := &Adaptor{}
			body := `{"model":"client-alias","stream":true,"max_tokens":128,"temperature":0,"cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}],"future_option":{"enabled":false},"anthropic_version":"bedrock-2023-05-31"}`
			req, err := a.newMantleRequest(c, info, strings.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, "/anthropic/v1/messages", req.URL.Path)
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "2023-06-01", req.Header.Get("anthropic-version"))
			assert.Equal(t, "test-beta", req.Header.Get("anthropic-beta"))
			assert.Equal(t, "override", req.Header.Get("X-Test"))
			payload, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			var data map[string]json.RawMessage
			require.NoError(t, common.Unmarshal(payload, &data))
			assert.JSONEq(t, `"anthropic.claude-opus-4-7"`, string(data["model"]))
			assert.JSONEq(t, `{"type":"ephemeral","ttl":"1h"}`, string(data["cache_control"]))
			assert.JSONEq(t, `[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]`, string(data["messages"]))
			assert.JSONEq(t, `{"enabled":false}`, string(data["future_option"]))
			assert.Equal(t, "0", string(data["temperature"]))
			assert.Equal(t, "true", string(data["stream"]))
			assert.NotContains(t, data, "anthropic_version")
			replay, err := req.GetBody()
			require.NoError(t, err)
			defer replay.Close()
			replayed, err := io.ReadAll(replay)
			require.NoError(t, err)
			assert.Equal(t, payload, replayed)
			if strings.HasPrefix(key, "access-key|") {
				assert.Empty(t, req.Header.Get("x-api-key"))
				auth := req.Header.Get("Authorization")
				assert.Contains(t, auth, "/eu-west-1/bedrock-mantle/aws4_request")
				assert.NotContains(t, auth, "secret-key")
				signedAt, err := time.Parse("20060102T150405Z", req.Header.Get("X-Amz-Date"))
				require.NoError(t, err)
				verification := req.Clone(ctx)
				verification.Header.Del("Authorization")
				hash := sha256.Sum256(payload)
				err = v4.NewSigner().SignHTTP(ctx, aws.Credentials{AccessKeyID: "access-key", SecretAccessKey: "secret-key"}, verification, hex.EncodeToString(hash[:]), "bedrock-mantle", "eu-west-1", signedAt)
				require.NoError(t, err)
				assert.Equal(t, auth, verification.Header.Get("Authorization"))
			} else {
				assert.Equal(t, "test-api-key", req.Header.Get("x-api-key"))
				assert.Empty(t, req.Header.Get("Authorization"))
			}
			cancel()
			assert.ErrorIs(t, req.Context().Err(), context.Canceled)
		})
	}
}

func TestMantleEndpointAndCredentialValidation(t *testing.T) {
	for _, test := range []struct {
		name, key, base, want string
		invalid               bool
	}{
		{name: "regional default", key: "key|us-west-2", want: "https://bedrock-mantle.us-west-2.api.aws/anthropic/v1/messages"},
		{name: "proxy root", key: "ak|sk|us-east-1", base: "https://proxy.example/gateway/", want: "https://proxy.example/gateway/anthropic/v1/messages"},
		{name: "anthropic root", key: "key|us-east-1", base: "https://proxy.example/anthropic/", want: "https://proxy.example/anthropic/v1/messages"},
		{name: "version root", key: "key|us-east-1", base: "https://proxy.example/anthropic/v1", want: "https://proxy.example/anthropic/v1/messages"},
		{name: "full endpoint", key: "key|us-east-1", base: "https://proxy.example/anthropic/v1/messages", want: "https://proxy.example/anthropic/v1/messages"},
		{name: "missing region", key: "secret", invalid: true},
		{name: "empty key", key: "|us-east-1", invalid: true},
		{name: "empty secret", key: "ak||us-east-1", invalid: true},
		{name: "injected host", key: "key|us-east-1.evil.example/", invalid: true},
		{name: "plaintext URL", key: "key|us-east-1", base: "http://proxy.example", invalid: true},
		{name: "URL credentials", key: "key|us-east-1", base: "https://user:secret@proxy.example", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := newMantleTestInfo()
			info.ApiKey, info.ChannelBaseUrl = test.key, test.base
			endpoint, err := (&Adaptor{}).GetRequestURL(info)
			if test.invalid {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "secret")
				assert.Empty(t, endpoint)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, endpoint)
		})
	}
}

func TestMantleHTTPResponsesPreserveCacheUsage(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	const usageJSON = `{"input_tokens":100,"output_tokens":23,"cache_read_input_tokens":1000,"cache_creation_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30}}`
	for _, stream := range []bool{false, true} {
		for _, format := range []relaytypes.RelayFormat{relaytypes.RelayFormatClaude, relaytypes.RelayFormatOpenAI} {
			t.Run(fmt.Sprintf("stream=%t/format=%s", stream, format), func(t *testing.T) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_mantle\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"anthropic.claude-opus-4-7\",\"content\":[],\"usage\":%s}}\n\n", usageJSON)
						fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
						fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":23}}\n\n")
						fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"msg_mantle","type":"message","role":"assistant","model":"anthropic.claude-opus-4-7","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":%s}`, usageJSON)
				}))
				defer server.Close()
				client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
				require.NoError(t, err)
				originalClient := *client
				*client = *server.Client()
				defer func() { *client = originalClient }()
				info := newMantleTestInfo()
				info.ChannelBaseUrl, info.IsStream, info.RelayFormat = server.URL, stream, format
				recorder := httptest.NewRecorder()
				c := newAwsTestContext(recorder, context.Background())
				a := &Adaptor{}
				a.Init(info)
				var request dto.ClaudeRequest
				require.NoError(t, common.UnmarshalJsonStr(`{"model":"claude-opus-4-7","max_tokens":128,"messages":[{"role":"user","content":"hello"}],"cache_control":{"type":"ephemeral"}}`, &request))
				request.Stream = &stream
				converted, err := a.ConvertClaudeRequest(c, info, &request)
				require.NoError(t, err)
				body, err := common.Marshal(converted)
				require.NoError(t, err)
				response, err := a.DoRequest(c, info, bytes.NewReader(body))
				require.NoError(t, err)
				httpResponse, ok := response.(*http.Response)
				require.True(t, ok)
				result, apiErr := a.DoResponse(c, httpResponse, info)
				require.Nil(t, apiErr)
				usage, ok := result.(*dto.Usage)
				require.True(t, ok)
				require.NotNil(t, usage.BillingUsage)
				require.NotNil(t, usage.BillingUsage.ClaudeUsage)
				claudeUsage := usage.BillingUsage.ClaudeUsage
				assert.Equal(t, 100, claudeUsage.InputTokens)
				assert.Equal(t, 23, claudeUsage.OutputTokens)
				assert.Equal(t, 1000, claudeUsage.CacheReadInputTokens)
				assert.Equal(t, 50, claudeUsage.CacheCreationInputTokens)
				assert.Equal(t, 20, claudeUsage.GetCacheCreation5mTokens())
				assert.Equal(t, 30, claudeUsage.GetCacheCreation1hTokens())
				assert.Contains(t, recorder.Body.String(), "hello")
				if stream && format == relaytypes.RelayFormatOpenAI {
					assert.Contains(t, recorder.Body.String(), "[DONE]")
				}
			})
		}
	}
}

func TestMantleDoesNotFollowRedirectsOrFallbackOnAuthFailure(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/credential-leak")
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
			require.NoError(t, err)
			originalClient := *client
			*client = *server.Client()
			defer func() { *client = originalClient }()
			info := newMantleTestInfo()
			info.ChannelBaseUrl = server.URL
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			response, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{"messages":[]}`))
			require.NoError(t, err)
			httpResponse := response.(*http.Response)
			defer httpResponse.Body.Close()
			assert.Equal(t, status, httpResponse.StatusCode)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestAWSLegacyInterfaceRemainsDefault(t *testing.T) {
	for _, mode := range []dto.AwsApiType{"", dto.AwsApiTypeInvokeModel} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, stream), func(t *testing.T) {
				info := newMantleTestInfo()
				info.ChannelOtherSettings.AwsApiType = mode
				info.IsStream = stream
				info.UpstreamModelName = "claude-3-5-sonnet-20240620"
				c := newAwsTestContext(httptest.NewRecorder(), context.Background())
				a := &Adaptor{}
				a.Init(info)
				response, err := a.DoRequest(c, info, strings.NewReader(`{"max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}],"cache_control":{"type":"ephemeral"}}`))
				require.NoError(t, err)
				assert.Nil(t, response)
				var body []byte
				if stream {
					input, ok := a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput)
					require.True(t, ok)
					body = input.Body
					assert.Equal(t, "us."+awsTestModel, *input.ModelId)
				} else {
					input, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput)
					require.True(t, ok)
					body = input.Body
					assert.Equal(t, "us."+awsTestModel, *input.ModelId)
				}
				var payload map[string]json.RawMessage
				require.NoError(t, common.Unmarshal(body, &payload))
				assert.Equal(t, `"bedrock-2023-05-31"`, string(payload["anthropic_version"]))
				assert.NotContains(t, payload, "cache_control")
				assert.Contains(t, string(payload["messages"]), "cache_control")
			})
		}
	}
}

func TestMantleRejectsInvalidRequestsAndPreservesExplicitModelIDs(t *testing.T) {
	for _, test := range []struct {
		name, model, body, wantModel string
		invalid                      bool
	}{
		{name: "legacy Haiku alias", model: "claude-haiku-4-5-20251001", body: `{"messages":[]}`, wantModel: "anthropic.claude-haiku-4-5"},
		{name: "short Haiku alias", model: "claude-haiku-4-5", body: `{"messages":[]}`, wantModel: "anthropic.claude-haiku-4-5"},
		{name: "explicit Haiku model", model: "anthropic.claude-haiku-4-5", body: `{"messages":[]}`},
		{name: "explicit Haiku inference profile", model: "us.anthropic.claude-haiku-4-5", body: `{"messages":[]}`},
		{name: "explicit regional model", model: "us.anthropic.claude-opus-4-7", body: `{"messages":[]}`},
		{name: "explicit model", model: "anthropic.claude-opus-4-7", body: `{"messages":[]}`},
		{name: "Nova requires legacy", model: "nova-pro-v1:0", body: `{"messages":[]}`, invalid: true},
		{name: "null body", model: "claude-opus-4-7", body: `null`, invalid: true},
		{name: "malformed JSON", model: "claude-opus-4-7", body: `{`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := newMantleTestInfo()
			info.UpstreamModelName = test.model
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			req, err := (&Adaptor{}).newMantleRequest(c, info, strings.NewReader(test.body))
			if test.invalid {
				require.Error(t, err)
				assert.Nil(t, req)
				return
			}
			require.NoError(t, err)
			defer req.Body.Close()
			var payload map[string]any
			require.NoError(t, common.DecodeJson(req.Body, &payload))
			wantModel := test.wantModel
			if wantModel == "" {
				wantModel = test.model
			}
			assert.Equal(t, wantModel, payload["model"])
			assert.NotContains(t, payload, "cache_control")
		})
	}
}

func TestMantleSignatureMatchesWireHeaders(t *testing.T) {
	for _, test := range []struct {
		name          string
		http2         bool
		allHopHeaders bool
	}{
		{name: "HTTP2 keep-alive", http2: true},
		{name: "HTTP1 hop-by-hop headers", allHopHeaders: true},
		{name: "HTTP2 hop-by-hop headers", http2: true, allHopHeaders: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			type receivedRequest struct {
				request *http.Request
				body    []byte
				err     error
			}
			received := make(chan receivedRequest, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				received <- receivedRequest{request: r.Clone(context.Background()), body: body, err: err}
				w.WriteHeader(http.StatusOK)
			}))
			server.EnableHTTP2 = test.http2
			server.StartTLS()
			defer server.Close()
			client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
			require.NoError(t, err)
			originalClient := *client
			*client = *server.Client()
			defer func() { *client = originalClient }()

			info := newMantleTestInfo()
			info.ChannelBaseUrl = server.URL
			info.ApiKey = "access-key|secret-key|us-east-1"
			info.UseRuntimeHeadersOverride = true
			info.RuntimeHeadersOverride = map[string]any{
				"Connection":     "keep-alive",
				"anthropic-beta": "test-beta",
				"X-Test":         "end-to-end",
			}
			if test.allHopHeaders {
				maps.Copy(info.RuntimeHeadersOverride, map[string]any{
					"Connection":          "keep-alive, x-hop-token",
					"X-Hop-Token":         "hop-only",
					"Keep-Alive":          "timeout=5",
					"Proxy-Connection":    "keep-alive",
					"Proxy-Authenticate":  "Basic",
					"Proxy-Authorization": "Basic test-proxy-token",
					"Te":                  "trailers",
					"Trailer":             "X-Checksum",
					"Transfer-Encoding":   "chunked",
					"Upgrade":             "websocket",
				})
			}
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			response, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":128,"cache_control":{"type":"ephemeral","ttl":"1h"}}`))
			require.NoError(t, err)
			httpResponse, ok := response.(*http.Response)
			require.True(t, ok)
			defer httpResponse.Body.Close()
			wire := <-received
			require.NoError(t, wire.err)
			wantProtocol := 1
			if test.http2 {
				wantProtocol = 2
			}
			require.Equal(t, wantProtocol, wire.request.ProtoMajor)
			for _, name := range []string{"Connection", "X-Hop-Token", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
				assert.Empty(t, wire.request.Header.Values(name), "%s must not reach the upstream", name)
			}
			assert.Equal(t, "test-beta", wire.request.Header.Get("anthropic-beta"))
			assert.Equal(t, "end-to-end", wire.request.Header.Get("X-Test"))

			// Recompute the signature from only the bytes and signed headers that
			// reached the server, not from the pre-transport request object.
			authorization := wire.request.Header.Get("Authorization")
			_, signedHeaders, found := strings.Cut(authorization, "SignedHeaders=")
			require.True(t, found)
			signedHeaders, _, found = strings.Cut(signedHeaders, ",")
			require.True(t, found)
			verification := wire.request.Clone(context.Background())
			verification.URL.Scheme = "https"
			verification.URL.Host = wire.request.Host
			verification.Header = make(http.Header)
			for name := range strings.SplitSeq(signedHeaders, ";") {
				if values, ok := wire.request.Header[http.CanonicalHeaderKey(name)]; ok {
					verification.Header[http.CanonicalHeaderKey(name)] = values
				}
			}
			signedAt, err := time.Parse("20060102T150405Z", wire.request.Header.Get("X-Amz-Date"))
			require.NoError(t, err)
			hash := sha256.Sum256(wire.body)
			err = v4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: "access-key", SecretAccessKey: "secret-key"}, verification, hex.EncodeToString(hash[:]), "bedrock-mantle", "us-east-1", signedAt)
			require.NoError(t, err)
			assert.Equal(t, authorization, verification.Header.Get("Authorization"))
		})
	}
}

func TestAWSLegacyHaikuMappingPreservesVersionAndRegion(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   dto.AwsApiType
		stream bool
		key    string
	}{
		{name: "default API key", key: "test-api-key|us-east-1"},
		{name: "default API key stream", stream: true, key: "test-api-key|us-east-1"},
		{name: "default AK/SK", key: "access-key|secret-key|us-east-1"},
		{name: "default AK/SK stream", stream: true, key: "access-key|secret-key|us-east-1"},
		{name: "explicit API key", mode: dto.AwsApiTypeInvokeModel, key: "test-api-key|us-east-1"},
		{name: "explicit API key stream", mode: dto.AwsApiTypeInvokeModel, stream: true, key: "test-api-key|us-east-1"},
		{name: "explicit AK/SK", mode: dto.AwsApiTypeInvokeModel, key: "access-key|secret-key|us-east-1"},
		{name: "explicit AK/SK stream", mode: dto.AwsApiTypeInvokeModel, stream: true, key: "access-key|secret-key|us-east-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := newMantleTestInfo()
			info.ChannelOtherSettings.AwsApiType = test.mode
			info.ApiKey, info.IsStream = test.key, test.stream
			info.UpstreamModelName = "claude-haiku-4-5-20251001"
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			a := &Adaptor{}
			a.Init(info)
			response, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":128}`))
			require.NoError(t, err)
			assert.Nil(t, response)
			const wantModel = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
			if test.stream {
				request, ok := a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput)
				require.True(t, ok)
				assert.Equal(t, wantModel, *request.ModelId)
			} else {
				request, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput)
				require.True(t, ok)
				assert.Equal(t, wantModel, *request.ModelId)
			}
		})
	}
}

func TestMantleCanceledRequestSkipsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newAwsTestContext(httptest.NewRecorder(), ctx)
	response, err := (&Adaptor{}).DoRequest(c, newMantleTestInfo(), strings.NewReader(`{"messages":[]}`))
	require.Error(t, err)
	assert.Nil(t, response)
	var apiError *relaytypes.NewAPIError
	require.ErrorAs(t, err, &apiError)
	assert.True(t, relaytypes.IsSkipRetryError(apiError))
}

func TestAWSApiKeyTypeInvokesThroughSDKWithBearerToken(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			info := newMantleTestInfo()
			info.ChannelOtherSettings.AwsApiType = ""
			info.ChannelOtherSettings.AwsKeyType = dto.AwsKeyTypeApiKey
			info.IsStream = stream
			info.UpstreamModelName = "claude-3-5-sonnet-20240620"
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			a := &Adaptor{}
			a.Init(info)

			requestURL, err := a.GetRequestURL(info)
			require.NoError(t, err)
			assert.Empty(t, requestURL)

			response, err := a.DoRequest(c, info, strings.NewReader(`{"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`))
			require.NoError(t, err)
			assert.Nil(t, response)
			require.NotNil(t, a.AwsClient)

			var seen *http.Request
			capture := func(options *bedrockruntime.Options) {
				options.Retryer = aws.NopRetryer{}
				options.HTTPClient = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
					seen = request
					return nil, errors.New("captured")
				})
			}
			if stream {
				input, ok := a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput)
				require.True(t, ok)
				_, _ = a.AwsClient.InvokeModelWithResponseStream(context.Background(), input, capture)
			} else {
				input, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput)
				require.True(t, ok)
				_, _ = a.AwsClient.InvokeModel(context.Background(), input, capture)
			}
			require.NotNil(t, seen)
			action := "invoke"
			if stream {
				action = "invoke-with-response-stream"
			}
			assert.Equal(t, "bedrock-runtime.us-east-1.amazonaws.com", seen.URL.Host)
			assert.Equal(t, "/model/us."+awsTestModel+"/"+action, seen.URL.Path)
			assert.Equal(t, "Bearer test-api-key", seen.Header.Get("Authorization"))
		})
	}
}
