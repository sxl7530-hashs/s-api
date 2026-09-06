package common

import (
	"sync"
	"sync/atomic"
)

// DiskCacheConfig 磁盘缓存配置（由 performance_setting 包更新）
type DiskCacheConfig struct {
	// Enabled 是否启用磁盘缓存
	Enabled bool
	// ThresholdMB 触发磁盘缓存的请求体大小阈值（MB）
	ThresholdMB int
	// MaxSizeMB 磁盘缓存最大总大小（MB）
	MaxSizeMB int
	// Path 磁盘缓存目录
	Path string
	// CriticalWatermarkPercent 达到该缓存水位后拒绝新的磁盘缓存请求
	CriticalWatermarkPercent int
	// UnknownLengthDiskFirst 未提供 Content-Length 时是否直接流式写盘
	UnknownLengthDiskFirst bool
	// MaxRequestMB 单个请求允许占用的最大缓存空间（0 表示不额外限制）
	MaxRequestMB int
	// AutoSizing 按磁盘总容量比例和最低剩余空间动态计算上限
	AutoSizing bool
	// MaxDiskPercent 自动模式最多使用磁盘总容量的百分比
	MaxDiskPercent int
	// MinFreeSpaceMB 无论如何都必须保留的磁盘空间（MB）
	MinFreeSpaceMB int
}

// 全局磁盘缓存配置
var diskCacheConfig = DiskCacheConfig{
	Enabled:                  false,
	ThresholdMB:              10,
	MaxSizeMB:                0,
	Path:                     "",
	CriticalWatermarkPercent: 90,
	UnknownLengthDiskFirst:   true,
	MaxRequestMB:             4096,
	AutoSizing:               true,
	MaxDiskPercent:           50,
	MinFreeSpaceMB:           30720,
}
var diskCacheConfigMu sync.RWMutex

// GetDiskCacheConfig 获取磁盘缓存配置
func GetDiskCacheConfig() DiskCacheConfig {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	return diskCacheConfig
}

// SetDiskCacheConfig 设置磁盘缓存配置
func SetDiskCacheConfig(config DiskCacheConfig) {
	diskCacheConfigMu.Lock()
	defer diskCacheConfigMu.Unlock()
	diskCacheConfig = config
}

// IsDiskCacheEnabled 是否启用磁盘缓存
func IsDiskCacheEnabled() bool {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	return diskCacheConfig.Enabled
}

// GetDiskCacheThresholdBytes 获取磁盘缓存阈值（字节）
func GetDiskCacheThresholdBytes() int64 {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	return int64(diskCacheConfig.ThresholdMB) << 20
}

// GetDiskCacheMaxSizeBytes 获取磁盘缓存最大大小（字节）
func GetDiskCacheMaxSizeBytes() int64 {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	return int64(diskCacheConfig.MaxSizeMB) << 20
}

// GetDiskCachePath 获取磁盘缓存目录
func GetDiskCachePath() string {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	return diskCacheConfig.Path
}

func GetDiskCacheMaxRequestBytes() int64 {
	diskCacheConfigMu.RLock()
	defer diskCacheConfigMu.RUnlock()
	if diskCacheConfig.MaxRequestMB <= 0 {
		return 0
	}
	return int64(diskCacheConfig.MaxRequestMB) << 20
}

// DiskCacheStats 磁盘缓存统计信息
type DiskCacheStats struct {
	// 当前活跃的磁盘缓存文件数
	ActiveDiskFiles int64 `json:"active_disk_files"`
	// 当前磁盘缓存总大小（字节）
	CurrentDiskUsageBytes int64 `json:"current_disk_usage_bytes"`
	ReservedDiskBytes     int64 `json:"reserved_disk_bytes"`
	// 当前内存缓存数量
	ActiveMemoryBuffers int64 `json:"active_memory_buffers"`
	// 当前内存缓存总大小（字节）
	CurrentMemoryUsageBytes int64 `json:"current_memory_usage_bytes"`
	// 磁盘缓存命中次数
	DiskCacheHits int64 `json:"disk_cache_hits"`
	// 内存缓存命中次数
	MemoryCacheHits int64 `json:"memory_cache_hits"`
	// 磁盘缓存最大限制（字节）
	DiskCacheMaxBytes int64 `json:"disk_cache_max_bytes"`
	// 磁盘缓存阈值（字节）
	DiskCacheThresholdBytes int64 `json:"disk_cache_threshold_bytes"`
}

var diskCacheStats DiskCacheStats
var diskCacheReserveMu sync.Mutex

// GetDiskCacheStats 获取缓存统计信息
func GetDiskCacheStats() DiskCacheStats {
	stats := DiskCacheStats{
		ActiveDiskFiles:         atomic.LoadInt64(&diskCacheStats.ActiveDiskFiles),
		CurrentDiskUsageBytes:   atomic.LoadInt64(&diskCacheStats.CurrentDiskUsageBytes),
		ReservedDiskBytes:       atomic.LoadInt64(&diskCacheStats.ReservedDiskBytes),
		ActiveMemoryBuffers:     atomic.LoadInt64(&diskCacheStats.ActiveMemoryBuffers),
		CurrentMemoryUsageBytes: atomic.LoadInt64(&diskCacheStats.CurrentMemoryUsageBytes),
		DiskCacheHits:           atomic.LoadInt64(&diskCacheStats.DiskCacheHits),
		MemoryCacheHits:         atomic.LoadInt64(&diskCacheStats.MemoryCacheHits),
		DiskCacheMaxBytes:       GetDiskCacheMaxSizeBytes(),
		DiskCacheThresholdBytes: GetDiskCacheThresholdBytes(),
	}
	return stats
}

