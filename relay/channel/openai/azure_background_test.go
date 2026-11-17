package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAzureBackground serves the background Responses endpoints for one
// response id. Statuses are returned by successive polls, the last repeating.
type fakeAzureBackground struct {
	t        *testing.T
	statuses []string
	final    string // body returned for terminal polls; %s is the status

	// streamEvents are the events of a streamed response; the submit sends
	// the first breakAfter of them and then ends the body without a terminal.
	streamEvents []string
	breakAfter   int
	// hang keeps the submitted stream open after breakAfter events instead
	// of breaking it, until the gateway drops the connection.
	hang bool

	mu            sync.Mutex
	submitBody    []byte
	polls         int
	cancelled     bool
	resumeQueries []string
}

func (f *fakeAzureBackground) handler(w http.ResponseWriter, r *http.Request) {
	if f.hang && r.Method == http.MethodPost && r.URL.Path == "/openai/v1/responses" {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range f.streamEvents[:f.breakAfter] {
			_, _ = fmt.Fprintf(w, "event: x\ndata: %s\n\n", event)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(f.t, "test-key", r.Header.Get("api-key"))
	assert.Equal(f.t, "preview", r.URL.Query().Get("api-version"))
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/responses":
		f.submitBody, _ = io.ReadAll(r.Body)
		if strings.Contains(string(f.submitBody), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range f.streamEvents[:f.breakAfter] {
				_, _ = fmt.Fprintf(w, "event: x\ndata: %s\n\n", event)
			}
			// Leave a half-written line behind, as a dropped connection would.
			_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","seq`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","status":"queued","background":true}`)
	case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/responses/resp_1/cancel":
		f.cancelled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","status":"cancelled"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/openai/v1/responses/resp_1":
		if r.URL.Query().Get("stream") == "true" {
			f.resumeQueries = append(f.resumeQueries, r.URL.RawQuery)
			after, _ := strconv.Atoi(r.URL.Query().Get("starting_after"))
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range f.streamEvents[after+1:] {
				_, _ = fmt.Fprintf(w, "event: x\ndata: %s\n\n", event)
			}
			return
		}
		status := f.statuses[min(f.polls, len(f.statuses)-1)]
		f.polls++
		if f.cancelled {
			status = "cancelled"
		}
		w.Header().Set("Content-Type", "application/json")
		if status == "queued" || status == "in_progress" {
			_, _ = fmt.Fprintf(w, `{"id":"resp_1","object":"response","status":%q,"background":true}`, status)
			return
		}
		_, _ = fmt.Fprintf(w, f.final, status)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAzureBackground) isCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

func shortenAzureBgWaits(t *testing.T) {
	interval, maxInterval, delay := azureBgPollInterval, azureBgPollMax, azureBgFinalUsageDelay
	azureBgPollInterval, azureBgPollMax, azureBgFinalUsageDelay = time.Millisecond, 5*time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		azureBgPollInterval, azureBgPollMax, azureBgFinalUsageDelay = interval, maxInterval, delay
	})
}

func newAzureBgTestContext(t *testing.T, fake *fakeAzureBackground, isStream bool) (*gin.Context, *relaycommon.RelayInfo, context.CancelFunc) {
	t.Helper()
	shortenAzureBgWaits(t)
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(server.Close)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		IsStream:        isStream,
		OriginModelName: "gpt-6-astra",
		RequestURLPath:  "/v1/responses",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeAzure,
			ChannelBaseUrl:       server.URL,
			ApiKey:               "test-key",
			UpstreamModelName:    "gpt-6-astra",
			ChannelOtherSettings: dto.ChannelOtherSettings{AzureForceBackground: common.GetPointer(true)},
		},
	}
	return c, info, cancel
}

const azureBgCompletedBody = `{"id":"resp_1","object":"response","status":%q,"background":true,"model":"gpt-6-astra",
	"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"40"}]}],
	"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`

func TestAzureBackgroundNonStreamPollsUntilCompleted(t *testing.T) {
	fake := &fakeAzureBackground{t: t, statuses: []string{"queued", "in_progress", "completed"}, final: azureBgCompletedBody}
	c, info, _ := newAzureBgTestContext(t, fake, false)

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi","store":false}`))
	require.NoError(t, err)
	resp := result.(*http.Response)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var response dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(body, &response))
	assert.JSONEq(t, `"completed"`, string(response.Status))
	assert.Contains(t, string(body), `"background":false`)
	require.NotNil(t, response.Usage)
	assert.Equal(t, 15, response.Usage.TotalTokens)

	var submitted map[string]any
	require.NoError(t, common.Unmarshal(fake.submitBody, &submitted))
	assert.Equal(t, true, submitted["background"])
	assert.Equal(t, true, submitted["store"], "store=false must be overridden for background mode")
	assert.Equal(t, 3, fake.polls)
	assert.False(t, fake.isCancelled())
}

