package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRatioSyncResponseRetriesWithFreshAttemptContext(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			<-request.Context().Done()
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, responseCancel, err := fetchRatioSyncResponse(ctx, server.Client(), server.URL, "", 20*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotNil(t, responseCancel)
	defer responseCancel()
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"success":true}`, string(body))
	assert.Equal(t, int32(2), requests.Load())
}

func TestFetchUpstreamRatiosKeepsLongRequestAliveAndReturnsJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"success":true,"data":{"model_ratio":{"gpt-test":1}}}`))
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/ratio_sync/fetch",
		strings.NewReader(`{"upstreams":[{"name":"test","base_url":"`+upstream.URL+`","endpoint":"/api/pricing"}],"timeout":15}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")

	FetchUpstreamRatios(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"success":true,"data":{"differences":{"gpt-test":{"model_ratio":{"current":null,"upstreams":{"test":1},"confidence":{"test":true}}}},"test_results":[{"name":"test","status":"success"}]}}`, recorder.Body.String())
}
