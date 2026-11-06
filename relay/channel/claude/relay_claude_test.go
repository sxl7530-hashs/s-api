package claude

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func commonPointer[T any](value T) *T {
	return &value
}

type claudeFailingFinalWriter struct {
	*httptest.ResponseRecorder
}

func (w *claudeFailingFinalWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte("event: response.completed")) {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}

func (w *claudeFailingFinalWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func TestClaudeStreamFailurePreservesPartialUsageWithoutSuccessfulEnd(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, ending := range []string{"completed", "truncated", "malformed", "upstream error", "initial error", "empty stream", "cancelled", "final write failure"} {
			if ending == "final write failure" && format != types.RelayFormatOpenAIResponses {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s", format, ending), func(t *testing.T) {
				var events []string
				if ending != "initial error" && ending != "empty stream" && ending != "cancelled" {
					events = append(events,
						`{"type":"message_start","message":{"id":"msg_test","model":"claude-test","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":1}}}`,
						`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
						`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
					)
				}
				switch ending {
				case "completed", "final write failure":
					events = append(events, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`, `{"type":"message_stop"}`)
				case "upstream error", "initial error":
					events = append(events, `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)
				case "malformed":
					events = append(events, `{"type":`)
				}
				var body strings.Builder
				for _, event := range events {
					body.WriteString("data: " + event + "\n\n")
				}
				recorder := httptest.NewRecorder()
				var writer http.ResponseWriter = recorder
				if ending == "final write failure" {
					writer = &claudeFailingFinalWriter{ResponseRecorder: recorder}
				}
				ctx, _ := gin.CreateTestContext(writer)
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				if ending == "cancelled" {
					cancelled, cancel := context.WithCancel(t.Context())
					cancel()
					ctx.Request = ctx.Request.WithContext(cancelled)
				}
				info := &relaycommon.RelayInfo{RelayFormat: format, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}
				handler := ClaudeStreamHandler
				if format == types.RelayFormatOpenAIResponses {
					handler = ClaudeResponsesStreamHandler
				}
				usage, apiErr := handler(ctx, &http.Response{Body: io.NopCloser(strings.NewReader(body.String())), Header: http.Header{}}, info)
				if ending == "cancelled" {
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					assert.Equal(t, string(relaycommon.ResponseOutcomeCancelled), info.StreamStatus.ResponseOutcome())
					assert.Empty(t, recorder.Body.String())
					return
				}
				if ending == "initial error" || ending == "empty stream" {
					if ending == "initial error" && format == types.RelayFormatOpenAIResponses {
						require.Nil(t, apiErr)
						require.NotNil(t, usage)
						assert.Contains(t, recorder.Body.String(), "event: response.failed")
						assert.Equal(t, string(relaycommon.ResponseOutcomeFailed), info.StreamStatus.ResponseOutcome())
						return
					}
					require.NotNil(t, apiErr)
					assert.Nil(t, usage)
					if ending == "empty stream" {
						assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
					}
					return
				}
				require.Nil(t, apiErr, "a partial stream must retain usage for settlement")
				require.NotNil(t, usage)
				assert.Equal(t, 100, usage.PromptTokens)
				assert.Contains(t, recorder.Body.String(), "partial")
				if ending == "completed" {
					assert.Equal(t, string(relaycommon.ResponseOutcomeCompleted), info.StreamStatus.ResponseOutcome())
					assert.Equal(t, 10, usage.CompletionTokens)
				} else {
					assert.NotContains(t, recorder.Body.String(), "[DONE]")
					assert.NotContains(t, recorder.Body.String(), "event: message_stop")
					if format == types.RelayFormatOpenAIResponses {
						assert.NotContains(t, recorder.Body.String(), "event: response.completed")
						if ending != "final write failure" {
							assert.Contains(t, recorder.Body.String(), "event: response.failed")
						}
					}
					assert.Equal(t, string(relaycommon.ResponseOutcomeFailed), info.StreamStatus.ResponseOutcome())
				}
			})
		}
	}
}