func TestAzureBackgroundNonStreamFailedReturnsRetryableError(t *testing.T) {
	fake := &fakeAzureBackground{t: t, statuses: []string{"failed"},
		final: `{"id":"resp_1","status":%q,"error":{"code":"server_error","message":"boom"}}`}
	c, info, _ := newAzureBgTestContext(t, fake, false)

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.NoError(t, err)
	resp := result.(*http.Response)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	apiErr := service.RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "boom")
}

func TestAzureBackgroundNonStreamClientGoneCancelsAndKeepsUsage(t *testing.T) {
	fake := &fakeAzureBackground{t: t, statuses: []string{"in_progress"},
		final: `{"id":"resp_1","object":"response","status":%q,"background":true,"output":[],
			"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`}
	c, info, cancel := newAzureBgTestContext(t, fake, false)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.NoError(t, err)
	resp := result.(*http.Response)
	require.Equal(t, http.StatusOK, resp.StatusCode, "a cancelled response must still settle")
	assert.True(t, fake.isCancelled())

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var response dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(body, &response))
	assert.JSONEq(t, `"cancelled"`, string(response.Status))
	require.NotNil(t, response.Usage)
	assert.Equal(t, 19, response.Usage.TotalTokens)
}

func azureBgStreamEvents() []string {
	return []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","status":"queued","background":true}}`,
		`{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_1","status":"in_progress","background":true}}`,
		`{"type":"response.output_text.delta","sequence_number":2,"delta":"4"}`,
		`{"type":"response.output_text.delta","sequence_number":3,"delta":"0"}`,
		`{"type":"response.completed","sequence_number":4,"response":{"id":"resp_1","status":"completed","background":true,"usage":{"input_tokens":12,"output_tokens":2,"total_tokens":14}}}`,
	}
}

func readAzureBgSequence(t *testing.T, body string) []int {
	t.Helper()
	var seqs []int
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event struct {
			SequenceNumber int `json:"sequence_number"`
		}
		require.NoError(t, common.UnmarshalJsonStr(data, &event), line)
		seqs = append(seqs, event.SequenceNumber)
	}
	return seqs
}

func TestAzureBackgroundStreamResumesAfterBreak(t *testing.T) {
	fake := &fakeAzureBackground{t: t, streamEvents: azureBgStreamEvents(), breakAfter: 3}
	c, info, _ := newAzureBgTestContext(t, fake, true)

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi","stream":true}`))
	require.NoError(t, err)
	resp := result.(*http.Response)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, []int{0, 1, 2, 3, 4}, readAzureBgSequence(t, string(body)))
	assert.NotContains(t, string(body), `"background":true`)
	assert.Equal(t, []string{"api-version=preview&stream=true&starting_after=2"}, fake.resumeQueries)
	assert.False(t, fake.isCancelled(), "a stream that reached its terminal event must not be cancelled")
	assert.Nil(t, info.AzureBackgroundFinalUsage)
}

func TestAzureBackgroundStreamCloseBeforeTerminalCancelsAndRecordsUsage(t *testing.T) {
	fake := &fakeAzureBackground{t: t, streamEvents: azureBgStreamEvents(), breakAfter: 3,
		statuses: []string{"cancelled"},
		final:    `{"id":"resp_1","status":%q,"usage":{"input_tokens":12,"output_tokens":1,"total_tokens":13}}`}
	c, info, cancel := newAzureBgTestContext(t, fake, true)

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi","stream":true}`))
	require.NoError(t, err)
	resp := result.(*http.Response)

	buf := make([]byte, 64)
	_, err = resp.Body.Read(buf)
	require.NoError(t, err)
	cancel() // the client leaves; the scanner closes the body
	require.NoError(t, resp.Body.Close())

	assert.True(t, fake.isCancelled())
	require.NotNil(t, info.AzureBackgroundFinalUsage)
	assert.Equal(t, 12, info.AzureBackgroundFinalUsage.PromptTokens)
	assert.Equal(t, 1, info.AzureBackgroundFinalUsage.CompletionTokens)

	estimated := &dto.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}
	settled := takeAzureBackgroundFinalUsage(info, estimated)
	assert.Equal(t, 13, settled.TotalTokens)
	assert.Nil(t, info.AzureBackgroundFinalUsage)
}

// The scanner closes the body when the client leaves, before the handler
// returns its usage, so settlement sees the usage of the cancelled response.
func TestAzureBackgroundStreamHandlerSettlesCancelledUsageOnClientGone(t *testing.T) {
	fake := &fakeAzureBackground{t: t, streamEvents: azureBgStreamEvents(), breakAfter: 3, hang: true,
		statuses: []string{"cancelled"},
		final:    `{"id":"resp_1","status":%q,"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}`}
	c, info, cancel := newAzureBgTestContext(t, fake, true)

	adaptor := &Adaptor{}
	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi","stream":true}`))
	require.NoError(t, err)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	usage, apiErr := OaiResponsesStreamHandler(c, info, result.(*http.Response))
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.True(t, fake.isCancelled())
	assert.Equal(t, 120, usage.PromptTokens)
	assert.Equal(t, 30, usage.CompletionTokens)
	assert.Equal(t, 150, usage.TotalTokens)
}

