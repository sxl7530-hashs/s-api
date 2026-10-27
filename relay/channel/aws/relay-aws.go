package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockruntimeTypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/auth/bearer"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// getAwsErrorStatusCode extracts HTTP status code from AWS SDK error
func getAwsErrorStatusCode(err error) int {
	// Check for HTTP response error which contains status code
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode()
	}
	// Default to 500 if we can't determine the status code
	return http.StatusInternalServerError
}

func newAwsInvokeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if common.RelayTimeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(common.RelayTimeout)*time.Second)
}

func newAwsInvokeError(requestContext context.Context, err error, operation string) *types.NewAPIError {
	options := make([]types.NewAPIErrorOptions, 0, 1)
	if requestContext.Err() != nil {
		options = append(options, types.ErrOptionWithSkipRetry())
	}
	return types.NewOpenAIError(
		errors.Wrap(err, operation),
		types.ErrorCodeAwsInvokeError,
		getAwsErrorStatusCode(err),
		options...,
	)
}

func bedrockProtectionError(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) *types.NewAPIError {
	settings := info.ChannelSetting.BedrockProtection
	if service.IsBedrockCoolingDown(info.ChannelId, settings) {
		return types.NewErrorWithStatusCode(errors.New("Bedrock channel is cooling down"), types.ErrorCodeChannelBedrockCoolingDown, http.StatusTooManyRequests)
	}
	// A negative estimate records RPM while marking this window's TPM unknown.
	tokens := -1
	if settings != nil && common.RedisEnabled && common.RDB != nil {
		modelLimit := settings.ModelLimits[info.UpstreamModelName]
		requiresTokens := settings.RateLimitEnabled && settings.TPM > 0 || settings.ModelRateLimitEnabled && modelLimit.TPM > 0
		if settings.UsageMetricsEnabled || requiresTokens {
			var err error
			tokens, err = a.estimateProtectionTokens(c, info)
			if err != nil {
				if requiresTokens {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
				}
				tokens = -1
			}
		}
	}
	if settings != nil && settings.UsageMetricsEnabled && !settings.RateLimitEnabled && !settings.ModelRateLimitEnabled {
		// Display-only metrics follow physical HTTP attempts, including SDK
		// retries. Do not change the SDK's retry policy just to collect metrics.
		a.AwsClient = bedrockruntime.New(a.AwsClient.Options(), func(options *bedrockruntime.Options) {
			client := options.HTTPClient
			if previous, ok := client.(*bedrockUsageHTTPClient); ok {
				client = previous.client
			}
			options.HTTPClient = &bedrockUsageHTTPClient{client: client, channelID: info.ChannelId, tokens: tokens, modelName: info.UpstreamModelName}
		})
		return nil
	}
	allowed, _ := service.ReserveBedrockUsage(info.ChannelId, tokens, settings, info.UpstreamModelName)
	if !allowed {
		return types.NewErrorWithStatusCode(errors.New("Bedrock channel rate limit reached"), types.ErrorCodeChannelBedrockRateLimited, http.StatusTooManyRequests)
	}
	return nil
}

// bedrockUsageHTTPClient records each actual send without altering retries or
// mutating the shared transport. TPM is the same conservative token estimate
// used by channel protection, rather than billed token usage.
type bedrockUsageHTTPClient struct {
	client    aws.HTTPClient
	channelID int
	tokens    int
	modelName string
}

func (client *bedrockUsageHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	service.ReserveBedrockUsage(client.channelID, client.tokens, &dto.BedrockProtectionSettings{UsageMetricsEnabled: true}, client.modelName)
	return client.client.Do(request)
}

type bedrockChannelHeaders map[string]string

func (bedrockChannelHeaders) ID() string { return "BedrockChannelHeaders" }

func (headers bedrockChannelHeaders) HandleBuild(ctx context.Context, input middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
	request, ok := input.Request.(*smithyhttp.Request)
	if !ok {
		return middleware.BuildOutput{}, middleware.Metadata{}, errors.New("unexpected Bedrock HTTP request type")
	}
	for key, value := range headers {
		if isAwsManagedOrForwardingHeader(key) {
			continue
		}
		request.Header.Set(key, value)
	}
	return next.HandleBuild(ctx, input)
}

func isAwsManagedOrForwardingHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "x-forwarded-") {
		return true
	}
	switch key {
	case "authorization", "host", "x-amz-date", "x-amz-security-token", "x-amz-content-sha256", "content-length", "transfer-encoding", "x-real-ip", "forwarded":
		return true
	default:
		return false
	}
}

