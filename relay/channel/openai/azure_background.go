package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
)

// Azure background mode keeps a Responses request synchronous for the client:
// the gateway submits it upstream with background=true, then polls (or resumes
// the event stream) until a terminal status and hands the regular handlers an
// ordinary *http.Response. A client that disconnects first gets the upstream
// response cancelled and is billed for the usage reported at cancel time.
const (
	azureBgPollInitialInterval = 500 * time.Millisecond
	azureBgPollMaxInterval     = 2 * time.Second
	// Must stay below the nginx proxy_read_timeout (1800s) in front of the API.
	azureBgTotalTimeout     = 25 * time.Minute
	azureBgCleanupTimeout   = 15 * time.Second
	azureBgMaxPollFailures  = 5
	azureBgMaxStreamResumes = 5
	azureBgFinalUsageTries  = 3
)

// These are variables only so tests can shorten the waits.
var (
	azureBgPollInterval    = azureBgPollInitialInterval
	azureBgPollMax         = azureBgPollMaxInterval
	azureBgTimeout         = azureBgTotalTimeout
	azureBgFinalUsageDelay = time.Second
)

func shouldUseAzureBackground(info *relaycommon.RelayInfo) bool {
	return info != nil &&
		info.RelayMode == relayconstant.RelayModeResponses &&
		service.ShouldUseAzureBackground(info.ChannelType, info.ChannelOtherSettings.AzureForceBackground)
}

type azureBgTask struct {
	adaptor   *Adaptor
	c         *gin.Context
	info      *relaycommon.RelayInfo
	submitURL string
	header    http.Header
	respID    string
	startTime time.Time
	polls     int
}

func doAzureBackgroundRequest(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, fmt.Errorf("read request body failed: %w", err)
	}
	if store := ojson.GetBytes(body, "store"); store.Exists() && !store.Bool() {
		logger.LogDebug(c, "azure_bg: overriding store=false with store=true")
	}
	if body, err = ojson.SetBytes(body, "background", true); err != nil {
		return nil, fmt.Errorf("set background failed: %w", err)
	}
	if body, err = ojson.SetBytes(body, "store", true); err != nil {
		return nil, fmt.Errorf("set store failed: %w", err)
	}

	submitURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, fmt.Errorf("get request url failed: %w", err)
	}
	t := &azureBgTask{adaptor: a, c: c, info: info, submitURL: submitURL, startTime: time.Now()}

	resp, err := channel.DoApiRequest(a, c, info, bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	t.header = resp.Header.Clone()

	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		resp.Body = newAzureBgStream(t, resp.Body)
		return resp, nil
	}

	submitted, err := service.ReadResponseBodyLimited(resp, 0)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read background submit response failed: %w", err)
	}
	t.respID = ojson.GetBytes(submitted, "id").String()
	if t.respID == "" {
		// Not a background response object; let the regular handler judge it.
		return t.jsonResponse(http.StatusOK, submitted), nil
	}
	return t.poll(submitted)
}

// poll waits for the background response to reach a terminal status.
func (t *azureBgTask) poll(current []byte) (*http.Response, error) {
	ctx := t.c.Request.Context()
	interval := azureBgPollInterval
	failures := 0
	for {
		switch status := ojson.GetBytes(current, "status").String(); status {
		case "completed", "incomplete":
			t.logDone(status, false)
			return t.jsonResponse(http.StatusOK, azureBgClearBackground(current, "background")), nil
		case "failed", "cancelled":
			t.logDone(status, false)
			return t.jsonResponse(azureBgErrorStatus(current), azureBgErrorBody(current, status)), nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return t.abandon("client_gone", current), nil
		case <-timer.C:
		}
		if time.Since(t.startTime) > azureBgTimeout {
			return t.abandon("timeout", current), nil
		}
		interval = min(interval*2, azureBgPollMax)

		next, err := t.fetch(ctx, "")
		if err != nil {
			if ctx.Err() != nil {
				return t.abandon("client_gone", current), nil
			}
			failures++
			logger.LogWarn(t.c, "azure_bg poll failed resp_id=%s attempt=%d: %s", t.respID, failures, err.Error())
			if failures >= azureBgMaxPollFailures {
				t.cancelDetached()
				return nil, fmt.Errorf("azure background poll failed %d times: %w", failures, err)
			}
			continue
		}
		failures = 0
		current = next
	}
}

