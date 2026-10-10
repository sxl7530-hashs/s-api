package model_setting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

//var claudeHeadersSettings = map[string][]string{}
//
//var ClaudeThinkingAdapterEnabled = true
//var ClaudeThinkingAdapterMaxTokens = 8192
//var ClaudeThinkingAdapterBudgetTokensPercentage = 0.8

// ClaudeSettings 定义Claude模型的配置
type ClaudeSettings struct {
	HeadersSettings                       map[string]map[string][]string `json:"model_headers_settings"`
	DefaultMaxTokens                      map[string]int                 `json:"default_max_tokens"`
	ThinkingAdapterEnabled                bool                           `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64                        `json:"thinking_adapter_budget_tokens_percentage"`
	CacheOnlyCompatibilityUsers           map[string]bool                `json:"cache_only_compatibility_users"`
	CacheOnlyCompatibilityTokens          map[string]bool                `json:"cache_only_compatibility_tokens"`
}

// 默认配置
var defaultClaudeSettings = ClaudeSettings{
	HeadersSettings:              map[string]map[string][]string{},
	ThinkingAdapterEnabled:       true,
	CacheOnlyCompatibilityUsers:  map[string]bool{},
	CacheOnlyCompatibilityTokens: map[string]bool{},
	DefaultMaxTokens: map[string]int{
		"default": 8192,
	},
	ThinkingAdapterBudgetTokensPercentage: 0.8,
}

// UseCacheOnlyCompatibility reports whether a legacy Claude client should see
// output_tokens=1 for a cache-only response. Token settings override user
// settings so an administrator can opt a single API key out.
func (c *ClaudeSettings) UseCacheOnlyCompatibility(userID, tokenID int) bool {
	if tokenID > 0 {
		if enabled, ok := c.CacheOnlyCompatibilityTokens[strconv.Itoa(tokenID)]; ok {
			return enabled
		}
	}
	return userID > 0 && c.CacheOnlyCompatibilityUsers[strconv.Itoa(userID)]
}

// 全局实例
var claudeSettings = defaultClaudeSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("claude", &claudeSettings)
}

// GetClaudeSettings 获取Claude配置
func GetClaudeSettings() *ClaudeSettings {
	// check default max tokens must have default key
	if _, ok := claudeSettings.DefaultMaxTokens["default"]; !ok {
		claudeSettings.DefaultMaxTokens["default"] = 8192
	}
	return &claudeSettings
}

func (c *ClaudeSettings) WriteHeaders(originModel string, httpHeader *http.Header) {
	if headers, ok := c.HeadersSettings[originModel]; ok {
		for headerKey, headerValues := range headers {
			mergedValues := normalizeHeaderListValues(
				append(append([]string(nil), httpHeader.Values(headerKey)...), headerValues...),
			)
			if len(mergedValues) == 0 {
				continue
			}
			httpHeader.Set(headerKey, strings.Join(mergedValues, ","))
		}
	}
}

func normalizeHeaderListValues(values []string) []string {
	normalizedValues := make([]string, 0, len(values))
	seenValues := make(map[string]struct{}, len(values))
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			normalizedItem := strings.TrimSpace(item)
			if normalizedItem == "" {
				continue
			}
			if _, exists := seenValues[normalizedItem]; exists {
				continue
			}
			seenValues[normalizedItem] = struct{}{}
			normalizedValues = append(normalizedValues, normalizedItem)
		}
	}
	return normalizedValues
}

func (c *ClaudeSettings) GetDefaultMaxTokens(model string) int {
	if maxTokens, ok := c.DefaultMaxTokens[model]; ok {
		return maxTokens
	}
	return c.DefaultMaxTokens["default"]
}

// ValidateClaudeDefaultMaxTokens validates the JSON persisted by the option
// API. Zero stays allowed — the current Messages API accepts max_tokens: 0 as
// cache pre-warming — but negative values are rejected because they would
// wrap into huge unsigned values during request conversion.
func ValidateClaudeDefaultMaxTokens(value string) error {
	var settings map[string]int
	if err := common.UnmarshalJsonStr(value, &settings); err != nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer: %w", err)
	}
	if settings == nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer")
	}
	for model, maxTokens := range settings {
		if maxTokens < 0 {
			return fmt.Errorf("negative Claude default max_tokens %d for %q", maxTokens, model)
		}
	}
	return nil
}

// ValidateClaudeCacheOnlyCompatibility validates the user/token override maps
// accepted by the root-only option API. JSON object keys must be canonical,
// positive integer identifiers and values must be boolean literals.
func ValidateClaudeCacheOnlyCompatibility(value string) error {
	var raw map[string]json.RawMessage
	if err := common.UnmarshalJsonStr(value, &raw); err != nil || raw == nil {
		return fmt.Errorf("Claude cache-only compatibility must be a JSON map of positive IDs to booleans")
	}
	for id, rawValue := range raw {
		parsedID, err := strconv.Atoi(id)
		if err != nil || parsedID <= 0 || strconv.Itoa(parsedID) != id {
			return fmt.Errorf("Claude cache-only compatibility ID %q must be a positive integer", id)
		}
		var enabled bool
		if err := common.Unmarshal(rawValue, &enabled); err != nil || (string(rawValue) != "true" && string(rawValue) != "false") {
			return fmt.Errorf("Claude cache-only compatibility value for ID %q must be boolean", id)
		}
	}
	return nil
}
