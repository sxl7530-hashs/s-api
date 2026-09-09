package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemPerformanceCPUUsesGatewayProcessLoad(t *testing.T) {
	config := common.PerformanceMonitorConfig{Enabled: true, CPUThreshold: 90}

	assert.Nil(t, checkSystemPerformanceStatus(config, common.SystemStatus{
		CPUUsage:        99,
		ProcessCPUUsage: 25,
	}))

	err := checkSystemPerformanceStatus(config, common.SystemStatus{
		CPUUsage:        10,
		ProcessCPUUsage: 95,
	})
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "system cpu overloaded")
}

func TestRelayAdmissionSeparatesHeavyRequestsAndBoundsQueue(t *testing.T) {
	previousLightConcurrency := constant.RelayLightConcurrency
	previousLightQueue := constant.RelayLightQueue
	previousHeavyConcurrency := constant.RelayHeavyConcurrencyUnits
	previousHeavyQueue := constant.RelayHeavyQueue
	previousThreshold := constant.RelayHeavyThresholdMB
	previousWait := constant.RelayAdmissionWaitMilliseconds
	previousRetry := constant.RelayAdmissionRetryAfterSeconds
	t.Cleanup(func() {
		constant.RelayLightConcurrency = previousLightConcurrency
		constant.RelayLightQueue = previousLightQueue
		constant.RelayHeavyConcurrencyUnits = previousHeavyConcurrency
		constant.RelayHeavyQueue = previousHeavyQueue
		constant.RelayHeavyThresholdMB = previousThreshold
		constant.RelayAdmissionWaitMilliseconds = previousWait
		constant.RelayAdmissionRetryAfterSeconds = previousRetry
		lightRelayPool.Store(nil)
		heavyRelayPool.Store(nil)
	})
	constant.RelayLightConcurrency = 1
	constant.RelayLightQueue = 0
	constant.RelayHeavyConcurrencyUnits = 1
	constant.RelayHeavyQueue = 0
	constant.RelayHeavyThresholdMB = 1
	constant.RelayAdmissionWaitMilliseconds = 10
	constant.RelayAdmissionRetryAfterSeconds = 7
	lightRelayPool.Store(nil)
	heavyRelayPool.Store(nil)

	heavyPool := loadRelayAdmissionPool(&heavyRelayPool, 1, 0)
	require.True(t, heavyPool.sem.TryAcquire(1))
	heavyPool.active.Add(1)
	t.Cleanup(func() {
		heavyPool.active.Add(-1)
		heavyPool.sem.Release(1)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RelayAdmission())
	router.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/v1/audio/transcriptions", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	lightRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	lightResponse := httptest.NewRecorder()
	router.ServeHTTP(lightResponse, lightRequest)
	assert.Equal(t, http.StatusNoContent, lightResponse.Code)

	heavyRequest := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader([]byte("audio")))
	heavyResponse := httptest.NewRecorder()
	router.ServeHTTP(heavyResponse, heavyRequest)
	assert.Equal(t, http.StatusTooManyRequests, heavyResponse.Code)
	assert.Equal(t, "7", heavyResponse.Header().Get("Retry-After"))
	assert.Contains(t, heavyResponse.Body.String(), "relay_admission_overloaded")

	claudeRequest := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(make([]byte, 1<<20)))
	claudeResponse := httptest.NewRecorder()
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.ServeHTTP(claudeResponse, claudeRequest)
	assert.Equal(t, http.StatusTooManyRequests, claudeResponse.Code)
	assert.Contains(t, claudeResponse.Body.String(), `"type":"new_api_error"`)
}

func TestRelayAdmissionWeightsUnknownLengthAtMaximumRequestSize(t *testing.T) {
	previousHeavyConcurrency := constant.RelayHeavyConcurrencyUnits
	previousHeavyQueue := constant.RelayHeavyQueue
	previousThreshold := constant.RelayHeavyThresholdMB
	previousMaxRequest := constant.MaxRequestBodyMB
	t.Cleanup(func() {
		constant.RelayHeavyConcurrencyUnits = previousHeavyConcurrency
		constant.RelayHeavyQueue = previousHeavyQueue
		constant.RelayHeavyThresholdMB = previousThreshold
		constant.MaxRequestBodyMB = previousMaxRequest
		heavyRelayPool.Store(nil)
	})
	constant.RelayHeavyConcurrencyUnits = 64
	constant.RelayHeavyQueue = 1
	constant.RelayHeavyThresholdMB = 8
	constant.MaxRequestBodyMB = 128
	heavyRelayPool.Store(nil)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(nil))
	request.ContentLength = -1
	pool, weight := relayAdmissionPoolFor(request)
	require.NotNil(t, pool)
	assert.EqualValues(t, 16, weight)
}

func TestRelayAdmissionLoadUsesMostSaturatedPool(t *testing.T) {
	light := &relayAdmissionPool{limit: 100, queue: 100}
	heavy := &relayAdmissionPool{limit: 20, queue: 10}
	light.active.Store(25)
	heavy.active.Store(8)
	heavy.waiting.Store(6)
	lightRelayPool.Store(light)
	heavyRelayPool.Store(heavy)
	t.Cleanup(func() {
		lightRelayPool.Store(nil)
		heavyRelayPool.Store(nil)
	})

	assert.Equal(t, 60, RelayAdmissionLoad())
}
