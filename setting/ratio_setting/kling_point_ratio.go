package ratio_setting

import (
	"fmt"
	"strconv"
	"sync"
)

const defaultKlingPointRatio = 1.0

var (
	klingPointRatio   float64 = defaultKlingPointRatio
	klingPointRatioMu sync.RWMutex
)

// GetKlingPointRatio returns the current Kling point multiplier.
func GetKlingPointRatio() float64 {
	klingPointRatioMu.RLock()
	defer klingPointRatioMu.RUnlock()
	return klingPointRatio
}

// UpdateKlingPointRatio parses a float string and updates the ratio.
func UpdateKlingPointRatio(value string) error {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid KlingPointRatio value: %w", err)
	}
	if v < 0 {
		return fmt.Errorf("KlingPointRatio must be non-negative, got %f", v)
	}
	klingPointRatioMu.Lock()
	klingPointRatio = v
	klingPointRatioMu.Unlock()
	return nil
}

// KlingPointRatio2String returns the current ratio as a string.
func KlingPointRatio2String() string {
	klingPointRatioMu.RLock()
	defer klingPointRatioMu.RUnlock()
	return strconv.FormatFloat(klingPointRatio, 'f', -1, 64)
}

// InitKlingPointRatioSettings resets to the default value.
func InitKlingPointRatioSettings() {
	klingPointRatioMu.Lock()
	klingPointRatio = defaultKlingPointRatio
	klingPointRatioMu.Unlock()
}
