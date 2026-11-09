package model_setting

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ChatCompletionsToResponsesPolicy struct {
	Enabled       bool     `json:"enabled"`
	AllChannels   bool     `json:"all_channels"`
	ChannelIDs    []int    `json:"channel_ids,omitempty"`
	ChannelTypes  []int    `json:"channel_types,omitempty"`
	ModelPatterns []string `json:"model_patterns,omitempty"`
}

type ChannelNameDisplaySettings struct {
	Enabled  bool              `json:"enabled"`
	Mappings map[string]string `json:"mappings"`
}

type Claude5CompatSettings struct {
	ControlPercent            int                        `json:"control_percent"`
	FingerprintStatuses       []Claude5FingerprintStatus `json:"fingerprint_statuses"`
	AuditSamplePercent        *int                       `json:"audit_sample_percent,omitempty"`
	Enabled                   bool                       `json:"enabled"`
	TargetModels              []string                   `json:"target_models"`
	TargetRegions             []string                   `json:"target_regions"`
	ApplyToPassthroughUsers   bool                       `json:"apply_to_passthrough_users"`
	AttributionMode           string                     `json:"attribution_mode"`
	ForgeVersion              string                     `json:"forge_version"`
	FingerprintPolicy         string                     `json:"fingerprint_policy"`
	ThinkingBindingRecover    bool                       `json:"thinking_binding_recover"`
	BlockBindingWrite         bool                       `json:"block_binding_write"`
	ThinkingDisabledMap       bool                       `json:"thinking_disabled_map"`
	ThinkingEnabledMap        bool                       `json:"thinking_enabled_map"`
	ThinkingBudgetEffortTable []ThinkingBudgetEffort     `json:"thinking_budget_effort_table"`
	ThinkingDisplayNormalize  bool                       `json:"thinking_display_normalize"`
	DisplayDefault            string                     `json:"display_default"`
	DropTemperature           bool                       `json:"drop_temperature"`
	CleanEmptySystem          bool                       `json:"clean_empty_system"`
	FixToolResultCacheControl bool                       `json:"fix_tool_result_cache_control"`
	StripContextManagement    bool                       `json:"strip_context_management"`
	StripOutputConfig         bool                       `json:"strip_output_config"`
	OutputConfigPolicy        string                     `json:"output_config_policy"`
	BetaCapabilities          []Claude5BetaCapability    `json:"beta_capabilities"`
	CapabilityTableVersion    string                     `json:"capability_table_version"`
	StripContainerFields      bool                       `json:"strip_container_fields"`
	MapToolChoiceAuto         bool                       `json:"map_tool_choice_auto"`
	StripUnsupportedWebSearch bool                       `json:"strip_unsupported_web_search"`
}

// Fingerprint conclusions are administrator-reviewed results, never inferred
// from a generic upstream 400 or changed on the customer request path.
type Claude5FingerprintStatus struct {
	Model  string `json:"model"`
	Region string `json:"region"`
	Status string `json:"status"`
}

type Claude5BetaCapability struct {
	Model  string `json:"model"`
	Region string `json:"region"`
	Beta   string `json:"beta"`
	Status string `json:"status"`           // allow | deny
	Source string `json:"source,omitempty"` // admin (default) | static
}