// estimateProtectionTokens uses the actual outgoing body, after conversion,
// defaults, passthrough and overrides. Protection must not depend on billing's
// optional input estimation or overwrite its prompt-token context.
func (a *Adaptor) estimateProtectionTokens(c *gin.Context, info *relaycommon.RelayInfo) (int, error) {
	var body []byte
	switch request := a.AwsReq.(type) {
	case *bedrockruntime.InvokeModelInput:
		body = request.Body
	case *bedrockruntime.InvokeModelWithResponseStreamInput:
		body = request.Body
	default:
		return 0, errors.New("unsupported Bedrock request body")
	}
	var request dto.ClaudeRequest
	if isNovaModel(getAwsModelID(info.UpstreamModelName)) {
		var nova NovaRequest
		if err := common.Unmarshal(body, &nova); err != nil {
			return 0, err
		}
		// Nova's omitted maximum is dynamic, up to 5K output tokens.
		// Reserve that upper bound without changing the upstream payload.
		maxTokens := uint(5000)
		if nova.InferenceConfig != nil && nova.InferenceConfig.MaxTokens != 0 {
			if nova.InferenceConfig.MaxTokens < 0 {
				return 0, errors.New("invalid Bedrock maxTokens")
			}
			maxTokens = uint(nova.InferenceConfig.MaxTokens)
		}
		request.MaxTokens = &maxTokens
		for _, message := range nova.Messages {
			var text strings.Builder
			for _, content := range message.Content {
				text.WriteString(content.Text)
				text.WriteByte('\n')
			}
			request.Messages = append(request.Messages, dto.ClaudeMessage{Role: message.Role, Content: text.String()})
		}
	} else if err := common.Unmarshal(body, &request); err != nil {
		return 0, err
	}
	if helper.ExceedsMaxTokensLimit(request.MaxTokens) {
		return 0, errors.New("Bedrock max_tokens exceeds the supported limit")
	}
	countContext := c.Copy()
	// The copy borrows cached data from the request, but owns a separate
	// cleanup registry. Transfer new sources back even when counting fails.
	cleanupKey := string(constant.ContextKeyFileSourcesToCleanup)
	countContext.Set(cleanupKey, []types.FileSource(nil))
	common.SetContextKey(countContext, constant.ContextKeyOriginalModel, info.UpstreamModelName)
	meta := request.GetTokenCountMeta()
	countInfo := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: info.IsStream}
	promptTokens, err := service.CountRequestToken(countContext, meta, countInfo)
	if registered, ok := countContext.Get(cleanupKey); ok {
		sources, _ := registered.([]types.FileSource)
		if len(sources) > 0 {
			existing, _ := c.Get(cleanupKey)
			original, _ := existing.([]types.FileSource)
			c.Set(cleanupKey, append(original, sources...))
		}
	}
	if err != nil {
		return 0, err
	}
	return min(max(promptTokens, 0), int(^uint(0)>>1)-meta.MaxTokens) + meta.MaxTokens, nil
}

func handleAwsInvokeResult(requestContext context.Context, info *relaycommon.RelayInfo, err error, operation string) *types.NewAPIError {
	settings := info.ChannelSetting.BedrockProtection
	if err == nil {
		service.RecordBedrockNonThrottle(info.ChannelId, settings)
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ThrottlingException" {
		service.RecordBedrockThrottle(info.ChannelId, settings)
		if settings != nil && settings.ThrottlingCooldownEnabled {
			options := make([]types.NewAPIErrorOptions, 0, 1)
			if requestContext.Err() != nil {
				options = append(options, types.ErrOptionWithSkipRetry())
			}
			return types.NewOpenAIError(errors.Wrap(err, operation), types.ErrorCodeBedrockThrottled, http.StatusTooManyRequests, options...)
		}
	}
	service.RecordBedrockNonThrottle(info.ChannelId, settings)
	return newAwsInvokeError(requestContext, err, operation)
}

