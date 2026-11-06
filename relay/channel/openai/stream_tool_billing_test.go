package openai

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOaiStreamForwardsContentBeforeNextUpstreamFrame(t *testing.T) {
	for _, tc := range []struct {
		name        string
		format      types.RelayFormat
		terminal    string
		usage       string
		completions bool
	}{
		{"openai", types.RelayFormatOpenAI, "[DONE]", `"total_tokens":110`, false},
		{"claude", types.RelayFormatClaude, "event: message_stop", `"output_tokens":10`, false},
		{"gemini", types.RelayFormatGemini, `"finishReason":"STOP"`, `"totalTokenCount":110`, false},
		{"completions", types.RelayFormatOpenAI, "[DONE]", `"total_tokens":110`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				firstFrame := `{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"first-token"}}]}`
				if tc.completions {
					firstFrame = `{"id":"cmpl_1","model":"gpt-4o-mini","choices":[{"index":0,"text":"first-token"}]}`
				}
				fmt.Fprint(w, "data: "+firstFrame+"\n\n")
				w.(http.Flusher).Flush()
				<-release
				fmt.Fprint(w, "data: "+`{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`+"\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			usageResult := make(chan *dto.Usage, 1)
			engine := gin.New()
			engine.GET("/", func(c *gin.Context) {
				response, err := http.Get(upstream.URL)
				if err != nil {
					c.Status(502)
					return
				}
				info := &relaycommon.RelayInfo{RelayFormat: tc.format, IsStream: true, DisablePing: true, ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
				info.RelayMode = relayconstant.RelayModeChatCompletions
				if tc.completions {
					info.RelayMode = relayconstant.RelayModeCompletions
				}
				usage, _ := OaiStreamHandler(c, info, response)
				usageResult <- usage
			})
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			defer unblock()
			received := make(chan error, 1)
			streamResult := make(chan string, 1)
			go func() {
				response, err := http.Get(gateway.URL)
				if err != nil {
					received <- err
					return
				}
				defer response.Body.Close()
				scanner := bufio.NewScanner(response.Body)
				var output strings.Builder
				contentReceived := false
				for scanner.Scan() {
					output.WriteString(scanner.Text())
					output.WriteByte('\n')
					if !contentReceived && strings.Contains(scanner.Text(), "first-token") {
						contentReceived = true
						received <- nil
						unblock()
					}
				}
				if !contentReceived {
					received <- fmt.Errorf("stream ended before content: %v", scanner.Err())
				}
				streamResult <- output.String()
			}()
			select {
			case err := <-received:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				unblock()
				t.Fatal("first content is waiting for a later upstream frame")
			}
			select {
			case usage := <-usageResult:
				require.NotNil(t, usage)
				assert.Equal(t, 110, usage.TotalTokens)
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not finish after the upstream resumed")
			}
			select {
			case output := <-streamResult:
				assert.Equal(t, 1, strings.Count(output, "first-token"), "early forwarding must not duplicate content")
				assert.Equal(t, 1, strings.Count(output, tc.terminal))
				assert.Contains(t, output, tc.usage)
			case <-time.After(2 * time.Second):
				t.Fatal("client did not receive the terminal event")
			}
		})
	}
}

func TestObserveStreamChoicesDedupesFunctionCallNames(t *testing.T) {
	info := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	seen := make(map[string]struct{})
	var names []string

	chunks := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"get_time","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]}}]}`,
	}
	for _, chunk := range chunks {
		observeStreamChoices(info, chunk, seen, &names)
	}

	require.Len(t, names, 2)
	assert.Equal(t, []string{"get_weather", "get_time"}, names)
	assert.Empty(t, info.StreamStatus.ResponseOutcome(), "tool call deltas carry no finish reason")

	observeStreamChoices(info, `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, seen, &names)
	assert.Equal(t, "completed", info.StreamStatus.ResponseOutcome())
	assert.False(t, info.PerformanceBusinessRejection)

	filtered := &relaycommon.RelayInfo{StreamStatus: relaycommon.NewStreamStatus()}
	observeStreamChoices(filtered, `{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`, map[string]struct{}{}, &names)
	assert.True(t, filtered.PerformanceBusinessRejection)
}

func TestOaiStreamConversionDoesNotReplayFinalContent(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, tail := range []struct {
			name string
			data string
		}{
			{"content-at-eof", ""},
			{"terminal-content", `{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"last-token"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`},
			{"usage-only", `{"id":"chat_1","model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`},
		} {
			t.Run(string(format)+"/"+tail.name, func(t *testing.T) {
				data := "data: " + `{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"first-token"}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}` + "\n\n"
				if tail.data != "" {
					data += "data: " + tail.data + "\n\ndata: [DONE]\n\n"
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
				info := &relaycommon.RelayInfo{
					RelayFormat: format, RelayMode: relayconstant.RelayModeChatCompletions,
					IsStream: true, DisablePing: true, ShouldIncludeUsage: true,
					ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
				}
				response := &http.Response{Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}
				usage, apiErr := OaiStreamHandler(c, info, response)
				require.Nil(t, apiErr)
				require.NotNil(t, usage)
				assert.Equal(t, 110, usage.TotalTokens)
				output := recorder.Body.String()
				assert.Equal(t, 1, strings.Count(output, "first-token"))
				if tail.name == "terminal-content" {
					assert.Equal(t, 1, strings.Count(output, "last-token"))
				}
			})
		}
	}
}

func TestOaiStreamFailurePreservesPartialUsageWithoutSuccessfulEnd(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		for _, ending := range []string{"completed", "completed-eof", "done-only", "error-after-terminal", "truncated", "malformed", "upstream-error", "initial-error", "empty", "cancelled", "content-write-failure", "final-write-failure"} {
			t.Run(string(format)+"/"+ending, func(t *testing.T) {
				var body strings.Builder
				if ending != "initial-error" && ending != "empty" && ending != "cancelled" {
					body.WriteString("data: " + `{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}` + "\n\n")
				}
				switch ending {
				case "completed", "completed-eof", "error-after-terminal", "final-write-failure":
					body.WriteString("data: " + `{"id":"chat_1","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}` + "\n\n")
					if ending == "completed" || ending == "final-write-failure" {
						body.WriteString("data: [DONE]\n\n")
					}
				case "done-only":
					body.WriteString("data: [DONE]\n\n")
				case "malformed":
					body.WriteString("data: {\n\n")
				case "upstream-error", "initial-error":
					body.WriteString("data: " + `{"error":{"message":"controlled upstream failure","type":"server_error","code":"upstream_failure"}}` + "\n\n")
				}
				if ending == "error-after-terminal" {
					body.WriteString("data: " + `{"error":{"message":"controlled trailing failure","type":"server_error"}}` + "\n\n")
				}
				recorder := httptest.NewRecorder()
				var writer http.ResponseWriter = recorder
				if ending == "content-write-failure" || ending == "final-write-failure" {
					needle := "partial"
					if ending == "final-write-failure" {
						switch format {
						case types.RelayFormatOpenAI:
							needle = "[DONE]"
						case types.RelayFormatClaude:
							needle = "event: message_stop"
						case types.RelayFormatGemini:
							needle = `"finishReason":"STOP"`
						}
					}
					writer = &oaiFailingStreamWriter{ResponseRecorder: recorder, needle: needle}
				}
				c, _ := gin.CreateTestContext(writer)
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				if ending == "cancelled" {
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				info := &relaycommon.RelayInfo{RelayFormat: format, RelayMode: relayconstant.RelayModeChatCompletions, IsStream: true, DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
				usage, apiErr := OaiStreamHandler(c, info, &http.Response{Body: io.NopCloser(strings.NewReader(body.String())), Header: make(http.Header)})
				if ending == "initial-error" || ending == "empty" {
					require.NotNil(t, apiErr)
					assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
					assert.Nil(t, usage)
				} else {
					require.Nil(t, apiErr, "do not replay a stream that has already written content")
					require.NotNil(t, usage)
					if ending != "cancelled" {
						assert.Equal(t, 110, usage.TotalTokens)
					}
				}
				switch ending {
				case "completed", "completed-eof", "done-only":
					assert.Equal(t, "completed", info.StreamStatus.ResponseOutcome())
				case "cancelled":
					assert.Equal(t, "cancelled", info.StreamStatus.ResponseOutcome())
				default:
					assert.Equal(t, "failed", info.StreamStatus.ResponseOutcome())
				}
				if ending != "completed" && ending != "completed-eof" && ending != "done-only" {
					assert.NotContains(t, recorder.Body.String(), "[DONE]")
					assert.NotContains(t, recorder.Body.String(), "event: message_stop")
					assert.NotContains(t, recorder.Body.String(), `"finishReason":"STOP"`)
				}
			})
		}
	}
}

// Reject an actual protocol write without relying on Gin's error bookkeeping.
type oaiFailingStreamWriter struct {
	*httptest.ResponseRecorder
	needle string
}

func (w *oaiFailingStreamWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), w.needle) {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(data)
}
func (w *oaiFailingStreamWriter) WriteString(data string) (int, error) { return w.Write([]byte(data)) }