// pollOnlyAzure answers the submit with a queued response and each poll with
// the next scripted status code; 200 completes the response.
func pollOnlyAzure(t *testing.T, pollStatuses ...int) (*Adaptor, *gin.Context, *relaycommon.RelayInfo, *atomic.Int64) {
	t.Helper()
	var polls, cancels atomic.Int64
	fake := &fakeAzureBackground{t: t}
	c, info, _ := newAzureBgTestContext(t, fake, false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			cancels.Add(1)
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"cancelled"}`)
		case r.Method == http.MethodPost:
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"queued","background":true}`)
		default:
			n := int(polls.Add(1)) - 1
			status := pollStatuses[min(n, len(pollStatuses)-1)]
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"x","message":"x"}}`)
				return
			}
			_, _ = fmt.Fprintf(w, azureBgCompletedBody, "completed")
		}
	}))
	t.Cleanup(server.Close)
	info.ChannelBaseUrl = server.URL
	return &Adaptor{}, c, info, &cancels
}

func TestAzureBackgroundPollWaitsOutServerErrors(t *testing.T) {
	maxInterval := azureBgPollMax
	azureBgPollMax = time.Microsecond
	t.Cleanup(func() { azureBgPollMax = maxInterval })
	adaptor, c, info, cancels := pollOnlyAzure(t, 503, 500, 502, 503, 503, 503, 200)

	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.NoError(t, err, "server errors on polls must be waited out")
	assert.Equal(t, http.StatusOK, result.(*http.Response).StatusCode)
	assert.Zero(t, cancels.Load())
}

// A throttled poll fails the request with 429, leaving retries to the relay
// settings, and cancels the upstream response.
func TestAzureBackgroundPollThrottledFailsWith429(t *testing.T) {
	adaptor, c, info, cancels := pollOnlyAzure(t, http.StatusTooManyRequests)

	result, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.NoError(t, err)
	resp := result.(*http.Response)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, int64(1), cancels.Load())
	apiErr := service.RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
}

func TestAzureBackgroundPollGivesUpOnPersistentErrors(t *testing.T) {
	budget := azureBgTransientBudget
	azureBgTransientBudget = 20 * time.Millisecond
	t.Cleanup(func() { azureBgTransientBudget = budget })

	adaptor, c, info, cancels := pollOnlyAzure(t, http.StatusNotFound)
	_, err := adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.Error(t, err)
	assert.Equal(t, int64(1), cancels.Load())

	adaptor, c, info, cancels = pollOnlyAzure(t, http.StatusServiceUnavailable)
	_, err = adaptor.DoRequest(c, info, strings.NewReader(`{"model":"gpt-6-astra","input":"hi"}`))
	require.Error(t, err, "server errors that outlast the budget still end the request")
	assert.Equal(t, int64(1), cancels.Load())
}

func TestShouldUseAzureBackground(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	globalDefault := settings.AzureForceBackgroundEnabled
	t.Cleanup(func() { settings.AzureForceBackgroundEnabled = globalDefault })
	newInfo := func(channelType int, relayMode int, channelSetting *bool) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			RelayMode: relayMode,
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType:          channelType,
				ChannelOtherSettings: dto.ChannelOtherSettings{AzureForceBackground: channelSetting},
			},
		}
	}
	on, off := common.GetPointer(true), common.GetPointer(false)

	// The channel setting wins over the global default.
	settings.AzureForceBackgroundEnabled = false
	assert.True(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponses, on)))
	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponses, off)))
	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponses, nil)))
	settings.AzureForceBackgroundEnabled = true
	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponses, off)))
	// A channel that never set it follows the global default.
	assert.True(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponses, nil)))

	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeOpenAI, relayconstant.RelayModeResponses, on)))
	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeResponsesCompact, on)))
	assert.False(t, shouldUseAzureBackground(newInfo(constant.ChannelTypeAzure, relayconstant.RelayModeChatCompletions, on)))
}

func TestAzureBgItemURL(t *testing.T) {
	assert.Equal(t, "https://x.openai.azure.com/openai/v1/responses/resp_1?api-version=preview",
		azureBgItemURL("https://x.openai.azure.com/openai/v1/responses?api-version=preview", "resp_1", "", ""))
	assert.Equal(t, "https://x.cognitiveservices.azure.com/openai/responses/resp_1/cancel?api-version=2025-04-01-preview",
		azureBgItemURL("https://x.cognitiveservices.azure.com/openai/responses?api-version=2025-04-01-preview", "resp_1", "/cancel", ""))
	assert.Equal(t, "https://x/openai/v1/responses/resp_1?api-version=preview&stream=true&starting_after=7",
		azureBgItemURL("https://x/openai/v1/responses?api-version=preview", "resp_1", "", "stream=true&starting_after=7"))
}
