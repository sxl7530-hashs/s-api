package common

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
)

// DiskSpaceInfo 磁盘空间信息
type DiskSpaceInfo struct {
	// 总空间（字节）
	Total uint64 `json:"total"`
	// 可用空间（字节）
	Free uint64 `json:"free"`
	// 已用空间（字节）
	Used uint64 `json:"used"`
	// 使用百分比
	UsedPercent float64 `json:"used_percent"`
}

// SystemStatus 系统状态信息
type SystemStatus struct {
	CPUUsage        float64 `json:"cpu_usage"`
	ProcessCPUUsage float64 `json:"process_cpu_usage"`
	MemoryUsage     float64 `json:"memory_usage"`
	DiskUsage       float64 `json:"disk_usage"`
	CPUCapacity     float64 `json:"cpu_capacity"`
	GOMAXPROCS      int     `json:"gomaxprocs"`
}

type cgroupCPUSample struct {
	usageSeconds float64
	at           time.Time
}

type cgroupCPUMonitor struct {
	previous cgroupCPUSample
	ready    bool
}

var latestSystemStatus atomic.Value

func init() {
	latestSystemStatus.Store(SystemStatus{})
}

// StartSystemMonitor 启动系统监控
func StartSystemMonitor() {
	currentProcess, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		SysError("failed to initialize process cpu monitor: " + err.Error())
	}
	cgroupCPU := &cgroupCPUMonitor{}
	go func() {
		for {
			config := GetPerformanceMonitorConfig()
			updateSystemStatus(currentProcess, cgroupCPU)
			if !config.Enabled {
				time.Sleep(30 * time.Second)
				continue
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

func updateSystemStatus(currentProcess *process.Process, cgroupCPU *cgroupCPUMonitor) {
	var status SystemStatus
	cpuCapacity := float64(runtime.GOMAXPROCS(0))
	if cgroupCapacity, ok := getCgroupCPUCapacity(); ok && cgroupCapacity < cpuCapacity {
		cpuCapacity = cgroupCapacity
	}
	status.CPUCapacity = cpuCapacity
	status.GOMAXPROCS = runtime.GOMAXPROCS(0)

	// Containers report CPU from their own cgroup rather than from the host.
	// The first cgroup sample has no interval, so retain the host reading only
	// as a startup/non-container fallback.
	if cgroupCPU != nil {
		if cgroupPercent, ok := cgroupCPU.sample(cpuCapacity, time.Now()); ok {
			status.CPUUsage = cgroupPercent
		} else if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
			status.CPUUsage = percents[0]
		}
	} else if percents, err := cpu.Percent(0, false); err == nil && len(percents) > 0 {
		status.CPUUsage = percents[0]
	}
	if currentProcess != nil {
		processPercent, processErr := currentProcess.Percent(0)
		if processErr == nil {
			status.ProcessCPUUsage = normalizeProcessCPUUsage(processPercent, cpuCapacity)
		}
	}

	// Memory
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		status.MemoryUsage = memInfo.UsedPercent
		if cgroupPercent, ok := getCgroupMemoryUsage(memInfo.Total); ok {
			status.MemoryUsage = cgroupPercent
		}
	}

	// Disk
	diskInfo := GetDiskSpaceInfo()
	if diskInfo.Total > 0 {
		status.DiskUsage = diskInfo.UsedPercent
	}

	latestSystemStatus.Store(status)
}

func (m *cgroupCPUMonitor) sample(capacity float64, now time.Time) (float64, bool) {
	usage, ok := getCgroupCPUUsageSeconds()
	if !ok {
		m.ready = false
		return 0, false
	}
	current := cgroupCPUSample{usageSeconds: usage, at: now}
	if !m.ready {
		m.previous = current
		m.ready = true
		return 0, false
	}
	previous := m.previous
	m.previous = current
	return calculateCgroupCPUPercent(previous, current, capacity)
}

func calculateCgroupCPUPercent(previous, current cgroupCPUSample, capacity float64) (float64, bool) {
	elapsed := current.at.Sub(previous.at).Seconds()
	used := current.usageSeconds - previous.usageSeconds
	if capacity <= 0 || elapsed <= 0 || used < 0 || math.IsNaN(used) {
		return 0, false
	}
	percent := used / elapsed / capacity * 100
	if percent > 100 {
		percent = 100
	}
	return percent, true
}

func getCgroupCPUUsageSeconds() (float64, bool) {
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != "usage_usec" {
				continue
			}
			usage, err := strconv.ParseFloat(fields[1], 64)
			if err == nil && usage >= 0 {
				return usage / 1_000_000, true
			}
			return 0, false
		}
	}
	if data, err := os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage"); err == nil {
		usage, parseErr := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if parseErr == nil && usage >= 0 {
			return usage / 1_000_000_000, true
		}
	}
	return 0, false
}

func normalizeProcessCPUUsage(usage float64, cpuCapacity float64) float64 {
	if cpuCapacity <= 0 || math.IsNaN(usage) || usage <= 0 {
		return 0
	}
	usage /= cpuCapacity
	if usage > 100 {
		return 100
	}
	return usage
}

func getCgroupCPUCapacity() (float64, bool) {
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		return parseCgroupCPUCapacity(string(data))
	}
	quota, quotaErr := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, periodErr := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if quotaErr != nil || periodErr != nil {
		return 0, false
	}
	return parseCgroupCPUCapacity(string(quota) + " " + string(period))
}

func parseCgroupCPUCapacity(value string) (float64, bool) {
	fields := strings.Fields(value)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, quotaErr := strconv.ParseFloat(fields[0], 64)
	period, periodErr := strconv.ParseFloat(fields[1], 64)
	if quotaErr != nil || periodErr != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

func getCgroupMemoryUsage(hostTotal uint64) (float64, bool) {
	candidates := [][2]string{
		{"/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.max"},
		{"/sys/fs/cgroup/memory/memory.usage_in_bytes", "/sys/fs/cgroup/memory/memory.limit_in_bytes"},
	}
	for _, candidate := range candidates {
		currentData, currentErr := os.ReadFile(candidate[0])
		limitData, limitErr := os.ReadFile(candidate[1])
		if currentErr != nil || limitErr != nil {
			continue
		}
		percent, ok := calculateCgroupMemoryUsage(string(currentData), string(limitData), hostTotal)
		if ok {
			return percent, true
		}
	}
	return 0, false
}

func calculateCgroupMemoryUsage(currentValue, limitValue string, hostTotal uint64) (float64, bool) {
	limitValue = strings.TrimSpace(limitValue)
	if limitValue == "max" {
		return 0, false
	}
	current, currentErr := strconv.ParseUint(strings.TrimSpace(currentValue), 10, 64)
	limit, limitErr := strconv.ParseUint(limitValue, 10, 64)
	if currentErr != nil || limitErr != nil || limit == 0 || (hostTotal > 0 && limit > hostTotal) {
		return 0, false
	}
	usage := float64(current) / float64(limit) * 100
	if usage > 100 {
		usage = 100
	}
	return usage, true
}

// GetSystemStatus 获取当前系统状态
func GetSystemStatus() SystemStatus {
	return latestSystemStatus.Load().(SystemStatus)
}
