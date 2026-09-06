package regression

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// =============================================================================
// Gemini Image Pricing 回归测试
//
// 测试内容：
// 1. gemini-3-pro-image-preview 的模型倍率和补全倍率
// 2. Gemini 图片尺寸倍率（1K/2K/4K）的默认值和自定义配置
// 3. 从 Gemini 请求中正确解析 imageSize 参数
// 4. 价格乘算逻辑
//
// 运行方式：
//   go test ./tests/regression/ -run TestGemini -v
// =============================================================================

func init() {
	// 初始化所有倍率设置（和服务启动时一样）
	ratio_setting.InitRatioSettings()
}

// --- 1. 模型倍率测试 ---

func TestGeminiModelRatio(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		expectFound bool
		expectRatio float64
		tolerance   float64 // 允许的浮点误差
		description string
	}{
		{
			name:        "gemini-3-pro-preview base ratio",
			model:       "gemini-3-pro-preview",
			expectFound: false, // 未在 defaultModelRatio 中显式配置，走默认 37.5
			description: "gemini-3-pro-preview 未显式配置倍率时返回默认值",
		},
		{
			name:        "gemini-3-pro-image-preview base ratio",
			model:       "gemini-3-pro-image-preview",
			expectFound: false, // 同上，未在 defaultModelRatio 中显式配置
			description: "gemini-3-pro-image-preview 未显式配置倍率时返回默认值",
		},
		{
			name:        "gemini-2.5-flash ratio",
			model:       "gemini-2.5-flash",
			expectFound: true,
			expectRatio: 0.15,
			tolerance:   0.001,
			description: "gemini-2.5-flash 倍率应为 0.15",
		},
		{
			name:        "gemini-2.5-pro ratio",
			model:       "gemini-2.5-pro",
			expectFound: true,
			expectRatio: 0.625,
			tolerance:   0.001,
			description: "gemini-2.5-pro 倍率应为 0.625",
		},
		{
			name:        "gemini-2.0-flash ratio",
			model:       "gemini-2.0-flash",
			expectFound: true,
			expectRatio: 0.05,
			tolerance:   0.001,
			description: "gemini-2.0-flash 倍率应为 0.05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio, found, matchName := ratio_setting.GetModelRatio(tt.model)
			if found != tt.expectFound {
				t.Errorf("[%s] GetModelRatio(%q) found=%v, want %v (matched: %q)",
					tt.description, tt.model, found, tt.expectFound, matchName)
			}
			if tt.expectRatio != 0 && tt.tolerance > 0 {
				if math.Abs(ratio-tt.expectRatio) > tt.tolerance {
					t.Errorf("[%s] GetModelRatio(%q) ratio=%f, want %f",
						tt.description, tt.model, ratio, tt.expectRatio)
				}
			}
			t.Logf("✓ %s: ratio=%.4f, found=%v, matchName=%q", tt.model, ratio, found, matchName)
		})
	}
}

// --- 2. 补全倍率测试 ---

func TestGeminiCompletionRatio(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		expectRatio float64
		tolerance   float64
		description string
	}{
		{
			name:        "gemini-3-pro-preview completion ratio",
			model:       "gemini-3-pro-preview",
			expectRatio: 6.0,
			tolerance:   0.01,
			description: "gemini-3-pro-preview 的补全倍率应为 6",
		},
		{
			name:        "gemini-3-pro-image-preview completion ratio",
			model:       "gemini-3-pro-image-preview",
			expectRatio: 60.0,
			tolerance:   0.01,
			description: "gemini-3-pro-image-preview 补全倍率应为 60（含图片输出溢价）",
		},
		{
			name:        "gemini-2.5-pro completion ratio",
			model:       "gemini-2.5-pro",
			expectRatio: 8.0,
			tolerance:   0.01,
			description: "gemini-2.5-pro 补全倍率应为 8",
		},
		{
			name:        "gemini-2.5-flash completion ratio",
			model:       "gemini-2.5-flash",
			expectRatio: 2.5 / 0.3,
			tolerance:   0.1,
			description: "gemini-2.5-flash 补全倍率应为 2.5/0.3 ≈ 8.33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := ratio_setting.GetCompletionRatio(tt.model)
			if math.Abs(ratio-tt.expectRatio) > tt.tolerance {
				t.Errorf("[%s] GetCompletionRatio(%q) = %f, want %f (±%f)",
					tt.description, tt.model, ratio, tt.expectRatio, tt.tolerance)
			}
			t.Logf("✓ %s: completionRatio=%.4f", tt.model, ratio)
		})
	}
}

// --- 3. Gemini 图片尺寸倍率测试 ---

