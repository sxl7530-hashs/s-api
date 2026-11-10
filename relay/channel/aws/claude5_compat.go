package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const claude5CompatContextKey = "claude5_compat_rules"
const claude5CompatAuditKey = "claude5_compat_audit"
const (
	claude5CompatBetaContextKey   = "claude5_compat_beta_injected"
	claude5CompatModelContextKey  = "claude5_compat_beta_model"
	claude5CompatRegionContextKey = "claude5_compat_beta_region"
	claude5CompatCapabilityKey    = "claude5_compat_capability_key"
	claude5CompatBetaName         = "thinking-binding-controls-2026-08-01"
	claude5CompatCapabilityVer    = "v1"
)

// Only explicit rejections of this beta may create a shared temporary deny.
// Generic schema failures mentioning the beta elsewhere are not evidence that
// the model lacks beta support. Unrecognized wording is left for admin review.
var claude5BetaRejectionPattern = regexp.MustCompile(`(?i)(?:\b(?:unsupported|unknown)\s+beta(?:\s+(?:flag|feature|header|value))?\s*[:=]?\s*["']?` + regexp.QuoteMeta(claude5CompatBetaName) + `["']?(?:[\s,;.:]|$)|\bbeta(?:\s+(?:flag|feature|header|value))?\s*[:=]?\s*["']?` + regexp.QuoteMeta(claude5CompatBetaName) + `["']?\s+(?:is\s+)?(?:unsupported|not supported)\b)`)

type claude5JSON = json.RawMessage

type claude5RuleState struct {
	Evaluated bool `json:"rule_evaluated"`
	Matched   bool `json:"rule_matched"`
	Changed   bool `json:"body_changed"`
	Skipped   bool `json:"rule_skipped"`
}
type claude5RuleStates map[string]*claude5RuleState

func (states claude5RuleStates) evaluate(name string, matched bool) {
	if state := states[name]; state != nil {
		state.Evaluated = true
		state.Matched = matched
	}
}

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
	result, err := transformClaude5Compat(c, info, modelID, region, body)
	if err != nil {
		// Compatibility processing must never turn a valid Bedrock request into
		// a gateway error. The parsed request is a private working copy.
		if c != nil {
			c.Set(claude5CompatContextKey, []string{"config_error"})
			c.Set(claude5CompatBetaContextKey, false)
			if value, ok := c.Get(claude5CompatAuditKey); ok {
				if audit, ok := value.(map[string]any); ok {
					audit["config_error"] = true
					audit["body_changed"] = false
					delete(audit, "version_change")
				}
			}
		}
		return body, nil
	}
	return result, nil
}

// Diagnostics use controlled identifiers and never include request content.
func claude5CompatErrorCode(err error) string {
	switch err.Error() {
	case "system must be an array":
		return "system_invalid_type"
	case "invalid system array":
		return "system_invalid_array"
	case "thinking must be an object", "invalid thinking object":
		return "thinking_invalid_type"
	case "thinking.type must be a string":
		return "thinking_type_invalid"
	case "thinking.budget_tokens must be a non-negative integer":
		return "budget_not_integer"
	case "thinking.budget_tokens is outside safe integer range":
		return "budget_out_of_range"
	case "thinking.adaptive must be an object":
		return "adaptive_invalid_type"
	case "messages must be an array":
		return "messages_invalid_type"
	case "invalid messages array":
		return "messages_invalid_array"
	case "message content must be a block array":
		return "content_invalid_blocks"
	case "invalid thinking block binding":
		return "binding_invalid_type"
	case "invalid prefix mismatch behavior":
		return "binding_behavior_invalid"
	case "invalid mismatch behavior alias":
		return "binding_alias_invalid"
	case "invalid anthropic_beta array":
		return "beta_invalid_array"
	case "invalid anthropic_beta entry":
		return "beta_invalid_entry"
	case "conflicting thinking display fields":
		return "display_conflict"
	case "output_config must be an object when mapping effort":
		return "output_config_invalid_type"
	case "invalid forge version":
		return "forge_version_invalid"
	case "unable to replace Claude attribution version":
		return "attribution_version_replace_failed"
	case "Claude attribution compatibility requires thinking binding recovery":
		return "binding_required"
	case "invalid Claude compatibility target pattern":
		return "target_pattern_invalid"
	case "Claude compatibility expects a JSON object":
		return "request_invalid_type"
	case "final Claude compatibility body is not a JSON object":
		return "result_invalid_type"
	default:
		return "processing_failed"
	}
}