func newAwsClient(c *gin.Context, info *relaycommon.RelayInfo) (*bedrockruntime.Client, error) {
	httpClient, err := service.GetHttpClientWithProxySettings(info.ChannelSetting.Proxy, info.ChannelSetting)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}

	awsSecret := strings.Split(info.ApiKey, "|")
	var client *bedrockruntime.Client
	switch len(awsSecret) {
	case 2:
		apiKey := awsSecret[0]
		region := awsSecret[1]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:                  region,
			BearerAuthTokenProvider: bearer.StaticTokenProvider{Token: bearer.Token{Value: apiKey}},
			HTTPClient:              httpClient,
		})
	case 3:
		ak := awsSecret[0]
		sk := awsSecret[1]
		region := awsSecret[2]
		client = bedrockruntime.New(bedrockruntime.Options{
			Region:      region,
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(ak, sk, "")),
			HTTPClient:  httpClient,
		})
	default:
		return nil, errors.New("invalid aws secret key")
	}
	// SDK retries would send unreserved requests and hide intermediate 429s.
	// Optional protection delegates retries to the gateway's existing budget.
	p := info.ChannelSetting.BedrockProtection
	if p != nil && (p.RateLimitEnabled || p.ModelRateLimitEnabled || p.ThrottlingCooldownEnabled || p.RetryBackoffEnabled) {
		client = bedrockruntime.New(client.Options(), func(options *bedrockruntime.Options) {
			options.Retryer = aws.NopRetryer{}
		})
	}

	return client, nil
}

func doAwsClientRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor, requestBody io.Reader) (any, error) {
	awsCli, err := newAwsClient(c, info)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelAwsClientError)
	}
	a.AwsClient = awsCli

	// 获取对应的AWS模型ID
	awsModelId := getAwsModelID(info.UpstreamModelName)

	awsRegionPrefix := getAwsRegionPrefix(awsCli.Options().Region)
	canCrossRegion := awsModelCanCrossRegion(awsModelId, awsRegionPrefix)
	if canCrossRegion {
		awsModelId = awsModelCrossRegion(awsModelId, awsRegionPrefix)
	}

	// init empty request.header
	requestHeader := http.Header{}
	a.SetupRequestHeader(c, &requestHeader, info)
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	if info.AwsPassthroughCompatibility {
		// Forward only Bedrock operation controls, never client credentials or
		// transport headers. Explicit administrator overrides take precedence.
		forwarded := make(map[string]string)
		for _, key := range []string{"X-Amzn-Bedrock-GuardrailIdentifier", "X-Amzn-Bedrock-GuardrailVersion", "X-Amzn-Bedrock-Trace", "X-Amzn-Bedrock-PerformanceConfig-Latency", "X-Amzn-Bedrock-Request-Metadata"} {
			if value := c.GetHeader(key); value != "" {
				forwarded[http.CanonicalHeaderKey(key)] = value
			}
		}
		for key, value := range headerOverride {
			forwarded[http.CanonicalHeaderKey(key)] = value
		}
		headerOverride = forwarded
	}
	for key, value := range headerOverride {
		if isAwsManagedOrForwardingHeader(key) {
			continue
		}
		requestHeader.Set(key, value)
	}
	if len(headerOverride) > 0 {
		// Apply custom headers before SigV4 signing. Identity and wire framing
		// remain owned by the SDK for both bearer and AK/SK credentials.
		a.AwsClient = bedrockruntime.New(awsCli.Options(), func(options *bedrockruntime.Options) {
			options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error {
				return stack.Build.Add(bedrockChannelHeaders(headerOverride), middleware.After)
			})
		})
	}

	if isNovaModel(awsModelId) {
		if info.AwsPassthroughCompatibility {
			if info.IsStream {
				return nil, types.NewErrorWithStatusCode(errors.New("Nova streaming is not supported by this adaptor"), types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			body, bodyErr := prepareAwsCompatibilityBody(c, info, requestBody, requestHeader, true)
			if bodyErr != nil {
				return nil, types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			a.IsNova = true
			a.AwsReq = &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsModelId), Accept: aws.String("application/json"), ContentType: aws.String("application/json"), Body: body}
			return nil, nil
		}
		var novaReq *NovaRequest
		err = common.DecodeJson(requestBody, &novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "decode nova request fail"), types.ErrorCodeBadRequestBody)
		}

		// 使用InvokeModel API，但使用Nova格式的请求体
		awsReq := &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(awsModelId),
			Accept:      aws.String("application/json"),
			ContentType: aws.String("application/json"),
		}

		reqBody, err := common.Marshal(novaReq)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "marshal nova request"), types.ErrorCodeBadResponseBody)
		}
		awsReq.Body = reqBody
		a.AwsReq = awsReq
		return nil, nil
	} else {
		if info.AwsPassthroughCompatibility {
			body, bodyErr := prepareAwsCompatibilityBody(c, info, requestBody, requestHeader, false)
			if bodyErr != nil {
				return nil, types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			if info.IsStream {
				a.AwsReq = &bedrockruntime.InvokeModelWithResponseStreamInput{ModelId: aws.String(awsModelId), Accept: aws.String("application/json"), ContentType: aws.String("application/json"), Body: body}
			} else {
				a.AwsReq = &bedrockruntime.InvokeModelInput{ModelId: aws.String(awsModelId), Accept: aws.String("application/json"), ContentType: aws.String("application/json"), Body: body}
			}
			return nil, nil
		}
		awsClaudeReq, err := formatRequest(requestBody, requestHeader)
		if err != nil {
			return nil, types.NewError(errors.Wrap(err, "format aws request fail"), types.ErrorCodeBadRequestBody)
		}

		if info.IsStream {
			awsReq := &bedrockruntime.InvokeModelWithResponseStreamInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		} else {
			awsReq := &bedrockruntime.InvokeModelInput{
				ModelId:     aws.String(awsModelId),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
			}
			awsReq.Body, err = buildAwsRequestBody(c, info, awsClaudeReq)
			if err != nil {
				return nil, types.NewError(errors.Wrap(err, "marshal aws request fail"), types.ErrorCodeBadRequestBody)
			}
			a.AwsReq = awsReq
			return nil, nil
		}
	}
}