// abandon cancels the upstream response and returns its final object as a
// normal 200 result, so the regular handler settles the usage reported at
// cancel time instead of refunding.
func (t *azureBgTask) abandon(reason string, last []byte) *http.Response {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.c.Request.Context()), azureBgCleanupTimeout)
	defer cancel()
	final := t.cancelAndFetchFinal(ctx)
	if final == nil {
		final = last
	}
	// A response cancelled while still queued never reached the model, so
	// only one that started generating owes its estimated input.
	started := ojson.GetBytes(last, "status").String() == "in_progress"
	if started && ojson.GetBytes(final, "usage.total_tokens").Int() == 0 {
		estimated := t.info.GetEstimatePromptTokens()
		final, _ = ojson.SetBytes(final, "usage", map[string]int{
			"input_tokens":  estimated,
			"output_tokens": 0,
			"total_tokens":  estimated,
		})
		logger.LogWarn(t.c, "azure_bg resp_id=%s azure_bg_usage_estimated=true input_tokens=%d", t.respID, estimated)
	}
	t.logDone(ojson.GetBytes(final, "status").String()+"/"+reason, reason == "client_gone")
	return t.jsonResponse(http.StatusOK, azureBgClearBackground(final, "background"))
}

// cancelAndFetchFinal cancels the upstream response and reads it back until
// it reports usage. It returns nil when nothing usable came back.
func (t *azureBgTask) cancelAndFetchFinal(ctx context.Context) []byte {
	if t.respID == "" {
		return nil
	}
	if _, err := t.do(ctx, http.MethodPost, "/cancel", ""); err != nil {
		logger.LogWarn(t.c, "azure_bg cancel failed resp_id=%s: %s", t.respID, err.Error())
	}
	var final []byte
	for i := 0; i < azureBgFinalUsageTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return final
			case <-time.After(azureBgFinalUsageDelay):
			}
		}
		body, err := t.fetch(ctx, "")
		if err != nil {
			continue
		}
		final = body
		if ojson.GetBytes(body, "usage.total_tokens").Int() > 0 {
			return final
		}
	}
	return final
}

func (t *azureBgTask) cancelDetached() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.c.Request.Context()), azureBgCleanupTimeout)
	defer cancel()
	if _, err := t.do(ctx, http.MethodPost, "/cancel", ""); err != nil {
		logger.LogWarn(t.c, "azure_bg cancel failed resp_id=%s: %s", t.respID, err.Error())
	}
}

func (t *azureBgTask) fetch(ctx context.Context, query string) ([]byte, error) {
	t.polls++
	resp, err := t.do(ctx, http.MethodGet, "", query)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := service.ReadResponseBodyLimited(resp, 0)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, preview)
	}
	return body, nil
}

// do sends a request for the background response itself, e.g.
// GET .../responses/{id}?api-version=... or POST .../responses/{id}/cancel.
// The caller owns a successful response's body; a cancel's body is drained.
func (t *azureBgTask) do(ctx context.Context, method string, suffix string, query string) (*http.Response, error) {
	itemURL := azureBgItemURL(t.submitURL, t.respID, suffix, query)
	req, err := http.NewRequestWithContext(ctx, method, itemURL, nil)
	if err != nil {
		return nil, err
	}
	if err = t.adaptor.SetupRequestHeader(t.c, &req.Header, t.info); err != nil {
		return nil, err
	}
	if err = channel.ApplyHeaderOverride(t.info, t.c, req); err != nil {
		return nil, err
	}
	req.Header.Del("Content-Type")
	client, err := service.GetHttpClientWithProxySettings(t.info.ChannelSetting.Proxy, t.info.ChannelSetting)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if suffix == "/cancel" {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("cancel status %d", resp.StatusCode)
		}
	}
	return resp, nil
}

