package common

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DiskCacheType 磁盘缓存类型
type DiskCacheType string

const (
	DiskCacheTypeBody     DiskCacheType = "body"     // 请求体缓存
	DiskCacheTypeFile     DiskCacheType = "file"     // 文件数据缓存
	DiskCacheTypeResponse DiskCacheType = "response" // 长响应暂存
)

const diskCacheOrphanMaxAge = 30 * time.Minute

var activeDiskCacheFiles sync.Map

var diskCacheInstanceID = fmt.Sprintf("instance-%d-%s", os.Getpid(), uuid.New().String()[:8])

// 统一的缓存目录名
const diskCacheDir = "new-api-body-cache"

// GetDiskCacheDir 获取统一的磁盘缓存目录
// 注意：每次调用都会重新计算，以响应配置变化
func GetDiskCacheDir() string {
	return filepath.Join(getDiskCacheRootDir(), diskCacheInstanceID)
}

func getDiskCacheRootDir() string {
	cachePath := GetDiskCachePath()
	if cachePath == "" {
		cachePath = os.TempDir()
	}
	return filepath.Join(cachePath, diskCacheDir)
}

// EnsureDiskCacheDir 确保缓存目录存在
func EnsureDiskCacheDir() error {
	dir := GetDiskCacheDir()
	return os.MkdirAll(dir, 0755)
}

// CreateDiskCacheFile 创建磁盘缓存文件
// cacheType: 缓存类型（body/file）
// 返回文件路径和文件句柄
func CreateDiskCacheFile(cacheType DiskCacheType) (string, *os.File, error) {
	if err := EnsureDiskCacheDir(); err != nil {
		return "", nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	dir := GetDiskCacheDir()
	filename := fmt.Sprintf("%s-%s-%d.tmp", cacheType, uuid.New().String()[:8], time.Now().UnixNano())
	filePath := filepath.Join(dir, filename)

	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create cache file: %w", err)
	}
	activeDiskCacheFiles.Store(filePath, struct{}{})

	return filePath, file, nil
}

// WriteDiskCacheFile 写入数据到磁盘缓存文件
// 返回文件路径
func WriteDiskCacheFile(cacheType DiskCacheType, data []byte) (string, error) {
	size := int64(len(data))
	if !TryReserveDiskCache(size) {
		return "", ErrDiskCacheUnavailable
	}
	defer ReleaseDiskCacheReservation(size)
	filePath, file, err := CreateDiskCacheFile(cacheType)
	if err != nil {
		return "", err
	}

	_, err = file.Write(data)
	if err != nil {
		file.Close()
		RemoveDiskCacheFile(filePath)
		return "", fmt.Errorf("failed to write cache file: %w", err)
	}

	if err := file.Close(); err != nil {
		RemoveDiskCacheFile(filePath)
		return "", fmt.Errorf("failed to close cache file: %w", err)
	}

	IncrementDiskFiles(size)
	return filePath, nil
}

// WriteDiskCacheFileString 写入字符串到磁盘缓存文件
func WriteDiskCacheFileString(cacheType DiskCacheType, data string) (string, error) {
	return WriteDiskCacheFile(cacheType, []byte(data))
}

// ReadDiskCacheFile 读取磁盘缓存文件
func ReadDiskCacheFile(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

// ReadDiskCacheFileString 读取磁盘缓存文件为字符串
func ReadDiskCacheFileString(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RemoveDiskCacheFile 删除磁盘缓存文件
func RemoveDiskCacheFile(filePath string) error {
	err := os.Remove(filePath)
	if err == nil || os.IsNotExist(err) {
		activeDiskCacheFiles.Delete(filePath)
	}
	return err
}

// CleanupOldDiskCacheFiles 清理旧的缓存文件
// maxAge: 文件最大存活时间
func CleanupOldDiskCacheFiles(maxAge time.Duration) error {
	rootDir := getDiskCacheRootDir()
	currentDir := GetDiskCacheDir()

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 目录不存在，无需清理
		}
		return err
	}

	now := time.Now()
	for _, entry := range entries {
		entryPath := filepath.Join(rootDir, entry.Name())
		if !entry.IsDir() {
			cleanupDiskCacheFile(entryPath, now, maxAge)
			continue
		}
		if entryPath != currentDir {
			info, infoErr := entry.Info()
			if infoErr != nil || now.Sub(info.ModTime()) <= maxAge {
				continue
			}
		}
		files, readErr := os.ReadDir(entryPath)
		if readErr != nil {
			continue
		}
		for _, file := range files {
			if !file.IsDir() {
				cleanupDiskCacheFile(filepath.Join(entryPath, file.Name()), now, maxAge)
			}
		}
		if entryPath != currentDir {
			_ = os.Remove(entryPath)
		}
	}
	return nil
}

func cleanupDiskCacheFile(filePath string, now time.Time, maxAge time.Duration) {
	info, err := os.Stat(filePath)
	if err != nil || now.Sub(info.ModTime()) <= maxAge {
		return
	}
	if _, active := activeDiskCacheFiles.Load(filePath); active {
		return
	}
	if err = os.Remove(filePath); err == nil {
		DecrementDiskFiles(info.Size())
	}
}

// StartDiskCacheCleanup periodically removes files left behind by interrupted
// or crashed requests. Normal request-owned files are still removed promptly
// by Close; the age window is deliberately longer than supported long tasks.
func StartDiskCacheCleanup() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			_ = os.Chtimes(GetDiskCacheDir(), now, now)
			if err := CleanupOldDiskCacheFiles(diskCacheOrphanMaxAge); err != nil {
				SysError("failed to clean orphaned disk cache files: " + err.Error())
				continue
			}
			SyncDiskCacheStats()
		}
	}()
}

// GetDiskCacheInfo 获取磁盘缓存目录信息
func GetDiskCacheInfo() (fileCount int, totalSize int64, err error) {
	err = filepath.WalkDir(getDiskCacheRootDir(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		fileCount++
		totalSize += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		err = nil
	}
	return
}

// ShouldUseDiskCache 判断是否应该使用磁盘缓存
func ShouldUseDiskCache(dataSize int64) bool {
	if !IsDiskCacheEnabled() {
		return false
	}
	threshold := GetDiskCacheThresholdBytes()
	if dataSize < threshold {
		return false
	}
	return IsDiskCacheAvailable(dataSize)
}