// prepareAwsCompatibilityBody patches only envelope fields. RawMessage preserves
// unknown nested provider fields and numeric precision on native Claude requests.
// OpenAI requests arrive already converted and must never be replaced by ingress JSON.
func prepareAwsCompatibilityBody(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader, headers http.Header, nova bool) ([]byte, error) {
	var data map[string]json.RawMessage
	if err := common.DecodeJson(body, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("expected JSON object request body")
	}
	delete(data, "model")
	delete(data, "stream")
	if info.AwsCompatibilityConverted && !nova {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, err
		}
		original, err := storage.Bytes()
		if err != nil {
			return nil, err
		}
		var extensions map[string]json.RawMessage
		if err := common.Unmarshal(original, &extensions); err != nil {
			return nil, err
		}
		// These are native provider controls with no OpenAI equivalent. Never
		// merge messages, model, output limits or converted sampling fields.
		for _, key := range []string{"anthropic_beta", "context_management", "output_config", "metadata"} {
			if _, exists := data[key]; !exists && len(extensions[key]) > 0 {
				data[key] = extensions[key]
			}
		}
	}
	if nova {
		var config struct {
			MaxTokens *uint `json:"maxTokens"`
		}
		if raw, exists := data["inferenceConfig"]; exists {
			if err := common.Unmarshal(raw, &config); err != nil {
				return nil, err
			}
		}
		if helper.ExceedsMaxTokensLimit(config.MaxTokens) {
			return nil, errors.New("maxTokens is invalid")
		}
		var schema string
		if err := common.Unmarshal(data["schemaVersion"], &schema); err != nil || schema != "messages-v1" {
			return nil, errors.New("Nova requires messages-v1 request format")
		}
	} else {
		data["anthropic_version"] = json.RawMessage(`"bedrock-2023-05-31"`)
		var req dto.ClaudeRequest
		encoded, err := common.Marshal(data)
		if err != nil {
			return nil, err
		}
		if err := common.Unmarshal(encoded, &req); err != nil {
			return nil, err
		}
		if helper.ExceedsMaxTokensLimit(req.MaxTokens, req.MaxTokensToSample) {
			return nil, errors.New("max_tokens is invalid")
		}
		// Bedrock does not accept URL image sources. Patch only those sources
		// through the shared media loader, retaining all other block fields.
		var messages []map[string]json.RawMessage
		if err := common.Unmarshal(data["messages"], &messages); err != nil {
			return nil, err
		}
		for _, message := range messages {
			raw := message["content"]
			if common.GetJsonType(raw) != "array" {
				continue
			}
			var blocks []map[string]json.RawMessage
			if err := common.Unmarshal(raw, &blocks); err != nil {
				return nil, err
			}
			changed := false
			for _, block := range blocks {
				var kind string
				if err := common.Unmarshal(block["type"], &kind); err != nil || kind != "image" {
					continue
				}
				var source struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				}
				if err := common.Unmarshal(block["source"], &source); err != nil || source.Type != "url" {
					continue
				}
				base64Data, mimeType, err := service.GetBase64Data(c, types.NewURLFileSource(source.URL), "formatting image for Claude")
				if err != nil {
					return nil, err
				}
				block["source"], err = common.Marshal(map[string]string{"type": "base64", "media_type": mimeType, "data": base64Data})
				if err != nil {
					return nil, err
				}
				changed = true
			}
			if changed {
				message["content"], err = common.Marshal(blocks)
				if err != nil {
					return nil, err
				}
			}
		}
		data["messages"], err = common.Marshal(messages)
		if err != nil {
			return nil, err
		}
		if req.MaxTokens == nil {
			maxTokens := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(info.UpstreamModelName))
			if helper.ExceedsMaxTokensLimit(&maxTokens) {
				return nil, errors.New("max_tokens is invalid")
			}
			data["max_tokens"], err = common.Marshal(maxTokens)
			if err != nil {
				return nil, err
			}
		}
		var betas []string
		if raw, exists := data["anthropic_beta"]; exists {
			if err := common.Unmarshal(raw, &betas); err != nil {
				return nil, err
			}
		}
		for beta := range strings.SplitSeq(headers.Get("anthropic-beta"), ",") {
			beta = strings.TrimSpace(beta)
			if beta != "" && !slices.Contains(betas, beta) {
				betas = append(betas, beta)
			}
		}
		if len(betas) > 0 {
			data["anthropic_beta"], err = common.Marshal(betas)
			if err != nil {
				return nil, err
			}
		}
	}
	return common.Marshal(data)
}

