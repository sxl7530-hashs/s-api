package service

import (
	"regexp"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

// Chat→Responses upgrade policy is host routing logic (it decides *whether*
// to convert, reading host settings), so it lives here, not in relayconvert.

var chatResponsesRegexCache sync.Map // map[string]*regexp.Regexp

func matchAnyModelPattern(patterns []string, model string) bool {
	if len(patterns) == 0 || model == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		re, ok := chatResponsesRegexCache.Load(pattern)
		if !ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				// Treat invalid patterns as non-matching to avoid breaking runtime traffic.
				continue
			}
			re = compiled
			chatResponsesRegexCache.Store(pattern, re)
		}
		if re.(*regexp.Regexp).MatchString(model) {
			return true
		}
	}
	return false
}

func ShouldChatCompletionsUseResponsesPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, channelID int, channelType int, model string) bool {
	if !policy.IsChannelEnabled(channelID, channelType) {
		return false
	}
	return matchAnyModelPattern(policy.ModelPatterns, model)
}

func ShouldChatCompletionsUseResponsesGlobal(channelID int, channelType int, model string) bool {
	return ShouldChatCompletionsUseResponsesPolicy(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		channelID,
		channelType,
		model,
	) || ShouldUseAzureBackgroundGlobal(channelType, model)
}

// ShouldUseAzureBackgroundGlobal reports whether requests for model on a
// channel of channelType are submitted to Azure in background mode.
func ShouldUseAzureBackgroundGlobal(channelType int, model string) bool {
	settings := model_setting.GetGlobalSettings()
	if !settings.AzureForceBackgroundEnabled || channelType != constant.ChannelTypeAzure {
		return false
	}
	if len(settings.AzureForceBackgroundModels) == 0 {
		return true
	}
	return matchAnyModelPattern(settings.AzureForceBackgroundModels, model)
}
