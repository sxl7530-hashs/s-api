package aws

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

const claude5CompatContextKey = "claude5_compat_rules"

var attributionVersionPattern = regexp.MustCompile(`cc_version=([0-9]+\.[0-9]+\.[0-9]+)(\.[A-Za-z0-9_-]+)?`)

type claude5JSON = json.RawMessage

func claude5ModelMatches(patterns []string, modelID string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matched, err := path.Match(pattern, modelID)
		if err == nil && matched {
			return true
		}
	}
	return false
}

// applyClaude5Compat is deliberately kept in the AWS channel package and is
// called only while constructing Bedrock InvokeModel requests. Keeping the
// transformer behind this package boundary prevents the global setting from
// changing Anthropic, OpenAI, Gemini, or other providers.
func applyClaude5Compat(c *gin.Context, info *relaycommon.RelayInfo, modelID, region string, body []byte) ([]byte, error) {
	if c != nil {
		if _, exists := c.Get(claude5CompatContextKey); exists {
			c.Set(claude5CompatContextKey, []string(nil))
		}
		if _, exists := c.Get("claude5_compat_beta_injected"); exists {
			c.Set("claude5_compat_beta_injected", false)
		}
	}
	if info != nil && (info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeAws) {
		return body, nil
	}
	settings := model_setting.GetClaude5CompatSettings()
	if !settings.Enabled || !claude5ModelMatches(settings.TargetModels, modelID) {
		return body, nil
	}
	if len(settings.TargetRegions) > 0 && !slices.ContainsFunc(settings.TargetRegions, func(target string) bool {
		return strings.EqualFold(strings.TrimSpace(target), region)
	}) {
		return body, nil
	}
	if info != nil && info.UserSetting.AwsDirectPassthrough && !settings.ApplyToPassthroughUsers {
		return body, nil
	}

	var request map[string]claude5JSON
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, fmt.Errorf("Claude compatibility expects a JSON object")
	}

	changed := make([]string, 0, 12)
	if settings.AttributionMode == "remove" || settings.AttributionMode == "forge" {
		if !settings.ThinkingBindingRecover {
			return nil, fmt.Errorf("Claude attribution compatibility requires thinking binding recovery")
		}
		applyClaude5Attribution(request, settings, &changed)
	}

	var messages []map[string]claude5JSON
	messagesValid := false
	if settings.ThinkingBindingRecover || settings.StripContainerFields || settings.FixToolResultCacheControl {
		messages, messagesValid = claude5Messages(request["messages"])
	}
	hasThinking := false
	if messagesValid {
		messagesChanged := false
		for _, message := range messages {
			blocks, ok := claude5ContentBlocks(message["content"])
			if !ok {
				continue
			}
			messageChanged := false
			for _, block := range blocks {
				var kind string
				_ = common.Unmarshal(block["type"], &kind)
				if kind == "thinking" || kind == "redacted_thinking" {
					hasThinking = true
				}
				if settings.StripContainerFields && len(block["container"]) > 0 {
					delete(block, "container")
					messageChanged = true
					changed = appendUnique(changed, "container_fields_stripped")
				}
				if settings.FixToolResultCacheControl && kind == "tool_result" && moveToolResultCacheControl(block) {
					messageChanged = true
					changed = appendUnique(changed, "tool_result_cache_control_moved")
				}
			}
			if messageChanged {
				messagesChanged = true
				encoded, err := common.Marshal(blocks)
				if err != nil {
					return nil, err
				}
				message["content"] = encoded
			}
		}
		if messagesChanged {
			encoded, err := common.Marshal(messages)
			if err != nil {
				return nil, err
			}
			request["messages"] = encoded
		}
	}

	if settings.ThinkingBindingRecover && hasThinking {
		var existingBetas []string
		_ = common.Unmarshal(request["anthropic_beta"], &existingBetas)
		if err := addThinkingBindingRecovery(request); err != nil {
			return nil, err
		}
		if c != nil {
			c.Set("claude5_compat_beta_injected", !slices.Contains(existingBetas, "thinking-binding-controls-2026-08-01"))
		}
		changed = appendUnique(changed, "thinking_binding_recovered")
	}
	applyClaude5ThinkingMapping(request, settings, &changed)
	applyClaude5FieldCleaning(request, settings, &changed)

	if len(changed) == 0 {
		return body, nil
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	if c != nil {
		c.Set(claude5CompatContextKey, changed)
	}
	return encoded, nil
}

