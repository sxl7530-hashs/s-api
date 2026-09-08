package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func ChannelHasAvailableKey(channel *Channel) bool {
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return false
	}
	if !channel.ChannelInfo.IsMultiKey {
		return strings.TrimSpace(channel.Key) != ""
	}
	keys := channel.GetKeys()
	for index, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		status, exists := channel.ChannelInfo.MultiKeyStatusList[index]
		if !exists || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

func buildGroupAvailableKeyMap(channels map[int]*Channel) map[string]bool {
	available := make(map[string]bool)
	for _, channel := range channels {
		if !ChannelHasAvailableKey(channel) {
			continue
		}
		for _, group := range channel.GetGroups() {
			if group != "" {
				available[group] = true
			}
		}
	}
	return available
}

// GroupHasAvailableKey reports whether an enabled channel in group has at
// least one enabled key. Model support is deliberately not considered: callers
// use this only to distinguish group-wide key exhaustion from model routing
// failures.
func GroupHasAvailableKey(group string) (bool, error) {
	group = NormalizeChannelGroupFilter(group)
	if group == "" || group == "auto" {
		return true, nil
	}
	if common.MemoryCacheEnabled {
		channelSyncLock.RLock()
		defer channelSyncLock.RUnlock()
		// The cache is populated during startup. Fail open while it is not ready
		// so a transient initialization state does not replace the real routing
		// error with a misleading "no key" message.
		if group2hasAvailableKey == nil {
			return true, nil
		}
		return group2hasAvailableKey[group], nil
	}
	if DB == nil {
		return true, nil
	}

	var channels []*Channel
	err := ApplyChannelGroupFilter(
		DB.Select("id", "status", "key", "group", "channel_info").
			Where("status = ?", common.ChannelStatusEnabled),
		group,
	).Find(&channels).Error
	if err != nil {
		return false, err
	}
	for _, channel := range channels {
		if ChannelHasAvailableKey(channel) {
			return true, nil
		}
	}
	return false, nil
}

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func isChannelIDInList(list []int, channelID int) bool {
	for _, id := range list {
		if id == channelID {
			return true
		}
	}
	return false
}