// ValidateClaude5CompatSettings is shared by option saves and request snapshots.
// Runtime callers bypass this validation entirely when the feature is disabled.
func ValidateClaude5CompatSettings(s Claude5CompatSettings) error {
	invalid := func() error { return common.NewMessage("Invalid parameters") }
	if s.ControlPercent < 0 || s.ControlPercent > 100 || len(s.FingerprintStatuses) > 1000 {
		return invalid()
	}
	seenStatuses := make(map[string]bool)
	for _, entry := range s.FingerprintStatuses {
		key := entry.Model + "\x00" + strings.ToLower(entry.Region)
		if strings.TrimSpace(entry.Model) == "" || len(entry.Model) > 256 || strings.TrimSpace(entry.Region) == "" || len(entry.Region) > 64 || seenStatuses[key] {
			return invalid()
		}
		switch entry.Status {
		case "unknown", "preserve_verified", "preserve_rejected", "remove_verified", "unavailable":
		default:
			return invalid()
		}
		seenStatuses[key] = true
	}
	if s.AuditSamplePercent != nil && (*s.AuditSamplePercent < 0 || *s.AuditSamplePercent > 100) {
		return invalid()
	}
	if s.AttributionMode != "" && s.AttributionMode != "off" && s.AttributionMode != "remove" && s.AttributionMode != "forge" {
		return invalid()
	}
	if s.AttributionMode != "" && s.AttributionMode != "off" && !s.ThinkingBindingRecover {
		return common.NewMessage("Attribution processing requires thinking binding recovery")
	}
	if s.FingerprintPolicy != "" && s.FingerprintPolicy != "preserve" && s.FingerprintPolicy != "remove" && s.FingerprintPolicy != "keep_original" {
		return invalid()
	}
	if s.AttributionMode == "forge" && s.FingerprintPolicy == "keep_original" {
		return invalid()
	}
	policy := s.OutputConfigPolicy
	if policy == "" {
		if s.StripOutputConfig {
			policy = "strip_all"
		} else {
			policy = "none"
		}
	}
	if policy != "none" && policy != "strip_all" && policy != "keep_effort_only" {
		return invalid()
	}
	if s.StripOutputConfig && policy != "strip_all" {
		return invalid()
	}
	if s.ThinkingEnabledMap && policy != "keep_effort_only" {
		return common.NewMessage("Enabled thinking mapping requires keep_effort_only output policy")
	}
	if s.DisplayDefault != "" && s.DisplayDefault != "summarized" && s.DisplayDefault != "omitted" {
		return invalid()
	}
	if s.ForgeVersion != "" {
		parts := strings.Split(s.ForgeVersion, ".")
		if len(parts) != 3 {
			return invalid()
		}
		for _, part := range parts {
			if len(part) == 0 || len(part) > 9 || (len(part) > 1 && part[0] == '0') {
				return invalid()
			}
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return invalid()
				}
			}
		}
	}
	if len(s.TargetModels) > 100 || len(s.TargetRegions) > 50 || len(s.ThinkingBudgetEffortTable) > 20 || len(s.BetaCapabilities) > 1000 || len(s.CapabilityTableVersion) > 64 {
		return invalid()
	}
	for _, pattern := range s.TargetModels {
		if strings.TrimSpace(pattern) == "" || len(pattern) > 256 {
			return invalid()
		}
		if _, err := path.Match(strings.TrimSpace(pattern), ""); err != nil {
			return invalid()
		}
	}
	for _, region := range s.TargetRegions {
		if strings.TrimSpace(region) == "" || len(region) > 64 {
			return invalid()
		}
	}
	for _, entry := range s.BetaCapabilities {
		if strings.TrimSpace(entry.Model) == "" || len(entry.Model) > 256 || strings.TrimSpace(entry.Region) == "" || len(entry.Region) > 64 || entry.Beta != "thinking-binding-controls-2026-08-01" || (entry.Status != "allow" && entry.Status != "deny") || (entry.Source != "" && entry.Source != "admin" && entry.Source != "static") {
			return invalid()
		}
	}
	previousBudget := 0
	for _, entry := range s.ThinkingBudgetEffortTable {
		if entry.MaxBudget <= previousBudget || int64(entry.MaxBudget) > 1<<53-1 || (entry.Effort != "low" && entry.Effort != "medium" && entry.Effort != "high") {
			return invalid()
		}
		previousBudget = entry.MaxBudget
	}
	return nil
}

type ThinkingBudgetEffort struct {
	MaxBudget int    `json:"max_budget"`
	Effort    string `json:"effort"`
}

// UnmarshalJSON replaces the complete policy, so omitted switches and target
// lists cannot retain values from the previously saved configuration.
func (s *Claude5CompatSettings) UnmarshalJSON(data []byte) error {
	type policy Claude5CompatSettings
	var replacement policy
	if err := common.Unmarshal(data, &replacement); err != nil {
		return err
	}
	*s = Claude5CompatSettings(replacement)
	return nil
}

