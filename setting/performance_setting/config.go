package performance_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// PerformanceSetting 性能设置配置
type PerformanceSetting struct {
	// DiskCacheEnabled 是否启用磁盘缓存（磁盘换内存）
	DiskCacheEnabled bool `json:"disk_cache_enabled"`
	// DiskCacheThresholdMB 触发磁盘缓存的请求体大小阈值（MB）
	DiskCacheThresholdMB int `json:"disk_cache_threshold_mb"`
	// DiskCacheMaxSizeMB 磁盘缓存最大总大小（MB）
	DiskCacheMaxSizeMB int `json:"disk_cache_max_size_mb"`
	// DiskCachePath 磁盘缓存目录
	DiskCachePath string `json:"disk_cache_path"`
	// DiskCacheCriticalWatermarkPercent 达到该水位后拒绝新的大请求（百分比）
	DiskCacheCriticalWatermarkPercent int `json:"disk_cache_critical_watermark_percent"`
	// DiskCacheUnknownLengthDiskFirst 未提供 Content-Length 时是否直接流式写盘
	DiskCacheUnknownLengthDiskFirst bool `json:"disk_cache_unknown_length_disk_first"`
	// DiskCacheMaxRequestMB 单个请求允许占用的最大缓存空间（MB，0 表示不额外限制）
	DiskCacheMaxRequestMB   int  `json:"disk_cache_max_request_mb"`
	DiskCacheAutoSizing     bool `json:"disk_cache_auto_sizing"`
	DiskCacheMaxDiskPercent int  `json:"disk_cache_max_disk_percent"`
	DiskCacheMinFreeSpaceMB int  `json:"disk_cache_min_free_space_mb"`

	// MonitorEnabled 是否启用性能监控
	MonitorEnabled bool `json:"monitor_enabled"`
	// MonitorCPUThreshold CPU 使用率阈值（%）
	MonitorCPUThreshold int `json:"monitor_cpu_threshold"`
	// MonitorMemoryThreshold 内存使用率阈值（%）
	MonitorMemoryThreshold int `json:"monitor_memory_threshold"`
	// MonitorDiskThreshold 磁盘使用率阈值（%）
	MonitorDiskThreshold int `json:"monitor_disk_threshold"`
}

// 默认配置
var performanceSetting = PerformanceSetting{
	DiskCacheEnabled:                  false,
	DiskCacheThresholdMB:              10, // 超过 10MB 使用磁盘缓存
	DiskCacheMaxSizeMB:                0,  // 0 表示自动模式下不启用固定硬上限
	DiskCachePath:                     "", // 空表示使用系统临时目录
	DiskCacheCriticalWatermarkPercent: 90,
	DiskCacheUnknownLengthDiskFirst:   true,
	DiskCacheMaxRequestMB:             4096,
	// false 保持旧版固定容量行为；管理员可在后台开启自动模式。
	DiskCacheAutoSizing:     false,
	DiskCacheMaxDiskPercent: 50,
	DiskCacheMinFreeSpaceMB: 30720,

	MonitorEnabled:         true,
	MonitorCPUThreshold:    90,
	MonitorMemoryThreshold: 90,
	MonitorDiskThreshold:   95,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("performance_setting", &performanceSetting)
	// 同步初始配置到 common 包
	syncToCommon()
}

// syncToCommon 将配置同步到 common 包
func syncToCommon() {
	// 兼容旧版本已保存的配置：新增字段为零值时回填安全默认值。
	if performanceSetting.DiskCacheThresholdMB <= 0 {
		performanceSetting.DiskCacheThresholdMB = 1
	}
	if performanceSetting.DiskCacheCriticalWatermarkPercent <= 0 || performanceSetting.DiskCacheCriticalWatermarkPercent > 100 {
		performanceSetting.DiskCacheCriticalWatermarkPercent = 90
	}
	if performanceSetting.DiskCacheMaxRequestMB < 0 {
		performanceSetting.DiskCacheMaxRequestMB = 0
	}
	if performanceSetting.DiskCacheMaxDiskPercent <= 0 || performanceSetting.DiskCacheMaxDiskPercent > 100 {
		performanceSetting.DiskCacheMaxDiskPercent = 50
	}
	if performanceSetting.DiskCacheMinFreeSpaceMB < 0 {
		performanceSetting.DiskCacheMinFreeSpaceMB = 0
	}
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:                  performanceSetting.DiskCacheEnabled,
		ThresholdMB:              performanceSetting.DiskCacheThresholdMB,
		MaxSizeMB:                performanceSetting.DiskCacheMaxSizeMB,
		Path:                     performanceSetting.DiskCachePath,
		CriticalWatermarkPercent: performanceSetting.DiskCacheCriticalWatermarkPercent,
		UnknownLengthDiskFirst:   performanceSetting.DiskCacheUnknownLengthDiskFirst,
		MaxRequestMB:             performanceSetting.DiskCacheMaxRequestMB,
		AutoSizing:               performanceSetting.DiskCacheAutoSizing,
		MaxDiskPercent:           performanceSetting.DiskCacheMaxDiskPercent,
		MinFreeSpaceMB:           performanceSetting.DiskCacheMinFreeSpaceMB,
	})

	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{
		Enabled:         performanceSetting.MonitorEnabled,
		CPUThreshold:    performanceSetting.MonitorCPUThreshold,
		MemoryThreshold: performanceSetting.MonitorMemoryThreshold,
		DiskThreshold:   performanceSetting.MonitorDiskThreshold,
	})
}

// GetPerformanceSetting 获取性能设置
func GetPerformanceSetting() *PerformanceSetting {
	return &performanceSetting
}

// UpdateAndSync 更新配置并同步到 common 包
// 当配置从数据库加载后，需要调用此函数同步
func UpdateAndSync() {
	syncToCommon()
}

// GetCacheStats 获取缓存统计信息（代理到 common 包）
func GetCacheStats() common.DiskCacheStats {
	return common.GetDiskCacheStats()
}

// ResetStats 重置统计信息
func ResetStats() {
	common.ResetDiskCacheStats()
}