func applyClaude5Attribution(request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, changed *[]string) {
	raw, ok := request["system"]
	if !ok || common.GetJsonType(raw) != "array" {
		return
	}
	var blocks []map[string]claude5JSON
	if common.Unmarshal(raw, &blocks) != nil {
		return
	}
	before := len(*changed)
	for i, block := range blocks {
		var text string
		_ = common.Unmarshal(block["text"], &text)
		trimmedText := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmedText, "x-anthropic-billing-header:") || !strings.Contains(trimmedText, "cc_version=") {
			continue
		}
		if settings.AttributionMode == "remove" {
			blocks = append(blocks[:i], blocks[i+1:]...)
			*changed = appendUnique(*changed, "attribution_removed")
		} else if settings.AttributionMode == "forge" {
			version := strings.TrimSpace(settings.ForgeVersion)
			if version == "" {
				version = "2.1.280"
			}
			updated := attributionVersionPattern.ReplaceAllString(text, "cc_version="+version+"${2}")
			if updated != text {
				encoded, err := common.Marshal(updated)
				if err == nil {
					blocks[i]["text"] = encoded
					*changed = appendUnique(*changed, "attribution_forged")
				}
			}
		}
		break
	}
	if len(*changed) == before {
		return
	}
	if len(blocks) == 0 {
		delete(request, "system")
		return
	}
	if encoded, err := common.Marshal(blocks); err == nil {
		request["system"] = encoded
	}
}

func claude5Messages(raw claude5JSON) ([]map[string]claude5JSON, bool) {
	if common.GetJsonType(raw) != "array" {
		return nil, false
	}
	var messages []map[string]claude5JSON
	if common.Unmarshal(raw, &messages) != nil {
		return nil, false
	}
	return messages, true
}

func claude5ContentBlocks(raw claude5JSON) ([]map[string]claude5JSON, bool) {
	if common.GetJsonType(raw) != "array" {
		return nil, false
	}
	var blocks []map[string]claude5JSON
	if common.Unmarshal(raw, &blocks) != nil {
		return nil, false
	}
	return blocks, true
}

func addThinkingBindingRecovery(request map[string]claude5JSON) error {
	var thinking map[string]claude5JSON
	if len(request["thinking"]) > 0 {
		if err := common.Unmarshal(request["thinking"], &thinking); err != nil {
			return fmt.Errorf("invalid thinking object: %w", err)
		}
	}
	if thinking == nil {
		thinking = make(map[string]claude5JSON)
	}
	var binding map[string]claude5JSON
	if len(thinking["block_binding"]) > 0 {
		if err := common.Unmarshal(thinking["block_binding"], &binding); err != nil {
			return fmt.Errorf("invalid thinking block binding: %w", err)
		}
	}
	if binding == nil {
		binding = make(map[string]claude5JSON)
	}
	encoded, err := common.Marshal("drop_block")
	if err != nil {
		return err
	}
	binding["prefix_mismatch_behavior"] = encoded
	encoded, err = common.Marshal(binding)
	if err != nil {
		return err
	}
	thinking["block_binding"] = encoded
	encoded, err = common.Marshal(thinking)
	if err != nil {
		return err
	}
	request["thinking"] = encoded

	var betas []string
	if len(request["anthropic_beta"]) > 0 {
		if err := common.Unmarshal(request["anthropic_beta"], &betas); err != nil {
			return fmt.Errorf("invalid anthropic_beta array: %w", err)
		}
	}
	const beta = "thinking-binding-controls-2026-08-01"
	if !slices.Contains(betas, beta) {
		betas = append(betas, beta)
	}
	encoded, err = common.Marshal(betas)
	if err != nil {
		return err
	}
	request["anthropic_beta"] = encoded
	return nil
}