func (p ChatCompletionsToResponsesPolicy) IsChannelEnabled(channelID int, channelType int) bool {
	if !p.Enabled {
		return false
	}
	if p.AllChannels {
		return true
	}

	if channelID > 0 && len(p.ChannelIDs) > 0 && slices.Contains(p.ChannelIDs, channelID) {
		return true
	}
	if channelType > 0 && len(p.ChannelTypes) > 0 && slices.Contains(p.ChannelTypes, channelType) {
		return true
	}
	return false
}

type GlobalSettings struct {
	PassThroughRequestEnabled          bool     `json:"pass_through_request_enabled"`
	AwsPassthroughCompatibilityEnabled bool     `json:"aws_passthrough_compatibility_enabled"`
	ThinkingModelBlacklist             []string `json:"thinking_model_blacklist"`
	// EffortTailModelIDs lists real model IDs that sit inside the GPT/o-series
	// family whitelist but whose names already end in an effort word.
	EffortTailModelIDs               []string                         `json:"effort_tail_model_ids"`
	ChatCompletionsToResponsesPolicy ChatCompletionsToResponsesPolicy `json:"chat_completions_to_responses_policy"`
	// AwsBetaFilterEnabled strips anthropic-beta values listed in AwsBetaBlacklist
	// before forwarding to AWS Bedrock (AWS channels only); toggle via settings.
	AwsBetaFilterEnabled   bool                  `json:"aws_beta_filter_enabled"`
	AwsBetaBlacklist       []string              `json:"aws_beta_blacklist"`
	AwsUsageMetricsEnabled bool                  `json:"aws_usage_metrics_enabled"`
	Claude5Compat          Claude5CompatSettings `json:"claude5_compat"`
	ChannelNameDisplay     string                `json:"channel_name_display"`
}

// 默认配置
var defaultOpenaiSettings = GlobalSettings{
	PassThroughRequestEnabled:          false,
	AwsPassthroughCompatibilityEnabled: false,
	ThinkingModelBlacklist: []string{
		"moonshotai/kimi-k2-thinking",
		"kimi-k2-thinking",
	},
	EffortTailModelIDs: []string{
		"gpt-5.1-codex-max",
		"qwen-image-edit-max",
		"qwen-max",
		"stable-diffusion-3-medium",
		"yi-medium",
	},
	ChatCompletionsToResponsesPolicy: ChatCompletionsToResponsesPolicy{
		Enabled:     false,
		AllChannels: true,
	},
	AwsBetaFilterEnabled: true,
	AwsBetaBlacklist: []string{
		"prompt-caching-2024-07-31",
		"prompt-caching-scope-2026-01-05",
		"advanced-tool-use-2025-11-20",
		"redact-thinking-2026-02-12",
	},
	ChannelNameDisplay: `{"enabled":false,"mappings":{}}`,
}

// 全局实例
var globalSettings = defaultOpenaiSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("global", &globalSettings)
}

func GetGlobalSettings() *GlobalSettings {
	return &globalSettings
}

// IsAwsUsageMetricsEnabled reads the live setting under the same lock used by option updates.
func IsAwsUsageMetricsEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return globalSettings.AwsUsageMetricsEnabled
}

func GetClaude5CompatSettings() Claude5CompatSettings {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	settings := globalSettings.Claude5Compat
	if !settings.Enabled {
		return settings
	}
	if settings.AuditSamplePercent != nil {
		value := *settings.AuditSamplePercent
		settings.AuditSamplePercent = &value
	}
	settings.TargetModels = append([]string(nil), settings.TargetModels...)
	settings.TargetRegions = append([]string(nil), settings.TargetRegions...)
	settings.ThinkingBudgetEffortTable = append([]ThinkingBudgetEffort(nil), settings.ThinkingBudgetEffortTable...)
	settings.FingerprintStatuses = append([]Claude5FingerprintStatus(nil), settings.FingerprintStatuses...)
	settings.BetaCapabilities = append([]Claude5BetaCapability(nil), settings.BetaCapabilities...)
	return settings
}