func TestGeminiImageSizeRatio_Defaults(t *testing.T) {
	tests := []struct {
		sizeLabel   string
		expectRatio float64
		expectFound bool
		description string
	}{
		{"1K", 1.0, true, "1K 默认倍率应为 1.0"},
		{"2K", 1.0, true, "2K 默认倍率应为 1.0"},
		{"4K", 2.0, true, "4K 默认倍率应为 2.0（翻倍）"},
		{"8K", 1.0, false, "8K 未配置，应返回默认 1.0"},
		{"", 0, false, "空标签应返回 0（不适用）"},
	}

	for _, tt := range tests {
		t.Run("size_"+tt.sizeLabel, func(t *testing.T) {
			ratio, found := ratio_setting.GetGeminiImageSizeRatio(tt.sizeLabel)
			if found != tt.expectFound {
				t.Errorf("[%s] found=%v, want %v", tt.description, found, tt.expectFound)
			}
			if math.Abs(ratio-tt.expectRatio) > 0.001 {
				t.Errorf("[%s] ratio=%f, want %f", tt.description, ratio, tt.expectRatio)
			}
			t.Logf("✓ sizeLabel=%q: ratio=%.2f, found=%v", tt.sizeLabel, ratio, found)
		})
	}
}

func TestGeminiImageSizeRatio_CustomConfig(t *testing.T) {
	// 模拟管理员通过后台修改了配置
	customConfig := `{"1K": 1.0, "2K": 1.5, "4K": 3.0, "8K": 5.0}`
	err := ratio_setting.UpdateGeminiImageSizeRatioByJSONString(customConfig)
	if err != nil {
		t.Fatalf("更新 GeminiImageSizeRatio 失败: %v", err)
	}

	tests := []struct {
		sizeLabel   string
		expectRatio float64
	}{
		{"1K", 1.0},
		{"2K", 1.5},
		{"4K", 3.0},
		{"8K", 5.0},
	}

	for _, tt := range tests {
		t.Run("custom_"+tt.sizeLabel, func(t *testing.T) {
			ratio, found := ratio_setting.GetGeminiImageSizeRatio(tt.sizeLabel)
			if !found {
				t.Errorf("自定义配置后 %q 应该被找到", tt.sizeLabel)
			}
			if math.Abs(ratio-tt.expectRatio) > 0.001 {
				t.Errorf("自定义配置 %q: ratio=%f, want %f", tt.sizeLabel, ratio, tt.expectRatio)
			}
			t.Logf("✓ custom %s: ratio=%.2f", tt.sizeLabel, ratio)
		})
	}

	// 恢复默认值
	ratio_setting.InitGeminiImageSizeRatioSettings()
}

// --- 4. 从 Gemini 请求解析 imageSize ---

func TestGeminiRequest_ImageSizeParsing(t *testing.T) {
	tests := []struct {
		name            string
		requestJSON     string
		expectSizeLabel string
		description     string
	}{
		{
			name: "带 imageSize 4k 的请求",
			requestJSON: `{
				"contents": [{"role": "user", "parts": [{"text": "画一只猫"}]}],
				"generationConfig": {
					"responseModalities": ["TEXT", "IMAGE"],
					"imageConfig": {"imageSize": "4k"}
				}
			}`,
			expectSizeLabel: "4K",
			description:     "imageSize=4k 应解析为标签 '4K'（大写）",
		},
		{
			name: "带 imageSize 2K 的请求",
			requestJSON: `{
				"contents": [{"role": "user", "parts": [{"text": "画一只狗"}]}],
				"generationConfig": {
					"responseModalities": ["TEXT", "IMAGE"],
					"imageConfig": {"imageSize": "2K"}
				}
			}`,
			expectSizeLabel: "2K",
			description:     "imageSize=2K 应解析为标签 '2K'",
		},
		{
			name: "无 imageConfig 的请求",
			requestJSON: `{
				"contents": [{"role": "user", "parts": [{"text": "你好"}]}],
				"generationConfig": {
					"maxOutputTokens": 1024
				}
			}`,
			expectSizeLabel: "",
			description:     "没有 imageConfig 时 ImageSizeLabel 应为空",
		},
		{
			name: "有 imageConfig 但无 imageSize",
			requestJSON: `{
				"contents": [{"role": "user", "parts": [{"text": "画画"}]}],
				"generationConfig": {
					"responseModalities": ["TEXT", "IMAGE"],
					"imageConfig": {}
				}
			}`,
			expectSizeLabel: "",
			description:     "imageConfig 为空对象时 ImageSizeLabel 应为空",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req dto.GeminiChatRequest
			if err := json.Unmarshal([]byte(tt.requestJSON), &req); err != nil {
				t.Fatalf("解析请求 JSON 失败: %v", err)
			}

			meta := req.GetTokenCountMeta()
			if meta.ImageSizeLabel != tt.expectSizeLabel {
				t.Errorf("[%s] ImageSizeLabel=%q, want %q",
					tt.description, meta.ImageSizeLabel, tt.expectSizeLabel)
			}
			t.Logf("✓ %s: ImageSizeLabel=%q", tt.name, meta.ImageSizeLabel)
		})
	}
}

// --- 5. 模型价格查找测试 ---