func applyClaude5ThinkingMapping(request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, changed *[]string) {
	if !settings.ThinkingDisabledMap && !settings.ThinkingEnabledMap && !settings.ThinkingDisplayNormalize {
		return
	}
	var thinking map[string]claude5JSON
	if len(request["thinking"]) == 0 || common.Unmarshal(request["thinking"], &thinking) != nil || thinking == nil {
		return
	}
	var kind string
	_ = common.Unmarshal(thinking["type"], &kind)
	before := len(*changed)
	if kind == "disabled" && settings.ThinkingDisabledMap {
		thinking["type"] = mustClaude5Raw("between_tools")
		delete(thinking, "budget_tokens")
		*changed = appendUnique(*changed, "thinking_disabled_mapped")
	} else if kind == "enabled" && settings.ThinkingEnabledMap {
		var budget int
		budgetValid := common.Unmarshal(thinking["budget_tokens"], &budget) == nil && budget > 0
		thinking["type"] = mustClaude5Raw("adaptive")
		delete(thinking, "budget_tokens")
		matched := false
		for _, entry := range settings.ThinkingBudgetEffortTable {
			if budgetValid && budget <= entry.MaxBudget {
				var output map[string]claude5JSON
				_ = common.Unmarshal(request["output_config"], &output)
				if output == nil {
					output = make(map[string]claude5JSON)
				}
				output["effort"] = mustClaude5Raw(entry.Effort)
				request["output_config"] = mustClaude5Raw(output)
				matched = true
				break
			}
		}
		if !matched {
			*changed = appendUnique(*changed, "thinking_budget_dropped")
		}
		*changed = appendUnique(*changed, "thinking_enabled_mapped")
		kind = "adaptive"
	}
	if kind == "adaptive" && settings.ThinkingDisplayNormalize {
		var adaptive map[string]claude5JSON
		_ = common.Unmarshal(thinking["adaptive"], &adaptive)
		if adaptive == nil {
			adaptive = make(map[string]claude5JSON)
		}
		var display string
		_ = common.Unmarshal(adaptive["display"], &display)
		if len(adaptive["display"]) > 0 && display != "summarized" && display != "omitted" {
			fallback := settings.DisplayDefault
			if fallback != "omitted" {
				fallback = "summarized"
			}
			adaptive["display"] = mustClaude5Raw(fallback)
			thinking["adaptive"] = mustClaude5Raw(adaptive)
			*changed = appendUnique(*changed, "thinking_display_normalized")
		}
	}
	if len(*changed) > before {
		request["thinking"] = mustClaude5Raw(thinking)
	}
}

func applyClaude5FieldCleaning(request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, changed *[]string) {
	if settings.DropTemperature && len(request["temperature"]) > 0 {
		delete(request, "temperature")
		*changed = appendUnique(*changed, "temperature_dropped")
	}
	if settings.CleanEmptySystem {
		cleanClaude5System(request, changed)
	}
	if settings.StripContextManagement && len(request["context_management"]) > 0 {
		delete(request, "context_management")
		*changed = appendUnique(*changed, "context_management_stripped")
	}
	if settings.StripOutputConfig && len(request["output_config"]) > 0 {
		if slices.Contains(*changed, "thinking_enabled_mapped") && !slices.Contains(*changed, "thinking_budget_dropped") {
			var output map[string]claude5JSON
			_ = common.Unmarshal(request["output_config"], &output)
			effort := output["effort"]
			if len(effort) == 0 {
				delete(request, "output_config")
			} else {
				request["output_config"] = mustClaude5Raw(map[string]claude5JSON{"effort": effort})
			}
		} else {
			delete(request, "output_config")
		}
		*changed = appendUnique(*changed, "output_config_stripped")
	}
	if settings.MapToolChoiceAuto && len(request["tool_choice"]) > 0 {
		var choice map[string]claude5JSON
		if common.Unmarshal(request["tool_choice"], &choice) == nil {
			var kind string
			_ = common.Unmarshal(choice["type"], &kind)
			if kind == "tool" || kind == "any" {
				request["tool_choice"] = mustClaude5Raw(map[string]string{"type": "auto"})
				*changed = appendUnique(*changed, "tool_choice_mapped_auto")
			}
		}
	}
	if settings.StripUnsupportedWebSearch {
		stripUnsupportedWebSearch(request, changed)
	}
}