func TestClaudeResponsesToolStreamPreservesCacheAndTerminalUsage(t *testing.T) {
	for _, ending := range []string{"tool_use", "max_tokens", "truncated"} {
		t.Run(ending, func(t *testing.T) {
			events := []string{
				`{"type":"message_start","message":{"id":"msg_tool","model":"claude-test","role":"assistant","content":[],"usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":200,"cache_creation_input_tokens":50}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_read","name":"read_file","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`,
				`{"type":"content_block_stop","index":0}`,
			}
			if ending != "truncated" {
				events = append(events, fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":42}}`, ending), `{"type":"message_stop"}`)
			}
			var body strings.Builder
			for _, event := range events {
				body.WriteString("data: " + event + "\n\n")
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"}}
			usage, apiErr := ClaudeResponsesStreamHandler(ctx, &http.Response{Body: io.NopCloser(strings.NewReader(body.String())), Header: http.Header{}}, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.NotNil(t, usage.BillingUsage)
			canonical, known := usage.BillingUsage.CanonicalUsage()
			require.True(t, known)
			assert.Equal(t, 100, canonical.PromptTokens)
			assert.Equal(t, 200, canonical.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 50, canonical.PromptTokensDetails.CachedCreationTokens)
			assert.Contains(t, recorder.Body.String(), "function_call")
			assert.Contains(t, recorder.Body.String(), "read_file")
			assert.Contains(t, recorder.Body.String(), "README.md")
			switch ending {
			case "tool_use":
				assert.Equal(t, 42, canonical.CompletionTokens)
				assert.Contains(t, recorder.Body.String(), "event: response.completed")
				assert.NotContains(t, recorder.Body.String(), "event: response.failed")
			case "max_tokens":
				assert.Equal(t, 42, canonical.CompletionTokens)
				assert.Contains(t, recorder.Body.String(), "event: response.incomplete")
				assert.NotContains(t, recorder.Body.String(), "event: response.failed")
			case "truncated":
				assert.Positive(t, canonical.CompletionTokens)
				assert.Contains(t, recorder.Body.String(), "event: response.failed")
				assert.NotContains(t, recorder.Body.String(), "event: response.completed")
			}
		})
	}
}

func TestClaudeStreamFinalUsageEstimatesOnlyUncountedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, startUsage, deltaUsage, stopReason string // no deltaUsage: the stream broke before message_delta
		streamText                               bool
		wantInput, wantOutput                    int
		wantLocalCount                           bool
	}{
		{name: "cache-only refusal keeps zeros", startUsage: `{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":20000,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":20000}}`, deltaUsage: `{"output_tokens":0}`, stopReason: "refusal"},
		{name: "broken stream keeps cache-only input", startUsage: `{"input_tokens":0,"output_tokens":1,"cache_read_input_tokens":50000}`, streamText: true, wantOutput: 2, wantLocalCount: true},
		{name: "zeros after streamed text are estimated", startUsage: `{"input_tokens":0,"output_tokens":0}`, deltaUsage: `{"output_tokens":0}`, stopReason: "end_turn", streamText: true, wantInput: 1200, wantOutput: 2, wantLocalCount: true},
		{name: "zero delta input keeps start input", startUsage: `{"input_tokens":1000,"output_tokens":1}`, deltaUsage: `{"input_tokens":0,"output_tokens":50}`, stopReason: "end_turn", streamText: true, wantInput: 1000, wantOutput: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-4-8"}}
			info.SetEstimatePromptTokens(1200)
			state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
			start := fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_1","model":"claude-opus-4-8","usage":%s}}`, tc.startUsage)
			require.Nil(t, HandleStreamResponseData(c, info, state, start))
			if tc.streamText {
				require.Nil(t, HandleStreamResponseData(c, info, state, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`))
			}
			if tc.deltaUsage != "" {
				delta := fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":%s}`, tc.stopReason, tc.deltaUsage)
				require.Nil(t, HandleStreamResponseData(c, info, state, delta))
				require.Nil(t, HandleStreamResponseData(c, info, state, `{"type":"message_stop"}`))
			}
			HandleStreamFinalResponse(c, info, state)
			assert.Equal(t, tc.wantInput, state.Usage.PromptTokens)
			assert.Equal(t, tc.wantOutput, state.Usage.CompletionTokens)
			assert.Equal(t, tc.wantLocalCount, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
			canonical, ok := state.Usage.BillingUsage.CanonicalUsage()
			require.True(t, ok)
			assert.Equal(t, tc.wantInput, canonical.PromptTokens, "settlement must bill the same input")
			assert.Equal(t, tc.wantOutput, canonical.CompletionTokens)
		})
	}
}

func TestResponseOpenAI2ClaudeToolUseInputIsObject(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]any
	}{
		{name: "object", args: `{"q":"x"}`, want: map[string]any{"q": "x"}},
		{name: "empty", args: "", want: map[string]any{}},
		{name: "invalid", args: "{", want: map[string]any{}},
		{name: "null", args: "null", want: map[string]any{}},
		{name: "array", args: `["x"]`, want: map[string]any{}},
		{name: "string", args: `"x"`, want: map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallRequest{
				{
					ID:   "call_1",
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      "lookup",
						Arguments: tt.args,
					},
				},
			})
			resp := relayconvert.ResponseOpenAI2Claude(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: msg, FinishReason: "tool_calls"},
				},
			}, nil)

			require.Len(t, resp.Content, 1)
			assert.Equal(t, "tool_use", resp.Content[0].Type)
			encoded, err := common.Marshal(resp.Content[0].Input)
			require.NoError(t, err)
			want, err := common.Marshal(tt.want)
			require.NoError(t, err)
			assert.Equal(t, string(want), string(encoded))
		})
	}
}

