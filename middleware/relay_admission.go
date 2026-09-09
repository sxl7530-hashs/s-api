package middleware

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/semaphore"
)

type relayAdmissionPool struct {
	limit   int64
	queue   int64
	active  atomic.Int64
	waiting atomic.Int64
	rejects atomic.Int64
	sem     *semaphore.Weighted
}

type RelayAdmissionStats struct {
	LightActive  int64 `json:"light_active"`
	LightWaiting int64 `json:"light_waiting"`
	LightRejects int64 `json:"light_rejects"`
	HeavyActive  int64 `json:"heavy_active_units"`
	HeavyWaiting int64 `json:"heavy_waiting"`
	HeavyRejects int64 `json:"heavy_rejects"`
}

var lightRelayPool atomic.Pointer[relayAdmissionPool]
var heavyRelayPool atomic.Pointer[relayAdmissionPool]

func RelayAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		pool, weight := relayAdmissionPoolFor(c.Request)
		if pool == nil {
			c.Next()
			return
		}
		if weight > pool.limit {
			pool.rejects.Add(1)
			writeRelayAdmissionRejected(c)
			return
		}
		if pool.sem.TryAcquire(weight) {
			pool.active.Add(weight)
			defer func() {
				pool.active.Add(-weight)
				pool.sem.Release(weight)
			}()
			c.Next()
			return
		}
		if pool.waiting.Add(1) > pool.queue {
			pool.waiting.Add(-1)
			pool.rejects.Add(1)
			writeRelayAdmissionRejected(c)
			return
		}

		wait := time.Duration(constant.RelayAdmissionWaitMilliseconds) * time.Millisecond
		if wait <= 0 {
			wait = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), wait)
		defer cancel()
		if err := pool.sem.Acquire(ctx, weight); err != nil {
			pool.waiting.Add(-1)
			if c.Request.Context().Err() == nil {
				pool.rejects.Add(1)
				writeRelayAdmissionRejected(c)
			}
			return
		}
		pool.waiting.Add(-1)
		pool.active.Add(weight)
		defer func() {
			pool.active.Add(-weight)
			pool.sem.Release(weight)
		}()
		c.Next()
	}
}

func relayAdmissionPoolFor(request *http.Request) (*relayAdmissionPool, int64) {
	heavy, size := isHeavyRelayRequest(request)
	if heavy {
		limit := int64(constant.RelayHeavyConcurrencyUnits)
		pool := loadRelayAdmissionPool(&heavyRelayPool, limit, int64(constant.RelayHeavyQueue))
		unitBytes := int64(max(constant.RelayHeavyThresholdMB, 1)) << 20
		weight := int64(1)
		if size > 0 {
			weight = int64(math.Ceil(float64(size) / float64(unitBytes)))
		}
		return pool, weight
	}
	return loadRelayAdmissionPool(&lightRelayPool, int64(constant.RelayLightConcurrency), int64(constant.RelayLightQueue)), 1
}

func loadRelayAdmissionPool(target *atomic.Pointer[relayAdmissionPool], limit, queue int64) *relayAdmissionPool {
	if limit <= 0 {
		return nil
	}
	if queue < 0 {
		queue = 0
	}
	current := target.Load()
	if current != nil && current.limit == limit && current.queue == queue {
		return current
	}
	created := &relayAdmissionPool{limit: limit, queue: queue, sem: semaphore.NewWeighted(limit)}
	target.Store(created)
	return created
}

func isHeavyRelayRequest(request *http.Request) (bool, int64) {
	if request.ContentLength < 0 {
		return false, request.ContentLength
	}
	threshold := int64(max(constant.RelayHeavyThresholdMB, 1)) << 20
	if request.ContentLength >= threshold {
		return true, request.ContentLength
	}
	contentType := strings.ToLower(request.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/") ||
		strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "application/octet-stream") ||
		strings.HasPrefix(contentType, "application/pdf") ||
		strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/gzip") {
		return true, request.ContentLength
	}
	path := request.URL.Path
	for _, marker := range []string{"/audio/", "/images/edits", "/images/variations", "/video/", "/videos/", "/files", "/submit/blend", "/submit/describe", "/submit/edits", "/upload-discord-images"} {
		if strings.Contains(path, marker) {
			return true, request.ContentLength
		}
	}
	if path == "/v1/edits" {
		return true, request.ContentLength
	}
	return false, request.ContentLength
}

func writeRelayAdmissionRejected(c *gin.Context) {
	retryAfter := constant.RelayAdmissionRetryAfterSeconds
	if retryAfter <= 0 {
		retryAfter = 3
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	if strings.HasPrefix(c.Request.URL.Path, "/v1/messages") {
		err := types.NewErrorWithStatusCode(
			context.DeadlineExceeded,
			types.ErrorCode("relay_admission_overloaded"),
			http.StatusTooManyRequests,
		)
		err.SetMessage("server is busy, please retry shortly")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": err.ToClaudeError()})
		return
	}
	abortWithOpenAiMessage(c, http.StatusTooManyRequests, "server is busy, please retry shortly", types.ErrorCode("relay_admission_overloaded"))
}

func RelayAdmissionLoad() int {
	load := 0.0
	for _, pool := range []*relayAdmissionPool{lightRelayPool.Load(), heavyRelayPool.Load()} {
		if pool == nil || pool.limit <= 0 {
			continue
		}
		activePercent := float64(pool.active.Load()) / float64(pool.limit)
		queuePercent := 0.0
		if pool.queue > 0 {
			queuePercent = float64(pool.waiting.Load()) / float64(pool.queue)
		}
		poolLoad := math.Max(activePercent, queuePercent)
		if poolLoad > load {
			load = poolLoad
		}
	}
	if load > 1 {
		load = 1
	}
	return int(math.Round(load * 100))
}

func GetRelayAdmissionStats() RelayAdmissionStats {
	stats := RelayAdmissionStats{}
	if pool := lightRelayPool.Load(); pool != nil {
		stats.LightActive = pool.active.Load()
		stats.LightWaiting = pool.waiting.Load()
		stats.LightRejects = pool.rejects.Load()
	}
	if pool := heavyRelayPool.Load(); pool != nil {
		stats.HeavyActive = pool.active.Load()
		stats.HeavyWaiting = pool.waiting.Load()
		stats.HeavyRejects = pool.rejects.Load()
	}
	return stats
}