func cleanClaude5System(request map[string]claude5JSON, changed *[]string) {
	raw, ok := request["system"]
	if !ok {
		return
	}
	// Native passthrough accepts both string and block-array system prompts.
	// Only remove empty strings; preserve nonempty prompt bytes and unknown types.
	if common.GetJsonType(raw) == "string" {
		var text string
		if common.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) == "" {
			delete(request, "system")
			*changed = appendUnique(*changed, "empty_system_cleaned")
		}
		return
	}
	if common.GetJsonType(raw) != "array" {
		return
	}
	var blocks []map[string]claude5JSON
	if common.Unmarshal(raw, &blocks) != nil {
		return
	}
	filtered := make([]map[string]claude5JSON, 0, len(blocks))
	for _, block := range blocks {
		if _, hasText := block["text"]; !hasText {
			filtered = append(filtered, block)
			continue
		}
		var text string
		if common.Unmarshal(block["text"], &text) != nil {
			filtered = append(filtered, block)
			continue
		}
		if strings.TrimSpace(text) == "" {
			*changed = appendUnique(*changed, "empty_system_cleaned")
			continue
		}
		filtered = append(filtered, block)
	}
	if len(filtered) == 0 {
		*changed = appendUnique(*changed, "empty_system_cleaned")
		delete(request, "system")
		return
	}
	if len(filtered) != len(blocks) {
		request["system"] = mustClaude5Raw(filtered)
	}
}

func moveToolResultCacheControl(block map[string]claude5JSON) bool {
	content, ok := claude5ContentBlocks(block["content"])
	if !ok {
		return false
	}
	var parentControl claude5JSON
	if len(block["cache_control"]) > 0 {
		parentControl = block["cache_control"]
	}
	// Conflicting cache policies cannot be moved without changing semantics.
	for _, child := range content {
		if control, exists := child["cache_control"]; exists {
			if len(parentControl) > 0 && !bytes.Equal(bytes.TrimSpace(parentControl), bytes.TrimSpace(control)) {
				return false
			}
			parentControl = control
		}
	}
	changed := false
	for _, child := range content {
		cacheControl, exists := child["cache_control"]
		if !exists {
			continue
		}
		if len(parentControl) == 0 {
			parentControl = cacheControl
		}
		delete(child, "cache_control")
		changed = true
	}
	if !changed {
		return false
	}
	block["cache_control"] = parentControl
	block["content"] = mustClaude5Raw(content)
	return true
}

func stripUnsupportedWebSearch(request map[string]claude5JSON, changed *[]string) {
	raw, ok := request["tools"]
	if !ok || common.GetJsonType(raw) != "array" {
		return
	}
	var tools []map[string]claude5JSON
	if common.Unmarshal(raw, &tools) != nil {
		return
	}
	filtered := make([]map[string]claude5JSON, 0, len(tools))
	for _, tool := range tools {
		var kind string
		_ = common.Unmarshal(tool["type"], &kind)
		if kind == "web_search_20250305" {
			*changed = appendUnique(*changed, "unsupported_web_search_stripped")
			continue
		}
		filtered = append(filtered, tool)
	}
	if len(filtered) == len(tools) {
		return
	}
	if len(filtered) == 0 {
		delete(request, "tools")
		return
	}
	request["tools"] = mustClaude5Raw(filtered)
}

func appendUnique(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func mustClaude5Raw(value any) claude5JSON {
	encoded, _ := common.Marshal(value)
	return encoded
}