func (t *azureBgTask) jsonResponse(statusCode int, body []byte) *http.Response {
	header := t.header.Clone()
	if header == nil {
		header = http.Header{}
	}
	header.Del("Content-Length")
	header.Del("Content-Encoding")
	header.Set("Content-Type", "application/json")
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		StatusCode:    statusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func (t *azureBgTask) logDone(status string, cancelledByClient bool) {
	logger.LogInfo(t.c, fmt.Sprintf("azure_bg resp_id=%s channel=%d status=%s polls=%d elapsed=%dms cancelled_by_client=%t",
		t.respID, t.info.ChannelId, status, t.polls, time.Since(t.startTime).Milliseconds(), cancelledByClient))
}

// azureBgItemURL inserts /{id}{suffix} before the query of the submit URL and
// appends extra query parameters, keeping api-version unchanged.
func azureBgItemURL(submitURL string, respID string, suffix string, extraQuery string) string {
	path, query, _ := strings.Cut(submitURL, "?")
	itemURL := strings.TrimSuffix(path, "/") + "/" + respID + suffix
	if extraQuery != "" {
		if query != "" {
			query += "&"
		}
		query += extraQuery
	}
	if query != "" {
		itemURL += "?" + query
	}
	return itemURL
}

// azureBgClearBackground reports the response as a plain synchronous one.
func azureBgClearBackground(body []byte, path string) []byte {
	if !ojson.GetBytes(body, path).Bool() {
		return body
	}
	patched, err := ojson.SetBytes(body, path, false)
	if err != nil {
		return body
	}
	return patched
}

// azureBgErrorStatus maps the upstream error code of a failed response to the
// status a synchronous request would have returned.
func azureBgErrorStatus(body []byte) int {
	code := strings.ToLower(ojson.GetBytes(body, "error.code").String())
	switch {
	case strings.Contains(code, "rate_limit"):
		return http.StatusTooManyRequests
	case strings.Contains(code, "content_filter"), strings.Contains(code, "invalid"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// azureBgErrorBody turns a failed or foreign-cancelled response into an
// OpenAI error body that RelayErrorHandler understands, so retries still work.
func azureBgErrorBody(body []byte, status string) []byte {
	errResult := ojson.GetBytes(body, "error")
	if errResult.Exists() && errResult.IsObject() {
		wrapped, err := ojson.SetRawBytes([]byte(`{}`), "error", []byte(errResult.Raw))
		if err == nil {
			return wrapped
		}
	}
	wrapped, _ := common.Marshal(map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("azure background response %s", status),
			"type":    "upstream_error",
			"code":    "azure_background_" + status,
		},
	})
	return wrapped
}

// azureBgStream forwards the upstream event stream line by line, resumes it
// with starting_after when it breaks before a terminal event, and cancels the
// upstream response when it is closed early.
type azureBgStream struct {
	task *azureBgTask

	mu       sync.Mutex
	body     io.ReadCloser
	closed   bool
	terminal bool
	lastSeq  int64
	haveSeq  bool
	resumes  int

	partial   []byte
	out       []byte
	buf       []byte
	closeOnce sync.Once
}

func newAzureBgStream(t *azureBgTask, body io.ReadCloser) *azureBgStream {
	return &azureBgStream{task: t, body: body, buf: make([]byte, 32*1024)}
}

func (s *azureBgStream) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		s.mu.Lock()
		body, closed := s.body, s.closed
		s.mu.Unlock()
		if closed {
			return 0, io.ErrClosedPipe
		}
		n, err := body.Read(s.buf)
		if n > 0 {
			s.accept(s.buf[:n])
			continue
		}
		if err == nil {
			continue
		}
		if resumed := s.resume(err); !resumed {
			return 0, err
		}
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

// accept moves complete lines to out, so a resumed stream never continues a
// half-forwarded line.
func (s *azureBgStream) accept(chunk []byte) {
	s.partial = append(s.partial, chunk...)
	idx := bytes.LastIndexByte(s.partial, '\n')
	if idx < 0 {
		return
	}
	complete := s.partial[:idx+1]
	var out []byte
	for _, line := range bytes.SplitAfter(complete, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		out = append(out, s.observe(line)...)
	}
	s.out = append(s.out, out...)
	s.partial = append([]byte(nil), s.partial[idx+1:]...)
}

func (s *azureBgStream) observe(line []byte) []byte {
	payload, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok {
		return line
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || payload[0] != '{' {
		return line
	}
	s.mu.Lock()
	if seq := ojson.GetBytes(payload, "sequence_number"); seq.Exists() {
		s.lastSeq = seq.Int()
		s.haveSeq = true
	}
	if s.task.respID == "" {
		s.task.respID = ojson.GetBytes(payload, "response.id").String()
	}
	switch ojson.GetBytes(payload, "type").String() {
	case "response.completed", "response.incomplete", "response.failed", "response.cancelled", "response.done", "error":
		s.terminal = true
	}
	s.mu.Unlock()

	if !ojson.GetBytes(payload, "response.background").Bool() {
		return line
	}
	patched := azureBgClearBackground(payload, "response.background")
	return append(append([]byte("data: "), patched...), '\n')
}

// resume reopens the stream after an upstream break. It reports false when
// the stream must end with the original error.
func (s *azureBgStream) resume(cause error) bool {
	ctx := s.task.c.Request.Context()
	s.mu.Lock()
	canResume := !s.closed && !s.terminal && s.haveSeq && s.task.respID != "" &&
		s.resumes < azureBgMaxStreamResumes && ctx.Err() == nil
	lastSeq := s.lastSeq
	if canResume {
		s.resumes++
	}
	attempt := s.resumes
	s.mu.Unlock()
	if !canResume {
		return false
	}

	logger.LogWarn(s.task.c, "azure_bg stream broke resp_id=%s after seq=%d, resuming (%d): %v", s.task.respID, lastSeq, attempt, cause)
	resp, err := s.task.do(ctx, http.MethodGet, "", fmt.Sprintf("stream=true&starting_after=%d", lastSeq))
	if err == nil && resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		err = fmt.Errorf("resume status %d", resp.StatusCode)
	}
	if err != nil {
		logger.LogWarn(s.task.c, "azure_bg stream resume failed resp_id=%s: %s", s.task.respID, err.Error())
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = resp.Body.Close()
		return false
	}
	_ = s.body.Close()
	s.body = resp.Body
	s.partial = nil
	return true
}

// Close ends the stream. Before a terminal event that means the client left,
// the scanner gave up, or resuming failed: the upstream response is cancelled
// and the usage it reports is kept on info for settlement.
func (s *azureBgStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		err = s.body.Close()
		terminal := s.terminal
		respID := s.task.respID
		s.mu.Unlock()

		t := s.task
		if terminal || respID == "" {
			t.logDone("stream_done", false)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.c.Request.Context()), azureBgCleanupTimeout)
		defer cancel()
		final := t.cancelAndFetchFinal(ctx)
		cancelledByClient := t.c.Request.Context().Err() != nil
		if usageResult := ojson.GetBytes(final, "usage"); usageResult.Exists() && ojson.GetBytes(final, "usage.total_tokens").Int() > 0 {
			var upstreamUsage dto.Usage
			if unmarshalErr := common.Unmarshal([]byte(usageResult.Raw), &upstreamUsage); unmarshalErr == nil {
				usage := &dto.Usage{}
				service.ApplyResponsesUsage(usage, &upstreamUsage)
				t.info.AzureBackgroundFinalUsage = usage
			}
		} else {
			logger.LogWarn(t.c, "azure_bg resp_id=%s azure_bg_usage_estimated=true", t.respID)
		}
		t.logDone(ojson.GetBytes(final, "status").String()+"/stream_closed", cancelledByClient)
	})
	return err
}

// takeAzureBackgroundFinalUsage prefers the usage the upstream reported when a
// background stream was cancelled over the estimate built from delivered text.
func takeAzureBackgroundFinalUsage(info *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	if info == nil || info.AzureBackgroundFinalUsage == nil {
		return usage
	}
	final := info.AzureBackgroundFinalUsage
	info.AzureBackgroundFinalUsage = nil
	if final.TotalTokens == 0 {
		return usage
	}
	return final
}