func TestFormatClaudeResponseInfo_MessageStart(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_start",
		Message: &dto.ClaudeMediaMessage{
			Id:    "msg_123",
			Model: "claude-3-5-sonnet",
			Usage: &dto.ClaudeUsage{
				InputTokens:              100,
				OutputTokens:             1,
				CacheCreationInputTokens: 50,
				CacheReadInputTokens:     30,
			},
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.ResponseId != "msg_123" {
		t.Errorf("ResponseId = %s, want msg_123", claudeInfo.ResponseId)
	}
	if claudeInfo.Model != "claude-3-5-sonnet" {
		t.Errorf("Model = %s, want claude-3-5-sonnet", claudeInfo.Model)
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_FullUsage(t *testing.T) {
	// message_start 先积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens: 1,
		},
	}

	// message_delta 带完整 usage（原生 Anthropic 场景）
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			InputTokens:              100,
			OutputTokens:             200,
			CacheCreationInputTokens: 50,
			CacheReadInputTokens:     30,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_MessageDelta_OnlyOutputTokens(t *testing.T) {
	// 模拟 Bedrock: message_start 已积累 usage
	claudeInfo := &ClaudeResponseInfo{
		Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
			CompletionTokens:            1,
			ClaudeCacheCreation5mTokens: 10,
			ClaudeCacheCreation1hTokens: 20,
		},
	}

	// Bedrock 的 message_delta 只有 output_tokens，缺少 input_tokens 和 cache 字段
	claudeResponse := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			OutputTokens: 200,
			// InputTokens, CacheCreationInputTokens, CacheReadInputTokens 都是 0
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	// PromptTokens 应保持 message_start 的值（因为 message_delta 的 InputTokens=0，不更新）
	if claudeInfo.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", claudeInfo.Usage.PromptTokens)
	}
	if claudeInfo.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", claudeInfo.Usage.CompletionTokens)
	}
	if claudeInfo.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", claudeInfo.Usage.TotalTokens)
	}
	// cache 字段应保持 message_start 的值
	if claudeInfo.Usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("CachedTokens = %d, want 30", claudeInfo.Usage.PromptTokensDetails.CachedTokens)
	}
	if claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens != 50 {
		t.Errorf("CachedCreationTokens = %d, want 50", claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation5mTokens != 10 {
		t.Errorf("ClaudeCacheCreation5mTokens = %d, want 10", claudeInfo.Usage.ClaudeCacheCreation5mTokens)
	}
	if claudeInfo.Usage.ClaudeCacheCreation1hTokens != 20 {
		t.Errorf("ClaudeCacheCreation1hTokens = %d, want 20", claudeInfo.Usage.ClaudeCacheCreation1hTokens)
	}
	if !claudeInfo.Done {
		t.Error("expected Done = true")
	}
}

func TestFormatClaudeResponseInfo_NilClaudeInfo(t *testing.T) {
	claudeResponse := &dto.ClaudeResponse{Type: "message_start"}
	ok := FormatClaudeResponseInfo(claudeResponse, nil, nil)
	if ok {
		t.Error("expected false for nil claudeInfo")
	}
}

func TestFormatClaudeResponseInfo_ContentBlockDelta(t *testing.T) {
	text := "hello"
	claudeInfo := &ClaudeResponseInfo{
		Usage:        &dto.Usage{},
		ResponseText: strings.Builder{},
	}
	claudeResponse := &dto.ClaudeResponse{
		Type: "content_block_delta",
		Delta: &dto.ClaudeMediaMessage{
			Text: &text,
		},
	}

	ok := FormatClaudeResponseInfo(claudeResponse, nil, claudeInfo)
	if !ok {
		t.Fatal("expected true")
	}
	if claudeInfo.ResponseText.String() != "hello" {
		t.Errorf("ResponseText = %q, want %q", claudeInfo.ResponseText.String(), "hello")
	}
}

// A stream cut off during an agent tool call must still estimate the call's
// output tokens when the final usage never arrives.
func TestClaudeStreamFallbackCountsInterruptedToolUse(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	events := []*dto.ClaudeResponse{
		{Type: "content_block_start", ContentBlock: &dto.ClaudeMediaMessage{Type: "tool_use", Id: "toolu_1", Name: "read_file"}},
		{Type: "content_block_delta", Delta: &dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: commonPointer(`{"path":`)}},
		{Type: "content_block_delta", Delta: &dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: commonPointer(`"main.go"}`)}},
	}
	for _, event := range events {
		require.True(t, FormatClaudeResponseInfo(event, nil, claudeInfo))
	}
	require.Equal(t, `read_file{"path":"main.go"}`, claudeInfo.ResponseText.String())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-5"},
	}
	info.SetEstimatePromptTokens(100)
	HandleStreamFinalResponse(c, info, claudeInfo)

	// Text estimate plus the per-tool_use framing the upstream bills.
	assert.Equal(t, tokenkit.Estimate("claude-sonnet-5", `read_file{"path":"main.go"}`)+40, claudeInfo.Usage.CompletionTokens)
	assert.Equal(t, 100, claudeInfo.Usage.PromptTokens)
}