// FilterAwsBetas removes anthropic-beta values that AWS Bedrock does not accept
// (configured in AwsBetaBlacklist) so they are not forwarded into the Bedrock
// request body. It only filters when AwsBetaFilterEnabled is true and must be
// called for AWS channels only.
func (s *GlobalSettings) FilterAwsBetas(betas []string) []string {
	// Option updates replace both fields under this lock. Read them together
	// so a request cannot combine the old switch with a new blacklist.
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if !s.AwsBetaFilterEnabled || len(betas) == 0 || len(s.AwsBetaBlacklist) == 0 {
		return betas
	}
	blocked := make(map[string]struct{}, len(s.AwsBetaBlacklist))
	for _, b := range s.AwsBetaBlacklist {
		if b = strings.TrimSpace(b); b != "" {
			blocked[b] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return betas
	}
	filtered := make([]string, 0, len(betas))
	for _, b := range betas {
		if _, bad := blocked[strings.TrimSpace(b)]; !bad {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

const thinkingBlacklistRegexPrefix = "re:"

type thinkingBlacklistCompiled struct {
	source  string
	exact   []string
	regexes []*regexp.Regexp
}

var (
	thinkingBlacklistMu    sync.RWMutex
	thinkingBlacklistCache thinkingBlacklistCompiled
)

func thinkingBlacklistSourceKey(entries []string) string {
	return strings.Join(entries, "\x00")
}

func compiledThinkingBlacklist() ([]string, []*regexp.Regexp) {
	entries := globalSettings.ThinkingModelBlacklist
	key := thinkingBlacklistSourceKey(entries)

	thinkingBlacklistMu.RLock()
	if thinkingBlacklistCache.source == key {
		exact, regexes := thinkingBlacklistCache.exact, thinkingBlacklistCache.regexes
		thinkingBlacklistMu.RUnlock()
		return exact, regexes
	}
	thinkingBlacklistMu.RUnlock()

	thinkingBlacklistMu.Lock()
	defer thinkingBlacklistMu.Unlock()
	if thinkingBlacklistCache.source == key {
		return thinkingBlacklistCache.exact, thinkingBlacklistCache.regexes
	}

	exact := make([]string, 0, len(entries))
	var regexes []*regexp.Regexp
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if after, ok := strings.CutPrefix(entry, thinkingBlacklistRegexPrefix); ok {
			pattern := after
			if pattern == "" {
				common.SysError(common.LogText("invalid thinking_model_blacklist regex %q: pattern is empty", entry))
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				common.SysError(common.LogText("invalid thinking_model_blacklist regex %q: %v", entry, err))
				continue
			}
			regexes = append(regexes, re)
			continue
		}
		exact = append(exact, entry)
	}
	thinkingBlacklistCache = thinkingBlacklistCompiled{source: key, exact: exact, regexes: regexes}
	return exact, regexes
}

// ShouldPreserveThinkingSuffix reports whether the full model name is exempt
// from host thinking-suffix and @-modifier parsing. Exact blacklist entries
// match the complete name; entries prefixed with re: are Go regular expressions
// matched with MatchString against the same full name.
func ShouldPreserveThinkingSuffix(modelName string) bool {
	target := strings.TrimSpace(modelName)
	if target == "" {
		return false
	}

	exact, regexes := compiledThinkingBlacklist()
	if slices.Contains(exact, target) {
		return true
	}
	for _, re := range regexes {
		if re.MatchString(target) {
			return true
		}
	}
	return false
}

// ShouldPreserveEffortTail reports whether modelName is a real model ID whose
// name already ends in an effort word. Entries match the complete name and the
// de-namespaced bare name.
func ShouldPreserveEffortTail(modelName string) bool {
	target := strings.TrimSpace(modelName)
	if target == "" {
		return false
	}
	bare := target
	if slash := strings.LastIndex(target, "/"); slash >= 0 {
		bare = target[slash+1:]
	}

	for _, entry := range globalSettings.EffortTailModelIDs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == target || entry == bare {
			return true
		}
	}
	return false
}