// buildAwsRequestBody prepares the payload for AWS requests, applying passthrough rules when enabled.
func buildAwsRequestBody(c *gin.Context, info *relaycommon.RelayInfo, awsClaudeReq any) ([]byte, error) {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, errors.Wrap(err, "get request body for pass-through fail")
		}
		body, err := storage.Bytes()
		if err != nil {
			return nil, errors.Wrap(err, "get request body bytes fail")
		}
		// Nested values keep the client's bytes and key order.
		var data map[string]json.RawMessage
		if err := common.Unmarshal(body, &data); err != nil {
			return nil, errors.Wrap(err, "pass-through unmarshal request body fail")
		}
		delete(data, "model")
		delete(data, "stream")
		return common.Marshal(data)
	}
	return common.Marshal(awsClaudeReq)
}

func getAwsRegionPrefix(awsRegionId string) string {
	parts := strings.Split(awsRegionId, "-")
	regionPrefix := ""
	if len(parts) > 0 {
		regionPrefix = parts[0]
	}
	return regionPrefix
}

func awsModelCanCrossRegion(awsModelId, awsRegionPrefix string) bool {
	regionSet, exists := awsModelCanCrossRegionMap[awsModelId]
	return exists && regionSet[awsRegionPrefix]
}

func awsModelCrossRegion(awsModelId, awsRegionPrefix string) string {
	modelPrefix, find := awsRegionCrossModelPrefixMap[awsRegionPrefix]
	if !find {
		return awsModelId
	}
	return modelPrefix + "." + awsModelId
}

func getAwsModelID(requestModel string) string {
	if awsModelIDName, ok := awsModelIDMap[requestModel]; ok {
		return awsModelIDName
	}
	return requestModel
}

func awsHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	if err := bedrockProtectionError(c, info, a); err != nil {
		return err, nil
	}

	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput))
	if invokeErr := handleAwsInvokeResult(requestContext, info, err, "InvokeModel"); invokeErr != nil {
		return invokeErr, nil
	}

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	// 复制上游 Content-Type 到客户端响应头
	if awsResp.ContentType != nil && *awsResp.ContentType != "" {
		c.Writer.Header().Set("Content-Type", *awsResp.ContentType)
	}

	handlerErr := claude.HandleClaudeResponseData(c, info, claudeInfo, nil, awsResp.Body)
	if handlerErr != nil {
		return handlerErr, nil
	}
	return nil, claudeInfo.Usage
}

func awsStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	if err := bedrockProtectionError(c, info, a); err != nil {
		return err, nil
	}
	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModelWithResponseStream(ctx, a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput))
	if err != nil {
		return handleAwsInvokeResult(requestContext, info, err, "InvokeModelWithResponseStream"), nil
	}
	stream := awsResp.GetStream()
	defer stream.Close()
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RequireTerminal()

	claudeInfo := &claude.ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}

	events := stream.Events()
	var responseErr *types.NewAPIError
