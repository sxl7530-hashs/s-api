package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/alicebob/miniredis/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
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
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	previousMedia, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() {
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
			assert.Equal(t, 1, calls)
			assert.Equal(t, "1", server.HGet("bedrock:usage:v2:{98}", "rpm"))
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
					server.FlushAll()
					info := newAwsTestRelayInfo()
					info.ApiKey, info.ChannelId, info.IsStream = key, 97, stream
					info.AwsPassthroughCompatibility = mode == "compatibility"
					info.UseRuntimeHeadersOverride = true
					info.RuntimeHeadersOverride = map[string]any{
						"X-Channel-Tag": "custom-value", "Authorization": "invalid", "X-Amz-Date": "invalid", "Host": "invalid",
						"x-amzn-bedrock-trace": "DISABLED",
					}
					if mode != "off" {
						info.ChannelSetting.BedrockProtection = &dto.BedrockProtectionSettings{UsageMetricsEnabled: true, RateLimitEnabled: mode == "protection", RPM: 100}
					}
					c := newAwsTestContext(httptest.NewRecorder(), t.Context())
					c.Request.Header.Set("X-Amzn-Bedrock-Trace", "ENABLED")
					c.Request.Header.Set("X-Amzn-Bedrock-GuardrailIdentifier", "test-guardrail")
					c.Request.Header.Set("X-Client-Secret", "must-not-forward")
					a := &Adaptor{}
					_, err := a.DoRequest(c, info, strings.NewReader(`{"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`))
					require.NoError(t, err)
					tokens := 0
					if mode != "off" {
						tokens, err = a.estimateProtectionTokens(c, info)
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
					if mode == "off" {
						assert.Empty(t, server.Keys(), "disabled metrics must not write Redis")
					} else {
						assert.Equal(t, fmt.Sprint(attempts), server.HGet("bedrock:usage:v2:{97}", "rpm"))
						assert.Equal(t, fmt.Sprint(attempts*tokens), server.HGet("bedrock:usage:v2:{97}", "tpm"))
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
	adaptor := &Adaptor{ClientMode: ClientModeApiKey}
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
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
	})
	for _, tc := range []struct {
		name      string
		partial   bool
		throttled bool
	}{{"initial throttle", false, true}, {"partial throttle", true, true}, {"truncated stream", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			server.FlushAll()
			info := newAwsTestRelayInfo()
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

	handlerErr, usage := awsStreamHandler(c, newAwsTestRelayInfo(), adaptor)

	require.Nil(t, handlerErr)
	require.NotNil(t, usage)
	require.NotNil(t, usage.BillingUsage)
	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	assert.Equal(t, 423, usage.BillingUsage.ClaudeUsage.OutputTokens)
	assert.Contains(t, recorder.Body.String(), "[DONE]")
}

func TestAwsStreamHandlerStopsAtClientCancellation(t *testing.T) {
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