// IncrementDiskFiles 增加磁盘文件计数
func IncrementDiskFiles(size int64) {
	atomic.AddInt64(&diskCacheStats.ActiveDiskFiles, 1)
	atomic.AddInt64(&diskCacheStats.CurrentDiskUsageBytes, size)
}

// AddDiskCacheUsage updates bytes for an already-counted cache file.
func AddDiskCacheUsage(size int64) {
	if size > 0 {
		atomic.AddInt64(&diskCacheStats.CurrentDiskUsageBytes, size)
	}
}

// DecrementDiskFiles 减少磁盘文件计数
func DecrementDiskFiles(size int64) {
	if atomic.AddInt64(&diskCacheStats.ActiveDiskFiles, -1) < 0 {
		atomic.StoreInt64(&diskCacheStats.ActiveDiskFiles, 0)
	}
	if atomic.AddInt64(&diskCacheStats.CurrentDiskUsageBytes, -size) < 0 {
		atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, 0)
	}
}

// IncrementMemoryBuffers 增加内存缓存计数
func IncrementMemoryBuffers(size int64) {
	atomic.AddInt64(&diskCacheStats.ActiveMemoryBuffers, 1)
	atomic.AddInt64(&diskCacheStats.CurrentMemoryUsageBytes, size)
}

// DecrementMemoryBuffers 减少内存缓存计数
func DecrementMemoryBuffers(size int64) {
	atomic.AddInt64(&diskCacheStats.ActiveMemoryBuffers, -1)
	atomic.AddInt64(&diskCacheStats.CurrentMemoryUsageBytes, -size)
}

// IncrementDiskCacheHits 增加磁盘缓存命中次数
func IncrementDiskCacheHits() {
	atomic.AddInt64(&diskCacheStats.DiskCacheHits, 1)
}

// IncrementMemoryCacheHits 增加内存缓存命中次数
func IncrementMemoryCacheHits() {
	atomic.AddInt64(&diskCacheStats.MemoryCacheHits, 1)
}

// ResetDiskCacheStats 重置命中统计信息（不重置当前使用量）
func ResetDiskCacheStats() {
	atomic.StoreInt64(&diskCacheStats.DiskCacheHits, 0)
	atomic.StoreInt64(&diskCacheStats.MemoryCacheHits, 0)
}

// ResetDiskCacheUsage 重置磁盘缓存使用量统计（用于清理缓存后）
func ResetDiskCacheUsage() {
	atomic.StoreInt64(&diskCacheStats.ActiveDiskFiles, 0)
	atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, 0)
}

// SyncDiskCacheStats 从实际磁盘状态同步统计信息
// 用于修正统计与实际不符的情况
func SyncDiskCacheStats() {
	fileCount, totalSize, err := GetDiskCacheInfo()
	if err != nil {
		return
	}
	atomic.StoreInt64(&diskCacheStats.ActiveDiskFiles, int64(fileCount))
	atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, totalSize)
}

// IsDiskCacheAvailable 检查是否可以创建新的磁盘缓存
func IsDiskCacheAvailable(requestSize int64) bool {
	diskCacheReserveMu.Lock()
	defer diskCacheReserveMu.Unlock()
	return isDiskCacheAvailable(requestSize)
}

func isDiskCacheAvailable(requestSize int64) bool {
	if !IsDiskCacheEnabled() {
		return false
	}
	config := GetDiskCacheConfig()
	maxBytes := GetDiskCacheMaxSizeBytes()
	if config.AutoSizing {
		space := GetDiskSpaceInfo()
		percent := config.MaxDiskPercent
		if percent <= 0 || percent > 100 {
			percent = 50
		}
		percentLimit := int64(space.Total) * int64(percent) / 100
		freeLimit := atomic.LoadInt64(&diskCacheStats.CurrentDiskUsageBytes) + int64(space.Free) - int64(config.MinFreeSpaceMB)<<20
		if freeLimit < 0 {
			freeLimit = 0
		}
		if percentLimit > 0 && (maxBytes <= 0 || percentLimit < maxBytes) {
			maxBytes = percentLimit
		}
		if freeLimit < maxBytes || maxBytes <= 0 {
			maxBytes = freeLimit
		}
	}
	currentUsage := atomic.LoadInt64(&diskCacheStats.CurrentDiskUsageBytes)
	watermark := GetDiskCacheConfig().CriticalWatermarkPercent
	if watermark <= 0 || watermark > 100 {
		watermark = 90
	}
	allowedBytes := maxBytes * int64(watermark) / 100
	if requestSize < 0 || (GetDiskCacheMaxRequestBytes() > 0 && requestSize > GetDiskCacheMaxRequestBytes()) {
		return false
	}
	return currentUsage+atomic.LoadInt64(&diskCacheStats.ReservedDiskBytes)+requestSize <= allowedBytes
}

func TryReserveDiskCache(requestSize int64) bool {
	diskCacheReserveMu.Lock()
	defer diskCacheReserveMu.Unlock()
	if requestSize <= 0 {
		return GetDiskCacheConfig().Enabled
	}
	if !isDiskCacheAvailable(requestSize) {
		return false
	}
	atomic.AddInt64(&diskCacheStats.ReservedDiskBytes, requestSize)
	return true
}

func ReleaseDiskCacheReservation(size int64) {
	if size <= 0 {
		return
	}
	diskCacheReserveMu.Lock()
	defer diskCacheReserveMu.Unlock()
	old := atomic.LoadInt64(&diskCacheStats.ReservedDiskBytes)
	next := old - size
	if next < 0 {
		next = 0
	}
	atomic.StoreInt64(&diskCacheStats.ReservedDiskBytes, next)
}