streamLoop:
	for {
		select {
		case <-ctx.Done():
			break streamLoop
		case event, ok := <-events:
			if !ok {
				break streamLoop
			}
			if ctx.Err() != nil {
				break streamLoop
			}

			switch v := event.(type) {
			case *bedrockruntimeTypes.ResponseStreamMemberChunk:
				info.SetFirstResponseTime()
				respErr := claude.HandleStreamResponseData(c, info, claudeInfo, string(v.Value.Bytes))
				if respErr != nil {
					responseErr = respErr
					break streamLoop
				}
			case *bedrockruntimeTypes.UnknownUnionMember:
				fmt.Println(common.LogText("unknown tag: %v", v.Tag))
				responseErr = types.NewError(errors.New("unknown response type"), types.ErrorCodeInvalidRequest)
				break streamLoop
			default:
				fmt.Println(common.LogText("union is nil or unknown type"))
				responseErr = types.NewError(errors.New("nil or unknown response type"), types.ErrorCodeInvalidRequest)
				break streamLoop
			}
		}
	}

	if requestContext.Err() != nil {
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, requestContext.Err())
		info.StreamStatus.MarkCancelled()
	} else {
		streamErr := stream.Err()
		if streamErr == nil {
			streamErr = ctx.Err()
		}
		if responseErr == nil && streamErr != nil {
			responseErr = handleAwsInvokeResult(requestContext, info, streamErr, "InvokeModelWithResponseStream")
		}
		if responseErr == nil && info.StreamStatus.ResponseOutcome() != string(relaycommon.ResponseOutcomeCompleted) {
			responseErr = newAwsInvokeError(requestContext, io.ErrUnexpectedEOF, "InvokeModelWithResponseStream")
		}
		if responseErr != nil {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, responseErr)
			info.StreamStatus.RecordError("Bedrock response stream failed")
			info.StreamStatus.MarkFailed(string(responseErr.GetErrorCode()), string(responseErr.GetErrorType()), responseErr.StatusCode)
			if !c.Writer.Written() {
				return responseErr, nil
			}
			// The response has started: settle its observed usage without replaying
			// the request or emitting a successful terminal event.
		} else {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
			service.RecordBedrockNonThrottle(info.ChannelId, info.ChannelSetting.BedrockProtection)
		}
	}
	claude.HandleStreamFinalResponse(c, info, claudeInfo)
	return nil, claudeInfo.Usage
}

// Nova模型处理函数
func handleNovaRequest(c *gin.Context, info *relaycommon.RelayInfo, a *Adaptor) (*types.NewAPIError, *dto.Usage) {
	if err := bedrockProtectionError(c, info, a); err != nil {
		return err, nil
	}

	requestContext := c.Request.Context()
	ctx, cancel := newAwsInvokeContext(requestContext)
	defer cancel()

	awsResp, err := a.AwsClient.InvokeModel(ctx, a.AwsReq.(*bedrockruntime.InvokeModelInput))
	if invokeErr := handleAwsInvokeResult(requestContext, info, err, "InvokeModel"); invokeErr != nil {
		return invokeErr, nil
	}

	// 解析Nova响应
	var novaResp struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
			TotalTokens  int `json:"totalTokens"`
		} `json:"usage"`
	}

	if err := common.Unmarshal(awsResp.Body, &novaResp); err != nil {
		return types.NewError(errors.Wrap(err, "unmarshal nova response"), types.ErrorCodeBadResponseBody), nil
	}
	if len(novaResp.Output.Message.Content) == 0 {
		return types.NewError(errors.New("Nova response contains no message content"), types.ErrorCodeBadResponseBody), nil
	}

	// 构造OpenAI格式响应
	response := dto.OpenAITextResponse{
		Id:      helper.GetResponseID(c),
		Object:  "chat.completion",
		Created: common.GetTimestamp(),
		Model:   info.UpstreamModelName,
		Choices: []dto.OpenAITextResponseChoice{{
			Index: 0,
			Message: dto.Message{
				Role:    "assistant",
				Content: novaResp.Output.Message.Content[0].Text,
			},
			FinishReason: "stop",
		}},
		Usage: dto.Usage{
			PromptTokens:     novaResp.Usage.InputTokens,
			CompletionTokens: novaResp.Usage.OutputTokens,
			TotalTokens:      novaResp.Usage.TotalTokens,
		},
	}

	c.JSON(http.StatusOK, response)
	return nil, &response.Usage
}