func TestBuildOpenAIStyleUsageFromClaudeUsage(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
		UsageSemantic:               "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	if openAIUsage.PromptTokens != 180 {
		t.Fatalf("PromptTokens = %d, want 180", openAIUsage.PromptTokens)
	}
	if openAIUsage.InputTokens != 180 {
		t.Fatalf("InputTokens = %d, want 180", openAIUsage.InputTokens)
	}
	if openAIUsage.TotalTokens != 200 {
		t.Fatalf("TotalTokens = %d, want 200", openAIUsage.TotalTokens)
	}
	if openAIUsage.UsageSemantic != "openai" {
		t.Fatalf("UsageSemantic = %s, want openai", openAIUsage.UsageSemantic)
	}
	if openAIUsage.UsageSource != "anthropic" {
		t.Fatalf("UsageSource = %s, want anthropic", openAIUsage.UsageSource)
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsagePreservesCacheCreationRemainder(t *testing.T) {
	tests := []struct {
		name                    string
		cachedCreationTokens    int
		cacheCreationTokens5m   int
		cacheCreationTokens1h   int
		expectedTotalInputToken int
	}{
		{
			name:                    "prefers aggregate when it includes remainder",
			cachedCreationTokens:    50,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 180,
		},
		{
			name:                    "falls back to split tokens when aggregate missing",
			cachedCreationTokens:    0,
			cacheCreationTokens5m:   10,
			cacheCreationTokens1h:   20,
			expectedTotalInputToken: 160,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &dto.Usage{
				PromptTokens:     100,
				CompletionTokens: 20,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:         30,
					CachedCreationTokens: tt.cachedCreationTokens,
				},
				ClaudeCacheCreation5mTokens: tt.cacheCreationTokens5m,
				ClaudeCacheCreation1hTokens: tt.cacheCreationTokens1h,
				UsageSemantic:               "anthropic",
			}

			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

			if openAIUsage.PromptTokens != tt.expectedTotalInputToken {
				t.Fatalf("PromptTokens = %d, want %d", openAIUsage.PromptTokens, tt.expectedTotalInputToken)
			}
			if openAIUsage.InputTokens != tt.expectedTotalInputToken {
				t.Fatalf("InputTokens = %d, want %d", openAIUsage.InputTokens, tt.expectedTotalInputToken)
			}
		})
	}
}