func transformClaude5Compat(c *gin.Context, info *relaycommon.RelayInfo, modelID, region string, body []byte) (result []byte, transformErr error) {
	stage := "configuration"
	defer func() {
		if c == nil || transformErr == nil {
			return
		}
		value, _ := c.Get(claude5CompatAuditKey)
		audit, _ := value.(map[string]any)
		if audit == nil {
			audit = make(map[string]any)
			c.Set(claude5CompatAuditKey, audit)
		}
		audit["error_stage"] = stage
		audit["error_code"] = claude5CompatErrorCode(transformErr)
	}()
	if c != nil {
		c.Set(claude5CompatAuditKey, nil)
		c.Set("claude5_compat_attribution", "")
		c.Set("claude5_compat_version_change", nil)
		c.Set("claude5_compat_b1_status", "")
		if _, exists := c.Get(claude5CompatContextKey); exists {
			c.Set(claude5CompatContextKey, []string(nil))
		}
		if _, exists := c.Get(claude5CompatBetaContextKey); exists {
			c.Set(claude5CompatBetaContextKey, false)
		}
	}
	if info != nil && (info.ChannelMeta == nil || info.ChannelMeta.ChannelType != constant.ChannelTypeAws) {
		return body, nil
	}
	settings := model_setting.GetClaude5CompatSettings()
	if !settings.Enabled {
		return body, nil
	}
	if info != nil && info.UserSetting.AwsDirectPassthrough {
		return body, nil
	}

	// An omitted output policy preserves legacy request fields. Cleanup remains
	// independent from thinking conversion; only strip_all conflicts with it.
	if settings.OutputConfigPolicy == "" {
		settings.OutputConfigPolicy = "none"
		if settings.StripOutputConfig {
			settings.OutputConfigPolicy = "strip_all"
		}
	}
	if settings.ForgeVersion == "" {
		settings.ForgeVersion = "2.1.280"
	}
	if settings.FingerprintPolicy == "" {
		settings.FingerprintPolicy = "preserve"
	}
	if err := model_setting.ValidateClaude5CompatSettings(settings); err != nil {
		return body, err
	}
	for _, pattern := range settings.TargetModels {
		if _, err := path.Match(strings.TrimSpace(pattern), modelID); err != nil || strings.TrimSpace(pattern) == "" {
			return body, fmt.Errorf("invalid Claude compatibility target pattern")
		}
	}
	if !claude5ModelMatches(settings.TargetModels, modelID) {
		return body, nil
	}
	if len(settings.TargetRegions) > 0 && !slices.ContainsFunc(settings.TargetRegions, func(target string) bool {
		return strings.EqualFold(strings.TrimSpace(target), region)
	}) {
		return body, nil
	}

	audit := map[string]any{"aws_model_id": modelID, "region": region, "attribution_mode": settings.AttributionMode, "fingerprint_policy": settings.FingerprintPolicy, "output_config_policy": settings.OutputConfigPolicy, "thinking_enabled_map": settings.ThinkingEnabledMap, "cohort": "compatibility"}
	samplePercent := 100
	if settings.AuditSamplePercent != nil {
		samplePercent = *settings.AuditSamplePercent
	}
	audit["audit_sample_percent"] = samplePercent
	for _, entry := range settings.FingerprintStatuses {
		if entry.Model == modelID && strings.EqualFold(entry.Region, region) {
			audit["fingerprint_status"] = entry.Status
			break
		}
	}
	if c != nil {
		requestID := c.GetString(common.RequestIdKey)
		if requestID != "" {
			digest := common.GenerateHMAC("claude5-compat:" + requestID)
			audit["request_id_hmac"] = digest
			bucket, _ := strconv.ParseUint(digest[:8], 16, 32)
			if int(bucket%100) < settings.ControlPercent {
				audit["cohort"] = "control"
				audit["body_changed"] = false
				c.Set(claude5CompatAuditKey, audit)
				return body, nil
			}
		}
		c.Set(claude5CompatAuditKey, audit)
	}

	stage = "request_decode"
	var request map[string]claude5JSON
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, fmt.Errorf("Claude compatibility expects a JSON object")
	}

	changed := make([]string, 0, 12)
	bindingCapability := ""
	states := make(claude5RuleStates)
	for _, rule := range []struct {
		name    string
		enabled bool
	}{
		{"attribution_removed", settings.AttributionMode == "remove"},
		{"attribution_forged", settings.AttributionMode == "forge"},
		{"thinking_disabled_mapped", settings.ThinkingDisabledMap},
		{"thinking_enabled_mapped", settings.ThinkingEnabledMap},
		{"thinking_display_normalized", settings.ThinkingDisplayNormalize},
		{"thinking_beta_injected", settings.ThinkingBindingRecover},
		{"thinking_binding_recovered", settings.ThinkingBindingRecover && settings.BlockBindingWrite},
		{"temperature_dropped", settings.DropTemperature},
		{"empty_system_cleaned", settings.CleanEmptySystem},
		{"tool_result_cache_control_moved", settings.FixToolResultCacheControl},
		{"context_management_stripped", settings.StripContextManagement},
		{"container_fields_stripped", settings.StripContainerFields},
		{"tool_choice_mapped_auto", settings.MapToolChoiceAuto},
		{"unsupported_web_search_stripped", settings.StripUnsupportedWebSearch},
		{"output_config_stripped", settings.OutputConfigPolicy == "strip_all"},
		{"output_config_normalized", settings.OutputConfigPolicy == "keep_effort_only"},
	} {
		if rule.enabled {
			states[rule.name] = &claude5RuleState{}
		}
	}

	// Diagnostics contain only controlled identifiers, never request content.
	if c != nil {
		c.Set(claude5CompatAuditKey, audit)
	}
	defer func() {
		if c == nil {
			return
		}
		value, _ := c.Get(claude5CompatAuditKey)
		audit, _ := value.(map[string]any)
		if audit == nil {
			return
		}
		committed := c.GetStringSlice(claude5CompatContextKey)
		for name, state := range states {
			// Earlier rules can create the target of a later rule (enabled
			// thinking becomes adaptive before display normalization).
			state.Matched = state.Matched || (state.Evaluated && slices.Contains(changed, name))
			state.Changed = slices.Contains(committed, name)
			state.Skipped = state.Evaluated && !state.Changed
		}
		for _, name := range []string{"attribution_removed", "attribution_forged"} {
			if state := states[name]; state != nil {
				state.Matched = c.GetString("claude5_compat_attribution") != ""
			}
		}
		// These rules always change the body when a supported target is found.
		for _, name := range []string{"empty_system_cleaned", "container_fields_stripped", "tool_result_cache_control_moved", "tool_choice_mapped_auto", "unsupported_web_search_stripped"} {
			if state := states[name]; state != nil {
				state.Matched = state.Evaluated && slices.Contains(changed, name)
			}
		}
		audit["rule_states"] = states

		audit["attribution_classification"] = c.GetString("claude5_compat_attribution")
		audit["b1_status"] = c.GetString("claude5_compat_b1_status")
		if change, ok := c.Get("claude5_compat_version_change"); ok && change != nil {
			audit["version_change"] = change
		}
	}()
	stage = "attribution"
	if settings.AttributionMode == "remove" || settings.AttributionMode == "forge" {
		states.evaluate("attribution_removed", false)
		states.evaluate("attribution_forged", false)
		if !settings.ThinkingBindingRecover {
			return nil, fmt.Errorf("Claude attribution compatibility requires thinking binding recovery")
		}
		originalSystem := request["system"]
		if err := applyClaude5Attribution(c, request, settings, &changed); err != nil {
			return nil, err
		}
		if slices.Contains(changed, "attribution_removed") || slices.Contains(changed, "attribution_forged") {
			// Check even the first turn, before there is thinking history. Otherwise
			// an unavailable beta would cause different prefixes on later turns.
			bindingCapability = claude5BetaCapabilityForRequest(c, settings, modelID, region)
			if bindingCapability != "supported" {
				request["system"] = originalSystem
				changed = slices.DeleteFunc(changed, func(rule string) bool {
					return rule == "attribution_removed" || rule == "attribution_forged"
				})
				changed = appendUnique(changed, "diag_attribution_skipped_binding_unavailable")
				if c != nil {
					c.Set("claude5_compat_version_change", nil)
				}
			}
		}
	}

	stage = "message_validation"
	var messages []map[string]claude5JSON
	messagesValid := false
	readThinkingMessages := settings.ThinkingBindingRecover
	readMessageFields := settings.StripContainerFields || settings.FixToolResultCacheControl
	if readThinkingMessages {
		states.evaluate("thinking_beta_injected", false)
		states.evaluate("thinking_binding_recovered", false)
	}
	if readThinkingMessages || readMessageFields {
		rawMessages, exists := request["messages"]
		if exists && common.GetJsonType(rawMessages) != "null" && common.GetJsonType(rawMessages) != "array" {
			if readThinkingMessages {
				return nil, fmt.Errorf("messages must be an array")
			}
		} else if exists && common.GetJsonType(rawMessages) == "array" {
			messages, messagesValid = claude5Messages(rawMessages)
			if !messagesValid && readThinkingMessages {
				return nil, fmt.Errorf("invalid messages array")
			}
		}
	}
	stage = "thinking_validation"
	if settings.ThinkingDisabledMap || settings.ThinkingEnabledMap || settings.ThinkingDisplayNormalize || settings.ThinkingBindingRecover {
		states.evaluate("thinking_disabled_mapped", false)
		states.evaluate("thinking_enabled_mapped", false)
		states.evaluate("thinking_display_normalized", false)
		if rawThinking, exists := request["thinking"]; exists {
			var thinking map[string]claude5JSON
			if common.GetJsonType(rawThinking) != "object" || common.Unmarshal(rawThinking, &thinking) != nil || thinking == nil {
				return nil, fmt.Errorf("thinking must be an object")
			}
			if rawType, exists := thinking["type"]; exists && common.GetJsonType(rawType) != "string" {
				return nil, fmt.Errorf("thinking.type must be a string")
			}
			var kind string
			_ = common.Unmarshal(thinking["type"], &kind)
			for _, rule := range []struct{ name, kind string }{{"thinking_disabled_mapped", "disabled"}, {"thinking_enabled_mapped", "enabled"}, {"thinking_display_normalized", "adaptive"}} {
				if state := states[rule.name]; state != nil {
					state.Matched = kind == rule.kind
				}
			}
			if rawBudget, exists := thinking["budget_tokens"]; exists {
				var budget json.Number
				if common.GetJsonType(rawBudget) != "number" || common.Unmarshal(rawBudget, &budget) != nil || strings.ContainsAny(budget.String(), ".eE+-") {
					return nil, fmt.Errorf("thinking.budget_tokens must be a non-negative integer")
				}
				if value, err := strconv.ParseUint(budget.String(), 10, 53); err != nil || value > 1<<53-1 {
					return nil, fmt.Errorf("thinking.budget_tokens is outside safe integer range")
				}
			}
			if adaptive, exists := thinking["adaptive"]; exists && common.GetJsonType(adaptive) != "object" {
				return nil, fmt.Errorf("thinking.adaptive must be an object")
			}
		}
	}
	stage = "system_validation"
	if (settings.AttributionMode == "remove" || settings.AttributionMode == "forge") || settings.CleanEmptySystem {
		if rawSystem, exists := request["system"]; exists && common.GetJsonType(rawSystem) != "array" && common.GetJsonType(rawSystem) != "string" {
			return nil, fmt.Errorf("system must be an array")
		}
	}
	stage = "message_validation"
	hasThinkingBlock := false
	if messagesValid && readThinkingMessages {
		for _, message := range messages {
			raw, exists := message["content"]
			if !exists || common.GetJsonType(raw) == "null" || common.GetJsonType(raw) == "string" {
				continue
			}
			blocks, ok := claude5ContentBlocks(raw)
			if !ok {
				return nil, fmt.Errorf("message content must be a block array")
			}
			for _, block := range blocks {
				var kind string
				_ = common.Unmarshal(block["type"], &kind)
				if kind == "thinking" || kind == "redacted_thinking" {
					hasThinkingBlock = true
				}
			}
		}
	}

	stage = "thinking_mapping"
	if err := applyClaude5ThinkingMapping(request, settings, modelID, &changed); err != nil {
		return nil, err
	}
	var finalThinking map[string]claude5JSON
	var finalThinkingType string
	if rawThinking, exists := request["thinking"]; exists && common.GetJsonType(rawThinking) == "object" {
		if common.Unmarshal(rawThinking, &finalThinking) == nil {
			_ = common.Unmarshal(finalThinking["type"], &finalThinkingType)
		}
	}
	if finalThinkingType == "disabled" {
		if _, supplied := finalThinking["block_binding"]; supplied {
			delete(finalThinking, "block_binding")
			request["thinking"] = mustClaude5Raw(finalThinking)
			changed = appendUnique(changed, "block_binding_stripped")
			audit["block_binding_strip_reason"] = "client_supplied_disabled"
		}
		var extras []string
		for _, key := range []string{"budget_tokens", "display", "adaptive"} {
			if _, exists := finalThinking[key]; exists {
				extras = append(extras, key)
			}
		}
		if len(extras) > 0 {
			audit["disabled_extra_fields"] = extras
		}
	}
	// B2 must run before B1.  In particular, B1 must see the final thinking
	// shape and must never be overwritten by a later mapping pass.
	betaInjected := false
	for _, name := range []string{"thinking_beta_injected", "thinking_binding_recovered"} {
		if state := states[name]; state != nil {
			state.Matched = hasThinkingBlock
		}
	}
	stage = "binding_recovery"
	if settings.ThinkingBindingRecover && hasThinkingBlock {
		capability := bindingCapability
		if capability == "" {
			capability = claude5BetaCapabilityForRequest(c, settings, modelID, region)
		}
		if capability != "supported" {
			status := "b1_capability_unknown"
			if capability == "unsupported" {
				status = "b1_skipped_unsupported"
			} else if capability == "redis_error" {
				status = "b1_skipped_redis_error"
			}
			if c != nil {
				c.Set("claude5_compat_b1_status", status)
			}
			changed = appendUnique(changed, status)
		} else if finalThinkingType == "disabled" {
			if c != nil {
				c.Set("claude5_compat_b1_status", "disabled_skipped")
			}
		} else {
			writeBinding := settings.BlockBindingWrite
			if finalThinkingType == "between_tools" {
				writeBinding = false
			}
			if c != nil {
				if writeBinding {
					c.Set("claude5_compat_b1_status", "binding_written")
				} else {
					c.Set("claude5_compat_b1_status", "beta_only")
				}
			}
			previousBetas := request["anthropic_beta"]
			bindingChanged, injected, err := addThinkingBindingRecovery(request, writeBinding)
			if err != nil {
				if c != nil {
					c.Set("claude5_compat_b1_status", "b1_type_conflict")
				}
				return nil, err
			}
			betaInjected = injected
			if !bytes.Equal(previousBetas, request["anthropic_beta"]) {
				changed = appendUnique(changed, "thinking_beta_injected")
			}
			if bindingChanged {
				changed = appendUnique(changed, "thinking_binding_recovered")
			}
		}
	}
	stage = "output_config"
	_, hasOutput := request["output_config"]
	states.evaluate("output_config_stripped", hasOutput)
	states.evaluate("output_config_normalized", hasOutput)
	if err := applyClaude5OutputConfigPolicy(request, settings.OutputConfigPolicy, &changed); err != nil {
		return nil, err
	}
	stage = "message_cleaning"
	states.evaluate("container_fields_stripped", false)
	states.evaluate("tool_result_cache_control_moved", false)
	if messagesValid && readMessageFields {
		messagesChanged := false
		for _, message := range messages {
			rawContent, contentExists := message["content"]
			if !contentExists || common.GetJsonType(rawContent) == "null" || common.GetJsonType(rawContent) == "string" {
				continue
			}
			blocks, ok := claude5ContentBlocks(rawContent)
			if !ok {
				if readThinkingMessages {
					return nil, fmt.Errorf("message content must be a block array")
				}
				continue
			}
			messageChanged := false
			for _, block := range blocks {
				var kind string
				_ = common.Unmarshal(block["type"], &kind)
				if kind == "thinking" || kind == "redacted_thinking" {
					hasThinkingBlock = true
				}
				if settings.StripContainerFields && len(block["container"]) > 0 {
					delete(block, "container")
					messageChanged = true
					changed = appendUnique(changed, "container_fields_stripped")
				}
				if settings.FixToolResultCacheControl && kind == "tool_result" {
					moved, reason := moveToolResultCacheControl(block)
					if moved {
						messageChanged = true
						changed = appendUnique(changed, "tool_result_cache_control_moved")
					}
					if reason != "" {
						changed = appendUnique(changed, "diag_"+reason)
					}
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

	stage = "system_cleanup"
	if settings.CleanEmptySystem && common.GetJsonType(request["system"]) == "string" {
		audit["system_cleanup_skip_reason"] = "system_string"
	}
	if err := applyClaude5FieldCleaning(request, settings, &changed, states); err != nil {
		return nil, err
	}

	if c != nil {
		for _, diagnostic := range c.GetStringSlice(claude5CompatContextKey) {
			changed = appendUnique(changed, diagnostic)
		}
	}
	actualChanges := slices.DeleteFunc(append([]string(nil), changed...), func(value string) bool {
		return strings.HasPrefix(value, "diag_") || strings.HasPrefix(value, "b1_capability_") || strings.HasPrefix(value, "b1_skipped_")
	})
	if c != nil {
		if value, ok := c.Get(claude5CompatAuditKey); ok {
			if audit, ok := value.(map[string]any); ok {
				audit["body_changed"] = len(actualChanges) > 0
			}
		}
	}
	if len(actualChanges) == 0 {
		if c != nil && len(changed) > 0 {
			c.Set(claude5CompatContextKey, changed)
			c.Set(claude5CompatBetaContextKey, betaInjected)
			c.Set(claude5CompatModelContextKey, modelID)
			c.Set(claude5CompatRegionContextKey, region)
		}
		return body, nil
	}
	stage = "request_encode"
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var validated map[string]claude5JSON
	if err := common.Unmarshal(encoded, &validated); err != nil || validated == nil {
		return nil, fmt.Errorf("final Claude compatibility body is not a JSON object")
	}
	if c != nil {
		c.Set(claude5CompatContextKey, changed)
		c.Set(claude5CompatBetaContextKey, betaInjected)
		c.Set(claude5CompatModelContextKey, modelID)
		c.Set(claude5CompatRegionContextKey, region)
	}
	return encoded, nil
}

func applyClaude5Attribution(c *gin.Context, request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, changed *[]string) error {
	raw, ok := request["system"]
	if !ok {
		return nil
	}
	if common.GetJsonType(raw) == "string" {
		if c != nil {
			if value, ok := c.Get(claude5CompatAuditKey); ok {
				if audit, ok := value.(map[string]any); ok {
					audit["attribution_skip_reason"] = "system_string"
				}
			}
		}
		return nil
	}
	if common.GetJsonType(raw) != "array" {
		return fmt.Errorf("system must be an array")
	}
	var blocks []claude5JSON
	if common.Unmarshal(raw, &blocks) != nil {
		return fmt.Errorf("invalid system array")
	}
	type attribution struct {
		index  int
		status string
		text   string
	}
	var candidate *attribution
	for i, rawBlock := range blocks {
		if common.GetJsonType(rawBlock) != "object" {
			continue
		}
		var block map[string]claude5JSON
		if common.Unmarshal(rawBlock, &block) != nil {
			continue
		}
		var blockType string
		var text string
		if common.GetJsonType(block["text"]) != "string" || common.Unmarshal(block["text"], &text) != nil {
			continue
		}
		trimmedText := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmedText, "x-anthropic-billing-header:") {
			continue
		}
		_ = common.Unmarshal(block["type"], &blockType)
		status := parseClaude5Attribution(trimmedText)
		if blockType != "text" {
			status = "unrecognized"
		}
		if c != nil {
			count := 0
			for key := range parseClaude5AttributionValues(trimmedText) {
				if key != "cc_version" && key != "cc_entrypoint" {
					count++
				}
			}
			if count > 0 {
				setClaude5CompatDiagnostic(c, "diag_unknown_key_present")
			}
			if value, ok := c.Get(claude5CompatAuditKey); ok {
				if audit, ok := value.(map[string]any); ok && count > 0 {
					audit["unknown_key_present"] = true
					audit["unknown_key_count"] = count
				}
			}
		}

		if candidate != nil {
			if c != nil {
				c.Set("claude5_compat_attribution", "duplicate")
			}
			*changed = appendUnique(*changed, "diag_duplicate_attribution")
			return nil
		}
		candidate = &attribution{index: i, status: status, text: trimmedText}
	}
	if candidate == nil {
		return nil
	}
	if c != nil {
		c.Set("claude5_compat_attribution", candidate.status)
	}
	if candidate.status != "pure" {
		if candidate.status == "mixed" {
			*changed = appendUnique(*changed, "diag_mixed_skipped")
		} else {
			*changed = appendUnique(*changed, "diag_unrecognized_skipped")
			if _, exists := parseClaude5AttributionValues(candidate.text)["cc_version"]; exists {
				*changed = appendUnique(*changed, "diag_unrecognized_attribution_version")
			}
		}
		return nil
	}
	if settings.AttributionMode == "remove" {
		blocks = append(blocks[:candidate.index], blocks[candidate.index+1:]...)
		*changed = appendUnique(*changed, "attribution_removed")
		if len(blocks) == 0 {
			delete(request, "system")
			return nil
		}
	} else if settings.AttributionMode == "forge" {
		if settings.FingerprintPolicy == "keep_original" {
			return nil
		}
		var block map[string]claude5JSON
		if err := common.Unmarshal(blocks[candidate.index], &block); err != nil {
			return err
		}
		var text string
		if err := common.Unmarshal(block["text"], &text); err != nil {
			return err
		}
		values := parseClaude5AttributionValues(candidate.text)
		major, minor, patch, ok := parseClaude5Version(values["cc_version"])
		if !ok {
			*changed = appendUnique(*changed, "version_unparseable")
			return nil
		}
		thresholdMajor, thresholdMinor, thresholdPatch, ok := parseClaude5Version(settings.ForgeVersion)
		if !ok {
			return fmt.Errorf("invalid forge version")
		}
		if compareClaude5Versions(major, minor, patch, thresholdMajor, thresholdMinor, thresholdPatch) >= 0 {
			return nil
		}
		versionParts := strings.Split(values["cc_version"], ".")
		version := settings.ForgeVersion
		if len(versionParts) > 3 && settings.FingerprintPolicy == "preserve" {
			version += "." + strings.Join(versionParts[3:], ".")
		}
		if _, exists := values["cc_version"]; !exists {
			return nil
		}
		var replaced bool
		text, replaced = replaceClaude5AttributionValue(text, "cc_version", version)
		if !replaced {
			return fmt.Errorf("unable to replace Claude attribution version")
		}
		block["text"] = mustClaude5Raw(text)
		blocks[candidate.index] = mustClaude5Raw(block)
		if c != nil {
			c.Set("claude5_compat_version_change", map[string]any{
				"from_semver": fmt.Sprintf("%d.%d.%d", major, minor, patch), "to_semver": settings.ForgeVersion, "opaque_suffix_present": len(versionParts) > 3,
			})
		}
		*changed = appendUnique(*changed, "attribution_forged")
	}
	request["system"] = mustClaude5Raw(blocks)
	return nil
}

func parseClaude5AttributionValues(text string) map[string]string {
	const prefix = "x-anthropic-billing-header:"
	rest, _ := strings.CutPrefix(text, prefix)
	values := make(map[string]string)
	for _, raw := range strings.Split(rest, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(raw), "=")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func replaceClaude5AttributionValue(text, targetKey, replacement string) (string, bool) {
	const prefix = "x-anthropic-billing-header:"
	rest, ok := strings.CutPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), prefix)
	if !ok {
		return text, false
	}
	offset := len(text) - len(rest)
	for item := range strings.SplitSeq(rest, ";") {
		trimmed := strings.TrimSpace(item)
		key, value, found := strings.Cut(trimmed, "=")
		if !found || strings.TrimSpace(key) != targetKey {
			offset += len(item) + 1
			continue
		}
		leading := len(item) - len(strings.TrimLeftFunc(item, unicode.IsSpace))
		keyStart := leading + len(strings.TrimSpace(key))
		separator := strings.Index(item[keyStart:], "=") + keyStart
		valueStart := separator + 1
		for valueStart < len(item) && (item[valueStart] == ' ' || item[valueStart] == '\t') {
			valueStart++
		}
		valueEnd := len(item)
		for valueEnd > valueStart && (item[valueEnd-1] == ' ' || item[valueEnd-1] == '\t') {
			valueEnd--
		}
		if strings.TrimSpace(value) == "" {
			return text, false
		}
		start := offset + valueStart
		end := offset + valueEnd
		return text[:start] + replacement + text[end:], true
	}
	return text, false
}

func compareClaude5Versions(major, minor, patch, otherMajor, otherMinor, otherPatch int) int {
	left := [3]int{major, minor, patch}
	right := [3]int{otherMajor, otherMinor, otherPatch}
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

func parseClaude5Attribution(text string) string {
	const prefix = "x-anthropic-billing-header:"
	rest, ok := strings.CutPrefix(text, prefix)
	if !ok {
		return "unrecognized"
	}
	if strings.ContainsAny(rest, "\r\n") {
		return "mixed"
	}
	values := make(map[string]string)
	for _, raw := range strings.Split(rest, ";") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		key, value, found := strings.Cut(item, "=")
		if !found || strings.Contains(value, "=") || strings.TrimSpace(value) == "" {
			return "mixed"
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// Known metadata values are single tokens. Whitespace or control
		// characters inside a value can hide merged business instructions;
		// preserve the whole block instead of deleting or rewriting it.
		if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return "mixed"
		}
		if key != "cc_version" && key != "cc_entrypoint" {
			return "mixed"
		}
		if _, exists := values[key]; exists {
			return "mixed"
		}
		values[key] = value
	}
	if len(values) == 0 {
		return "unrecognized"
	}
	if _, ok := values["cc_version"]; !ok {
		return "unrecognized"
	}
	if _, _, _, ok := parseClaude5Version(values["cc_version"]); !ok {
		return "unrecognized"
	}
	return "pure"
}

func parseClaude5Version(value string) (int, int, int, bool) {
	parts := strings.Split(value, ".")
	if len(parts) < 3 {
		return 0, 0, 0, false
	}
	parsed := [3]int{}
	for i := range 3 {
		if len(parts[i]) == 0 || len(parts[i]) > 9 || (len(parts[i]) > 1 && parts[i][0] == '0') {
			return 0, 0, 0, false
		}
		for _, char := range parts[i] {
			if char < '0' || char > '9' {
				return 0, 0, 0, false
			}
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return 0, 0, 0, false
		}
		parsed[i] = n
	}
	return parsed[0], parsed[1], parsed[2], true
}

func claude5Messages(raw claude5JSON) ([]map[string]claude5JSON, bool) {
	if common.GetJsonType(raw) != "array" {
		return nil, false
	}
	var rawMessages []claude5JSON
	if common.Unmarshal(raw, &rawMessages) != nil {
		return nil, false
	}
	messages := make([]map[string]claude5JSON, len(rawMessages))
	for i, item := range rawMessages {
		if common.GetJsonType(item) != "object" || common.Unmarshal(item, &messages[i]) != nil {
			return nil, false
		}
	}
	return messages, true
}

func claude5ContentBlocks(raw claude5JSON) ([]map[string]claude5JSON, bool) {
	if common.GetJsonType(raw) != "array" {
		return nil, false
	}
	var rawBlocks []claude5JSON
	if common.Unmarshal(raw, &rawBlocks) != nil {
		return nil, false
	}
	blocks := make([]map[string]claude5JSON, len(rawBlocks))
	for i, item := range rawBlocks {
		if common.GetJsonType(item) != "object" || common.Unmarshal(item, &blocks[i]) != nil {
			return nil, false
		}
	}
	return blocks, true
}

func addThinkingBindingRecovery(request map[string]claude5JSON, writeBinding bool) (bool, bool, error) {
	var thinking map[string]claude5JSON
	if len(request["thinking"]) > 0 {
		if common.GetJsonType(request["thinking"]) != "object" || common.Unmarshal(request["thinking"], &thinking) != nil {
			return false, false, fmt.Errorf("invalid thinking object")
		}
	}
	if raw, exists := thinking["block_binding"]; exists {
		var binding map[string]claude5JSON
		if common.GetJsonType(raw) != "object" || common.Unmarshal(raw, &binding) != nil {
			return false, false, fmt.Errorf("invalid thinking block binding")
		}
		for _, key := range []string{"prefix_mismatch_behavior", "mismatch_behavior"} {
			if rawValue, exists := binding[key]; exists {
				var value string
				if common.Unmarshal(rawValue, &value) != nil || (value != "error" && value != "drop_block") {
					return false, false, fmt.Errorf("invalid prefix mismatch behavior")
				}
			}
		}
	}
	changed := false
	if writeBinding {
		if thinking == nil {
			thinking = make(map[string]claude5JSON)
		}
		var binding map[string]claude5JSON
		if len(thinking["block_binding"]) > 0 {
			if common.GetJsonType(thinking["block_binding"]) != "object" || common.Unmarshal(thinking["block_binding"], &binding) != nil {
				return false, false, fmt.Errorf("invalid thinking block binding")
			}
		}
		if binding == nil {
			binding = make(map[string]claude5JSON)
		}
		if alias, ok := binding["mismatch_behavior"]; ok {
			var value string
			if common.Unmarshal(alias, &value) != nil || (value != "error" && value != "drop_block") {
				return false, false, fmt.Errorf("invalid mismatch behavior alias")
			}
			delete(binding, "mismatch_behavior")
			changed = true
		}
		if existing, ok := binding["prefix_mismatch_behavior"]; ok {
			var value string
			if common.GetJsonType(existing) != "string" || common.Unmarshal(existing, &value) != nil {
				return false, false, fmt.Errorf("invalid prefix mismatch behavior")
			}
			if value != "drop_block" {
				binding["prefix_mismatch_behavior"] = mustClaude5Raw("drop_block")
				changed = true
			}
		} else {
			binding["prefix_mismatch_behavior"] = mustClaude5Raw("drop_block")
			changed = true
		}
		if changed {
			thinking["block_binding"] = mustClaude5Raw(binding)
			request["thinking"] = mustClaude5Raw(thinking)
		}
	}
	bindingChanged := changed
	var rawBetas []claude5JSON
	if raw, exists := request["anthropic_beta"]; exists {
		if common.GetJsonType(raw) != "array" || common.Unmarshal(raw, &rawBetas) != nil {
			return false, false, fmt.Errorf("invalid anthropic_beta array")
		}
	}
	betas := make([]string, 0, len(rawBetas)+1)
	for _, raw := range rawBetas {
		var value string
		if common.GetJsonType(raw) != "string" || common.Unmarshal(raw, &value) != nil {
			return false, false, fmt.Errorf("invalid anthropic_beta entry")
		}
		betas = appendUnique(betas, value)
	}
	injected := !slices.Contains(betas, claude5CompatBetaName)
	if injected {
		betas = append(betas, claude5CompatBetaName)
	}
	if injected || len(betas) != len(rawBetas) {
		encoded, err := common.Marshal(betas)
		if err != nil {
			return false, false, err
		}
		request["anthropic_beta"] = encoded
	}
	return bindingChanged, injected, nil
}

func claude5BetaCapability(settings model_setting.Claude5CompatSettings, modelID, region string) string {
	return claude5BetaCapabilityForRequest(nil, settings, modelID, region)
}

func claude5BetaCapabilityForRequest(c *gin.Context, settings model_setting.Claude5CompatSettings, modelID, region string) string {
	requestContext := context.Background()
	if c != nil && c.Request != nil {
		requestContext = c.Request.Context()
	}
	if requestContext.Err() != nil {
		setClaude5CompatDiagnostic(c, "diag_b1_request_cancelled")
		return "unknown"
	}
	if len(model_setting.GetGlobalSettings().FilterAwsBetas([]string{claude5CompatBetaName})) == 0 {
		setClaude5CompatDiagnostic(c, "b1_skipped_beta_filtered")
		return "unsupported"
	}
	adminStatus, staticStatus := "", ""
	allowed := false
	for _, entry := range settings.BetaCapabilities {
		if entry.Beta != claude5CompatBetaName || entry.Model != modelID || !strings.EqualFold(entry.Region, region) {
			continue
		}
		if entry.Source == "static" {
			if staticStatus != "deny" {
				staticStatus = entry.Status
			}
			continue
		}
		if entry.Status == "deny" {
			adminStatus = "deny"
			continue
		}
		allowed = true
	}
	if !allowed && staticStatus == "allow" {
		allowed = true
	}
	if adminStatus == "deny" {
		return "unsupported"
	}
	if !allowed && staticStatus == "deny" {
		return "unsupported"
	}
	policy := settings.BindingCapabilityPolicy
	if policy == "" {
		policy = "legacy" // Missing in pre-policy settings: preserve opt-in behavior.
	}
	if policy == "legacy" || (policy == "official" && claudeThinkingProfileForModel(modelID).binding) {
		allowed = true
	}
	if !allowed {
		return "unknown"
	}
	key := claude5BetaCapabilityKey(modelID, region, settings.CapabilityTableVersion)
	if c != nil {
		c.Set(claude5CompatCapabilityKey, key)
	}
	if denied, ok := getClaude5BetaDenyCache(key); ok && denied {
		return "unsupported"
	}
	if common.RedisEnabled && common.RDB != nil {
		if denied, ok := getClaude5BetaDenyCache(key); ok {
			if denied {
				return "unsupported"
			}
			return "supported"
		}
		ctx, cancel := context.WithTimeout(requestContext, 30*time.Millisecond)
		value, err := common.RDB.Get(ctx, key).Result()
		cancel()
		if requestContext.Err() != nil {
			setClaude5CompatDiagnostic(c, "diag_b1_request_cancelled")
			return "unknown"
		}
		if err == nil && value == "unsupported" {
			setClaude5BetaDenyCache(key, true)
			return "unsupported"
		} else if err != nil && err != redis.Nil {
			setClaude5CompatDiagnostic(c, "b1_capability_read_error")
		} else {
			setClaude5BetaDenyCache(key, false)
		}
		if denied, ok := getClaude5BetaDenyCache(key); ok && denied {
			return "unsupported"
		}
	} else if !common.RedisEnabled {
		setClaude5CompatDiagnostic(c, "diag_b1_redis_disabled")
		return "supported"
	} else {
		setClaude5CompatDiagnostic(c, "b1_capability_read_error")
		return "supported"
	}
	return "supported"
}

func claude5BetaCapabilityKey(modelID, region, version string) string {
	if version == "" {
		version = claude5CompatCapabilityVer
	}
	sum := sha256.Sum256([]byte(modelID + "\x00" + strings.ToLower(region) + "\x00" + claude5CompatBetaName + "\x00" + version))
	return fmt.Sprintf("b1cap:%s:%s", claude5CompatCapabilityVer, fmt.Sprintf("%x", sum[:]))
}

func setClaude5CompatDiagnostic(c *gin.Context, values ...string) {
	if c == nil {
		return
	}
	current, _ := c.Get(claude5CompatContextKey)
	items, _ := current.([]string)
	for _, value := range values {
		items = appendUnique(items, value)
	}
	c.Set(claude5CompatContextKey, items)
}

// recordClaude5UpstreamResult observes only the existing invocation; it does
// not retry, probe, or re-send a request to discover provider capabilities.
func recordClaude5UpstreamResult(c *gin.Context, err error) {
	if c == nil {
		return
	}
	value, _ := c.Get(claude5CompatAuditKey)
	audit, ok := value.(map[string]any)
	if !ok {
		return
	}
	if err == nil {
		audit["upstream_status"] = 200
		return
	}
	var response interface{ HTTPStatusCode() int }
	if errors.As(err, &response) {
		audit["upstream_status"] = response.HTTPStatusCode()
	}
	recordClaude5BetaRejection(c, err)
}

func recordClaude5BetaRejection(c *gin.Context, err error) {
	if c == nil || err == nil || !c.GetBool(claude5CompatBetaContextKey) {
		return
	}
	// Inspect the provider error code, not wrapper text: a transport or auth
	// error mentioning ValidationException must never disable a capability.
	var apiError smithy.APIError
	if !errors.As(err, &apiError) || apiError.ErrorCode() != "ValidationException" {
		return
	}
	if !claude5BetaRejectionPattern.MatchString(apiError.ErrorMessage()) {
		return
	}
	modelID, _ := c.Get(claude5CompatModelContextKey)
	region, _ := c.Get(claude5CompatRegionContextKey)
	_, modelOK := modelID.(string)
	_, regionOK := region.(string)
	if !modelOK || !regionOK {
		setClaude5CompatDiagnostic(c, "b1_capability_write_error")
		return
	}
	key := c.GetString(claude5CompatCapabilityKey)
	if key == "" {
		return
	}
	setClaude5BetaDenyCache(key, true)
	if !common.RedisEnabled || common.RDB == nil {
		setClaude5CompatDiagnostic(c, "b1_capability_write_error")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := common.RDB.SetNX(ctx, key, "unsupported", 24*time.Hour).Err(); err != nil {
		setClaude5CompatDiagnostic(c, "b1_capability_write_error")
	}
}

var claude5BetaDenyCache = struct {
	sync.Mutex
	entries map[string]claude5BetaDenyCacheEntry
}{entries: make(map[string]claude5BetaDenyCacheEntry)}

type claude5BetaDenyCacheEntry struct {
	denied  bool
	expires time.Time
}

func getClaude5BetaDenyCache(key string) (bool, bool) {
	claude5BetaDenyCache.Lock()
	defer claude5BetaDenyCache.Unlock()
	entry, ok := claude5BetaDenyCache.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(claude5BetaDenyCache.entries, key)
		return false, false
	}
	return entry.denied, true
}

func setClaude5BetaDenyCache(key string, denied bool) {
	claude5BetaDenyCache.Lock()
	defer claude5BetaDenyCache.Unlock()
	// A missing-key GET started before a rejection must not erase that
	// rejection. Preserve its original expiry so this cannot extend its TTL.
	if current, ok := claude5BetaDenyCache.entries[key]; !denied && ok && current.denied && time.Now().Before(current.expires) {
		return
	}
	if len(claude5BetaDenyCache.entries) >= 4096 {
		now := time.Now()
		for existing, entry := range claude5BetaDenyCache.entries {
			if now.After(entry.expires) {
				delete(claude5BetaDenyCache.entries, existing)
			}
		}
		if len(claude5BetaDenyCache.entries) >= 4096 {
			clear(claude5BetaDenyCache.entries)
		}
	}
	claude5BetaDenyCache.entries[key] = claude5BetaDenyCacheEntry{denied: denied, expires: time.Now().Add(60 * time.Second)}
}

// These are exact model capabilities documented by AWS, not generation-prefix
// guesses. Unknown models remain untouched. Cross-region profiles share the
// foundation model's schema; administrators can override beta support per region.
// https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-adaptive-thinking.html
// https://docs.aws.amazon.com/bedrock/latest/userguide/claude-messages-thinking-block-binding.html
type claudeThinkingProfile struct {
	adaptiveOnly     bool
	disabledMode     string
	disabledHighOnly bool
	binding          bool
}

func claudeThinkingProfileForModel(modelID string) claudeThinkingProfile {
	for _, prefix := range []string{"global.", "us.", "eu.", "apac.", "au.", "jp."} {
		if suffix, ok := strings.CutPrefix(modelID, prefix); ok {
			modelID = suffix
			break
		}
	}
	switch modelID {
	case "anthropic.claude-sonnet-5-5":
		return claudeThinkingProfile{adaptiveOnly: true, disabledMode: "between_tools", binding: true}
	case "anthropic.claude-opus-5-5", "anthropic.claude-fable-5-1":
		return claudeThinkingProfile{adaptiveOnly: true, binding: true}
	case "anthropic.claude-haiku-5-5":
		return claudeThinkingProfile{adaptiveOnly: true, disabledMode: "disabled", disabledHighOnly: true, binding: true}
	case "anthropic.claude-opus-5":
		return claudeThinkingProfile{adaptiveOnly: true, disabledMode: "disabled", disabledHighOnly: true}
	case "anthropic.claude-sonnet-5", "anthropic.claude-opus-4-7", "anthropic.claude-opus-4-8":
		return claudeThinkingProfile{adaptiveOnly: true, disabledMode: "disabled"}
	case "anthropic.claude-fable-5", "anthropic.claude-mythos-5", "anthropic.claude-mythos-5-1":
		return claudeThinkingProfile{adaptiveOnly: true}
	default:
		return claudeThinkingProfile{}
	}
}

func applyClaude5ThinkingMapping(request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, modelID string, changed *[]string) error {
	if !settings.ThinkingDisabledMap && !settings.ThinkingEnabledMap && !settings.ThinkingDisplayNormalize {
		return nil
	}
	var thinking map[string]claude5JSON
	if len(request["thinking"]) == 0 {
		return nil
	}
	if common.GetJsonType(request["thinking"]) != "object" || common.Unmarshal(request["thinking"], &thinking) != nil || thinking == nil {
		return fmt.Errorf("thinking must be an object")
	}
	var kind string
	if rawType, exists := thinking["type"]; exists {
		if common.Unmarshal(rawType, &kind) != nil {
			return fmt.Errorf("thinking.type must be a string")
		}
	}
	before := len(*changed)
	profile := claudeThinkingProfileForModel(modelID)
	if kind == "disabled" && settings.ThinkingDisabledMap {
		target := ""
		var output map[string]claude5JSON
		_ = common.Unmarshal(request["output_config"], &output)
		var effort string
		_ = common.Unmarshal(output["effort"], &effort)
		if profile.disabledMode == "between_tools" {
			if effort == "" || effort == "low" || effort == "medium" || effort == "high" {
				target = "between_tools"
			}
		}
		if target == "" && profile.adaptiveOnly && (profile.disabledMode != "disabled" || (profile.disabledHighOnly && (effort == "xhigh" || effort == "max"))) && settings.AllowThinkingDowngrade {
			target = "adaptive"
		}
		if target != "" {
			thinking["type"] = mustClaude5Raw(target)
			delete(thinking, "budget_tokens")
			delete(thinking, "display")
			delete(thinking, "adaptive")
			*changed = appendUnique(*changed, "thinking_disabled_mapped")
			kind = target
		}
	} else if kind == "enabled" && settings.ThinkingEnabledMap && profile.adaptiveOnly {
		var budget int
		budgetValid := common.Unmarshal(thinking["budget_tokens"], &budget) == nil && budget > 0
		thinking["type"] = mustClaude5Raw("adaptive")
		delete(thinking, "budget_tokens")
		matched := false
		for _, entry := range settings.ThinkingBudgetEffortTable {
			if budgetValid && budget <= entry.MaxBudget {
				var output map[string]claude5JSON
				if raw, exists := request["output_config"]; exists {
					if common.GetJsonType(raw) != "object" || common.Unmarshal(raw, &output) != nil {
						return fmt.Errorf("output_config must be an object when mapping effort")
					}
				}
				if output == nil {
					output = make(map[string]claude5JSON)
				}
				// An explicit client effort takes precedence over the approximate
				// budget mapping. Never discard format or other output fields.
				if _, exists := output["effort"]; !exists {
					output["effort"] = mustClaude5Raw(entry.Effort)
					request["output_config"] = mustClaude5Raw(output)
				}
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
		rawDisplay, exists := thinking["display"]
		var adaptive map[string]claude5JSON
		if raw, legacy := thinking["adaptive"]; legacy {
			if common.Unmarshal(raw, &adaptive) != nil {
				return fmt.Errorf("thinking.adaptive must be an object")
			}
			if legacyDisplay, found := adaptive["display"]; found {
				if exists && !bytes.Equal(rawDisplay, legacyDisplay) {
					return fmt.Errorf("conflicting thinking display fields")
				}
				rawDisplay, exists = legacyDisplay, true
				delete(adaptive, "display")
				if len(adaptive) == 0 {
					delete(thinking, "adaptive")
				} else {
					thinking["adaptive"] = mustClaude5Raw(adaptive)
				}
				thinking["display"] = rawDisplay
				*changed = appendUnique(*changed, "thinking_display_normalized")
			}
		}
		var display string
		_ = common.Unmarshal(rawDisplay, &display)
		if exists && display != "summarized" && display != "omitted" {
			fallback := settings.DisplayDefault
			if fallback != "omitted" {
				fallback = "summarized"
			}
			thinking["display"] = mustClaude5Raw(fallback)
			*changed = appendUnique(*changed, "thinking_display_normalized")
		}
	}
	if len(*changed) > before {
		request["thinking"] = mustClaude5Raw(thinking)
	}
	return nil
}

func applyClaude5OutputConfigPolicy(request map[string]claude5JSON, policy string, changed *[]string) error {
	if policy == "strip_all" {
		if _, exists := request["output_config"]; exists {
			delete(request, "output_config")
			*changed = appendUnique(*changed, "output_config_stripped")
		}
	} else if policy == "keep_effort_only" {
		if raw, exists := request["output_config"]; exists {
			var output map[string]claude5JSON
			if common.GetJsonType(raw) == "object" && common.Unmarshal(raw, &output) == nil {
				var effort string
				if common.GetJsonType(output["effort"]) == "string" && common.Unmarshal(output["effort"], &effort) == nil && (effort == "low" || effort == "medium" || effort == "high" || effort == "xhigh" || effort == "max") {
					if len(output) == 1 {
						return nil
					}
					request["output_config"] = mustClaude5Raw(map[string]string{"effort": effort})
				} else {
					delete(request, "output_config")
				}
			} else {
				delete(request, "output_config")
			}
			*changed = appendUnique(*changed, "output_config_normalized")
		}
	}
	return nil
}

func applyClaude5FieldCleaning(request map[string]claude5JSON, settings model_setting.Claude5CompatSettings, changed *[]string, states claude5RuleStates) error {
	if settings.DropTemperature {
		_, exists := request["temperature"]
		states.evaluate("temperature_dropped", exists)
		if _, exists := request["temperature"]; exists {
			delete(request, "temperature")
			*changed = appendUnique(*changed, "temperature_dropped")
		}
	}
	if settings.CleanEmptySystem {
		states.evaluate("empty_system_cleaned", false)
		if err := cleanClaude5System(request, changed); err != nil {
			return err
		}
	}
	if settings.StripContextManagement {
		_, exists := request["context_management"]
		states.evaluate("context_management_stripped", exists)
		if _, exists := request["context_management"]; exists {
			delete(request, "context_management")
			*changed = appendUnique(*changed, "context_management_stripped")
		}
	}
	states.evaluate("tool_choice_mapped_auto", false)
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
		states.evaluate("unsupported_web_search_stripped", false)
		stripUnsupportedWebSearch(request, changed)
	}
	return nil
}

func cleanClaude5System(request map[string]claude5JSON, changed *[]string) error {
	raw, ok := request["system"]
	if !ok {
		return nil
	}
	if common.GetJsonType(raw) == "string" {
		return nil
	}
	if common.GetJsonType(raw) != "array" {
		return fmt.Errorf("system must be an array")
	}
	var blocks []claude5JSON
	if common.Unmarshal(raw, &blocks) != nil {
		return fmt.Errorf("invalid system array")
	}
	filtered := make([]claude5JSON, 0, len(blocks))
	for _, rawBlock := range blocks {
		var block map[string]claude5JSON
		if common.GetJsonType(rawBlock) != "object" || common.Unmarshal(rawBlock, &block) != nil || block == nil {
			filtered = append(filtered, rawBlock)
			continue
		}
		var kind string
		_ = common.Unmarshal(block["type"], &kind)
		if _, hasText := block["text"]; !hasText || kind != "text" {
			filtered = append(filtered, mustClaude5Raw(block))
			continue
		}
		var text string
		if common.GetJsonType(block["text"]) != "string" || common.Unmarshal(block["text"], &text) != nil {
			filtered = append(filtered, mustClaude5Raw(block))
			continue
		}
		if strings.TrimSpace(text) == "" {
			*changed = appendUnique(*changed, "empty_system_cleaned")
			continue
		}
		filtered = append(filtered, mustClaude5Raw(block))
	}
	if len(filtered) == 0 {
		*changed = appendUnique(*changed, "empty_system_cleaned")
		delete(request, "system")
		return nil
	}
	if len(filtered) != len(blocks) {
		request["system"] = mustClaude5Raw(filtered)
	}
	return nil
}

func moveToolResultCacheControl(block map[string]claude5JSON) (bool, string) {
	content, ok := claude5ContentBlocks(block["content"])
	if !ok {
		return false, ""
	}
	var parentControl claude5JSON
	if len(block["cache_control"]) > 0 {
		parentControl = block["cache_control"]
		if common.GetJsonType(parentControl) != "object" {
			return false, "cache_control_type_invalid"
		}
	}
	// Conflicting cache policies cannot be moved without changing semantics.
	for _, child := range content {
		if control, exists := child["cache_control"]; exists {
			if common.GetJsonType(control) != "object" {
				return false, "cache_control_type_invalid"
			}
			if len(parentControl) > 0 && !bytes.Equal(bytes.TrimSpace(parentControl), bytes.TrimSpace(control)) {
				if !claude5JSONEqual(parentControl, control) {
					return false, "cache_control_conflict"
				}
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
		return false, ""
	}
	block["cache_control"] = parentControl
	block["content"] = mustClaude5Raw(content)
	return true, ""
}

func claude5JSONEqual(left, right claude5JSON) bool {
	var leftValue, rightValue any
	if common.Unmarshal(left, &leftValue) != nil || common.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
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
