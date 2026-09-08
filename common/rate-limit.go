package common

import (
	"sync"
	"time"
)

const maxRateLimiterInitialCapacity = 64
const rateLimiterShardCount = 64

type rateLimiterShard struct {
	store map[string]*[]int64
	mutex sync.Mutex
}

type InMemoryRateLimiter struct {
	shards             [rateLimiterShardCount]rateLimiterShard
	initOnce           sync.Once
	expirationDuration time.Duration
}

func (l *InMemoryRateLimiter) Init(expirationDuration time.Duration) {
	l.initOnce.Do(func() {
		for i := range l.shards {
			l.shards[i].store = make(map[string]*[]int64)
		}
		l.expirationDuration = expirationDuration
		if expirationDuration > 0 {
			go l.clearExpiredItems()
		}
	})
}

func (l *InMemoryRateLimiter) clearExpiredItems() {
	for {
		time.Sleep(l.expirationDuration)
		now := time.Now().Unix()
		for i := range l.shards {
			shard := &l.shards[i]
			shard.mutex.Lock()
			for key, queue := range shard.store {
				size := len(*queue)
				if size == 0 || now-(*queue)[size-1] > int64(l.expirationDuration.Seconds()) {
					delete(shard.store, key)
				}
			}
			shard.mutex.Unlock()
		}
	}
}

func (l *InMemoryRateLimiter) shardForKey(key string) *rateLimiterShard {
	// FNV-1a is inexpensive for short IP/user keys and keeps unrelated clients
	// from contending on one global mutex.
	var hash uint64 = 14695981039346656037
	for i := 0; i < len(key); i++ {
		hash ^= uint64(key[i])
		hash *= 1099511628211
	}
	return &l.shards[hash&(rateLimiterShardCount-1)]
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	shard := l.shardForKey(key)
	shard.mutex.Lock()
	defer shard.mutex.Unlock()
	// [old <-- new]
	queue, ok := shard.store[key]
	now := time.Now().Unix()
	if ok {
		if len(*queue) < maxRequestNum {
			*queue = append(*queue, now)
			return true
		} else {
			if now-(*queue)[0] >= duration {
				*queue = (*queue)[1:]
				*queue = append(*queue, now)
				return true
			} else {
				return false
			}
		}
	} else {
		// maxRequestNum is a logical limit, not an estimate of immediate usage.
		// Preallocating it for every new IP can reserve megabytes per one-shot
		// visitor when operators configure a large global limit.
		initialCapacity := maxRequestNum
		if initialCapacity > maxRateLimiterInitialCapacity {
			initialCapacity = maxRateLimiterInitialCapacity
		}
		if initialCapacity < 0 {
			initialCapacity = 0
		}
		s := make([]int64, 0, initialCapacity)
		shard.store[key] = &s
		*(shard.store[key]) = append(*(shard.store[key]), now)
	}
	return true
}
