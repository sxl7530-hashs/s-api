package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/types"
)

// defaultGeminiImageSizeRatio holds the default multiplier for each image size label.
// 1K and 2K are priced the same by default; 4K costs 2× more.
var defaultGeminiImageSizeRatio = map[string]float64{
	"1K": 1.0,
	"2K": 1.0,
	"4K": 2.0,
}

var geminiImageSizeRatioMap = types.NewRWMap[string, float64]()

func init() {
	geminiImageSizeRatioMap.AddAll(defaultGeminiImageSizeRatio)
}

// InitGeminiImageSizeRatioSettings resets the map to its default values.
func InitGeminiImageSizeRatioSettings() {
	geminiImageSizeRatioMap.Clear()
	geminiImageSizeRatioMap.AddAll(defaultGeminiImageSizeRatio)
}

// GeminiImageSizeRatio2JSONString serialises the current map to a JSON string.
func GeminiImageSizeRatio2JSONString() string {
	return geminiImageSizeRatioMap.MarshalJSONString()
}

// UpdateGeminiImageSizeRatioByJSONString replaces the map contents from a JSON string.
func UpdateGeminiImageSizeRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonStringWithCallback(geminiImageSizeRatioMap, jsonStr, InvalidateExposedDataCache)
}

// GetGeminiImageSizeRatio returns the price ratio for a given Gemini imageSize label.
// Returns (ratio, true) if found; (1.0, false) if not configured.
func GetGeminiImageSizeRatio(sizeLabel string) (float64, bool) {
	if sizeLabel == "" {
		return 0, false
	}
	label := strings.ToUpper(sizeLabel)
	ratio, ok := geminiImageSizeRatioMap.Get(label)
	if !ok {
		return 1.0, false
	}
	return ratio, true
}

// GetGeminiImageSizeRatioCopy returns a read-only copy of the current map.
func GetGeminiImageSizeRatioCopy() map[string]float64 {
	return geminiImageSizeRatioMap.ReadAll()
}