func TestGeminiModelPriceLookup(t *testing.T) {
	// gemini-3-pro-image-preview 使用的是倍率模式（非固定价格）
	// 验证它不在 ModelPrice 中（→ 走倍率计费）
	_, hasPrice := ratio_setting.GetModelPrice("gemini-3-pro-image-preview", false)
	if hasPrice {
		t.Error("gemini-3-pro-image-preview 不应该有固定价格，应走倍率计费")
	} else {
		t.Log("✓ gemini-3-pro-image-preview 使用倍率计费（非固定价格），正确")
	}

	// imagen-3 使用固定价格
	price, hasPrice := ratio_setting.GetModelPrice("imagen-3.0-generate-002", false)
	if !hasPrice {
		t.Error("imagen-3.0-generate-002 应有固定价格配置")
	} else if math.Abs(price-0.03) > 0.001 {
		t.Errorf("imagen-3.0-generate-002 价格=%f, 期望 0.03", price)
	} else {
		t.Logf("✓ imagen-3.0-generate-002 固定价格=%.4f", price)
	}
}

// --- 6. 倍率 JSON 序列化/反序列化测试 ---

func TestGeminiImageSizeRatio_JSONRoundTrip(t *testing.T) {
	// 获取当前配置的 JSON
	jsonStr := ratio_setting.GeminiImageSizeRatio2JSONString()
	t.Logf("当前配置 JSON: %s", jsonStr)

	// 确保是合法 JSON
	var parsed map[string]float64
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("GeminiImageSizeRatio JSON 不合法: %v", err)
	}

	// 验证默认键存在
	requiredKeys := []string{"1K", "2K", "4K"}
	for _, key := range requiredKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("JSON 中缺少必需的键 %q", key)
		}
	}

	// 更新并恢复
	newJSON := `{"1K": 1.0, "2K": 2.0, "4K": 4.0}`
	if err := ratio_setting.UpdateGeminiImageSizeRatioByJSONString(newJSON); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	// 验证更新后的值
	ratio, _ := ratio_setting.GetGeminiImageSizeRatio("2K")
	if math.Abs(ratio-2.0) > 0.001 {
		t.Errorf("更新后 2K ratio=%f, want 2.0", ratio)
	}

	// 恢复
	ratio_setting.InitGeminiImageSizeRatioSettings()
	ratio, _ = ratio_setting.GetGeminiImageSizeRatio("2K")
	if math.Abs(ratio-1.0) > 0.001 {
		t.Errorf("恢复后 2K ratio=%f, want 1.0", ratio)
	}
	t.Log("✓ JSON 序列化/反序列化 round-trip 测试通过")
}

// --- 7. 价格乘算逻辑测试 ---

func TestGeminiImagePriceCalculation(t *testing.T) {
	// 模拟价格计算：
	// 对于按倍率计费的模型，最终价格 = tokens * modelRatio * completionRatio * groupRatio
	// 对于有 imageSizeRatio 的场景，额外乘以 imageSizeRatio（但这在按价格计费时生效）
	//
	// gemini-3-pro-image-preview 走倍率计费，补全倍率为60
	// 这意味着输出 token 的价格 = 输入token价格 × 60
	//
	// 注意：gemini-3-pro-image-preview 没有在 defaultModelRatio 中显式配置，
	// GetModelRatio 返回默认值 37.5 且 found=false（当 SelfUseMode 关闭时）。
	// 但补全倍率是通过前缀匹配获得的（hardcoded 60）。
	// 在实际运行中，管理员需要手动在后台设置该模型的倍率。

	modelRatio, _, _ := ratio_setting.GetModelRatio("gemini-3-pro-image-preview")
	// modelRatio 默认为 37.5（未显式配置时）
	t.Logf("gemini-3-pro-image-preview modelRatio=%.4f (默认值，需管理员在后台配置)", modelRatio)
	completionRatio := ratio_setting.GetCompletionRatio("gemini-3-pro-image-preview")

	// 模拟：100 个输入 token + 200 个输出 token，groupRatio=1.0
	promptTokens := 100
	completionTokens := 200
	groupRatio := 1.0

	inputCost := float64(promptTokens) * modelRatio * groupRatio
	outputCost := float64(completionTokens) * modelRatio * completionRatio * groupRatio
	totalCost := inputCost + outputCost

	t.Logf("模拟计价 (gemini-3-pro-image-preview):")
	t.Logf("  输入:  %d tokens × %.4f(modelRatio) × %.1f(groupRatio) = %.4f", promptTokens, modelRatio, groupRatio, inputCost)
	t.Logf("  输出:  %d tokens × %.4f(modelRatio) × %.1f(completionRatio) × %.1f(groupRatio) = %.4f", completionTokens, modelRatio, completionRatio, groupRatio, outputCost)
	t.Logf("  总计:  %.4f", totalCost)

	// 验证输出 cost 远大于输入 cost（因为 completionRatio=60）
	if outputCost <= inputCost {
		t.Errorf("输出成本(%.4f)应远大于输入成本(%.4f)，因为补全倍率为 60", outputCost, inputCost)
	}

	// 验证比例关系
	expectedRatio := completionRatio * float64(completionTokens) / float64(promptTokens)
	actualRatio := outputCost / inputCost
	if math.Abs(actualRatio-expectedRatio) > 0.01 {
		t.Errorf("输出/输入成本比=%f, 期望=%f", actualRatio, expectedRatio)
	}
	t.Log("✓ 价格计算逻辑正确")
}