func TestBuildOpenAIStyleUsageFromClaudeUsageDefaultsAggregateCacheCreationTo5m(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         30,
			CachedCreationTokens: 50,
		},
		UsageSemantic: "anthropic",
	}

	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(usage)

	require.Equal(t, 50, openAIUsage.ClaudeCacheCreation5mTokens)
	require.Equal(t, 0, openAIUsage.ClaudeCacheCreation1hTokens)
}

func applyOpenAIChatReasoningThroughHandlerOrder(t *testing.T, original dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{
		OriginModelName: original.Model,
		Request:         &original,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: original.Model,
		},
	}
	outbound, err := common.DeepCopy(&original)
	require.NoError(t, err)
	require.NoError(t, helper.ModelMappedHelper(c, info, outbound))
	err = helper.ApplyReasoningModelSuffix(nil, info, outbound)
	require.NoError(t, err)
	return outbound, info
}

func TestOpenAIChatRequestToClaudeMessages_ClaudeOpus48HighUsesAdaptiveThinking(t *testing.T) {
	original := dto.GeneralOpenAIRequest{
		Model:       "claude-opus-4-8-high",
		Temperature: commonPointer(0.7),
		TopP:        commonPointer(0.9),
		TopK:        commonPointer(40),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hello",
			},
		},
	}

	outbound, info := applyOpenAIChatReasoningThroughHandlerOrder(t, original)
	claudeRequest, err := relayconvert.OpenAIChatRequestToClaudeMessages(nil, info, *outbound)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8", claudeRequest.Model)
	require.NotNil(t, claudeRequest.Thinking)
	require.Equal(t, "adaptive", claudeRequest.Thinking.Type)
	require.Equal(t, "summarized", claudeRequest.Thinking.Display)
	require.JSONEq(t, `{"effort":"high"}`, string(claudeRequest.OutputConfig))
	require.Nil(t, claudeRequest.Temperature)
	require.Nil(t, claudeRequest.TopP)
	require.Nil(t, claudeRequest.TopK)
}

func TestOpenAIChatRequestToClaudeMessages_ClaudeOpus48ThinkingUsesAdaptiveHighEffort(t *testing.T) {
	original := dto.GeneralOpenAIRequest{
		Model:       "claude-opus-4-8-thinking",
		Temperature: commonPointer(0.7),
		TopP:        commonPointer(0.9),
		TopK:        commonPointer(40),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hello",
			},
		},
	}

	outbound, info := applyOpenAIChatReasoningThroughHandlerOrder(t, original)
	claudeRequest, err := relayconvert.OpenAIChatRequestToClaudeMessages(nil, info, *outbound)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8", claudeRequest.Model)
	require.NotNil(t, claudeRequest.Thinking)
	require.Equal(t, "adaptive", claudeRequest.Thinking.Type)
	require.Equal(t, "summarized", claudeRequest.Thinking.Display)
	require.JSONEq(t, `{"effort":"high"}`, string(claudeRequest.OutputConfig))
	require.Nil(t, claudeRequest.Temperature)
	require.Nil(t, claudeRequest.TopP)
	require.Nil(t, claudeRequest.TopK)
}

func TestClaudeResponsePassesInputTransformationsThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-opus-5-5"},
	}
	transformations := `"input_transformations":[{"type":"thinking_dropped","path":"messages.1.content.0","reason":"prefix_binding_mismatch"}]`

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],` +
		`"stop_reason":"end_turn",` + transformations + `,"usage":{"input_tokens":5,"output_tokens":1}}`
	require.Nil(t, HandleClaudeResponseData(c, info, &ClaudeResponseInfo{Usage: &dto.Usage{}}, nil, []byte(body)))
	assert.Equal(t, body, w.Body.String())

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	event := `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],` +
		transformations + `,"usage":{"input_tokens":5,"output_tokens":1}}}`
	require.Nil(t, HandleStreamResponseData(c, info, &ClaudeResponseInfo{Usage: &dto.Usage{}}, event))
	assert.Contains(t, w.Body.String(), "data: "+event+"\n")
}
