package model

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
var group2hasAvailableKey map[string]bool
var channel2pollingIndex map[int]int

// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	newChannel2pollingIndex := make(map[int]int)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	newGroup2hasAvailableKey := buildGroupAvailableKeyMap(newChannelId2channel)
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				pollingIndex := channel.ChannelInfo.MultiKeyPollingIndex
				if oldPollingIndex, ok := channel2pollingIndex[i]; ok {
					pollingIndex = oldPollingIndex
				}
				newChannel2pollingIndex[i] = pollingIndex
			}
		}
	}
	channelsIDM = newChannelId2channel
	group2hasAvailableKey = newGroup2hasAvailableKey
	channel2pollingIndex = newChannel2pollingIndex
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return cloneChannelSnapshot(channel), nil
		}
		return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channels[0])
	}

	if retry < 0 {
		retry = 0
	}
	priorityLevel := 0
	var targetPriority int64
	var previousPriority int64
	for i, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
		priority := channel.GetPriority()
		if i == 0 {
			targetPriority = priority
			previousPriority = priority
			continue
		}
		if priority != previousPriority {
			priorityLevel++
			previousPriority = priority
			if priorityLevel <= retry {
				targetPriority = priority
			}
		}
	}

	var sumWeight = 0
	var targetCount int
	for _, channelId := range channels {
		channel := channelsIDM[channelId]
		if channel.GetPriority() == targetPriority {
			sumWeight += channel.GetWeight()
			targetCount++
		}
	}

	if targetCount == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = targetCount * 100
		smoothingAdjustment = 100
	} else if sumWeight/targetCount < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, channelId := range channels {
		channel := channelsIDM[channelId]
		if channel.GetPriority() != targetPriority {
			continue
		}
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return cloneChannelSnapshot(channel), nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return cloneChannelSnapshot(c), nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	channelInfo := cloneChannelInfo(c.ChannelInfo)
	if pollingIndex, ok := channel2pollingIndex[id]; ok {
		channelInfo.MultiKeyPollingIndex = pollingIndex
	}
	return &channelInfo, nil
}

func cloneChannelInfo(channelInfo ChannelInfo) ChannelInfo {
	cloned := channelInfo
	if channelInfo.MultiKeyStatusList != nil {
		cloned.MultiKeyStatusList = make(map[int]int, len(channelInfo.MultiKeyStatusList))
		for index, status := range channelInfo.MultiKeyStatusList {
			cloned.MultiKeyStatusList[index] = status
		}
	}
	if channelInfo.MultiKeyDisabledReason != nil {
		cloned.MultiKeyDisabledReason = make(map[int]string, len(channelInfo.MultiKeyDisabledReason))
		for index, reason := range channelInfo.MultiKeyDisabledReason {
			cloned.MultiKeyDisabledReason[index] = reason
		}
	}
	if channelInfo.MultiKeyDisabledTime != nil {
		cloned.MultiKeyDisabledTime = make(map[int]int64, len(channelInfo.MultiKeyDisabledTime))
		for index, disabledTime := range channelInfo.MultiKeyDisabledTime {
			cloned.MultiKeyDisabledTime[index] = disabledTime
		}
	}
	return cloned
}

func cloneChannelForCache(channel *Channel) *Channel {
	if channel == nil {
		return nil
	}
	cloned := *channel
	cloned.ChannelInfo = cloneChannelInfo(channel.ChannelInfo)
	cloned.Keys = append([]string(nil), channel.Keys...)
	return &cloned
}

func cloneChannelSnapshot(channel *Channel) *Channel {
	if channel == nil {
		return nil
	}
	cloned := *channel
	return &cloned
}

func CacheDeleteChannel(id int) {
	CacheDeleteChannels([]int{id})
}

func CacheDeleteChannels(ids []int) {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		if len(ids) > 0 {
			invalidateTaskAliasView()
		}
		return
	}
	if len(ids) == 0 {
		return
	}
	channelSyncLock.Lock()
	changed := false
	pricingAffected := false
	taskAliasAffected := false
	affectedGroups := make(map[string]struct{})
	for _, id := range ids {
		oldChannel := channelsIDM[id]
		if oldChannel == nil {
			continue
		}
		refreshChannelRoutingLocked(oldChannel, &Channel{Id: id, Status: common.ChannelStatusManuallyDisabled})
		pricingAffected = pricingAffected || channelAffectsPricing(oldChannel)
		taskAliasAffected = taskAliasAffected || channelAffectsTaskAliases(oldChannel)
		addAffectedChannelGroups(affectedGroups, oldChannel)
		delete(channelsIDM, id)
		delete(channel2pollingIndex, id)
		delete(channel2advancedCustomConfig, id)
		changed = true
	}
	if changed {
		refreshGroupAvailableKeysLocked(affectedGroups)
	}
	channelSyncLock.Unlock()
	if !changed {
		return
	}
	if pricingAffected {
		InvalidatePricingCache()
	}
	if taskAliasAffected {
		invalidateTaskAliasView()
	}
}

func CacheDeleteChannelsByStatus(statuses ...int) {
	if len(statuses) == 0 {
		return
	}
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		invalidateTaskAliasView()
		return
	}
	statusSet := make(map[int]struct{}, len(statuses))
	for _, status := range statuses {
		statusSet[status] = struct{}{}
	}
	channelSyncLock.RLock()
	ids := make([]int, 0)
	for id, channel := range channelsIDM {
		if _, ok := statusSet[channel.Status]; ok {
			ids = append(ids, id)
		}
	}
	channelSyncLock.RUnlock()
	CacheDeleteChannels(ids)
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	changed := false
	pricingAffected := false
	taskAliasAffected := false
	if channel, ok := channelsIDM[id]; ok {
		if channel.Status == status {
			channelSyncLock.Unlock()
			return
		}
		affectedGroups := make(map[string]struct{})
		addAffectedChannelGroups(affectedGroups, channel)
		updatedChannel := cloneChannelForCache(channel)
		updatedChannel.Status = status
		channelsIDM[id] = updatedChannel
		// Remove any stale route entry, then add the channel back only when the
		// new status is enabled. This handles both disable and re-enable without
		// rebuilding every channel from the database.
		refreshChannelRoutingLocked(channel, updatedChannel)
		refreshGroupAvailableKeysLocked(affectedGroups)
		changed = true
		pricingAffected = channelAffectsPricing(channel)
		taskAliasAffected = channelAffectsTaskAliases(channel)
	}
	channelSyncLock.Unlock()
	if !changed {
		return
	}
	if pricingAffected {
		InvalidatePricingCache()
	}
	if taskAliasAffected {
		invalidateTaskAliasView()
	}
}

func CacheSetChannelPollingIndex(id int, pollingIndex int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if _, ok := channelsIDM[id]; ok {
		if channel2pollingIndex == nil {
			channel2pollingIndex = make(map[int]int)
		}
		channel2pollingIndex[id] = pollingIndex
	}
	channelSyncLock.Unlock()
}

func addAffectedChannelGroups(groups map[string]struct{}, channel *Channel) {
	if channel == nil {
		return
	}
	for _, group := range channel.GetGroups() {
		if group != "" {
			groups[group] = struct{}{}
		}
	}
}

func refreshGroupAvailableKeysLocked(groups map[string]struct{}) {
	if len(groups) == 0 {
		return
	}
	if group2hasAvailableKey == nil {
		group2hasAvailableKey = make(map[string]bool)
	}
	for group := range groups {
		group2hasAvailableKey[group] = false
	}
	for _, channel := range channelsIDM {
		if !ChannelHasAvailableKey(channel) {
			continue
		}
		for _, group := range channel.GetGroups() {
			if _, affected := groups[group]; affected {
				group2hasAvailableKey[group] = true
			}
		}
	}
	for group := range groups {
		if !group2hasAvailableKey[group] {
			delete(group2hasAvailableKey, group)
		}
	}
}

func refreshChannelRoutingLocked(oldChannel, channel *Channel) {
	if group2model2channels == nil {
		group2model2channels = make(map[string]map[string][]int)
	}
	if oldChannel != nil {
		for _, group := range strings.Split(oldChannel.Group, ",") {
			model2channels := group2model2channels[group]
			for _, modelName := range strings.Split(oldChannel.Models, ",") {
				channelIDs := model2channels[modelName]
				kept := channelIDs[:0]
				for _, channelID := range channelIDs {
					if channelID != channel.Id {
						kept = append(kept, channelID)
					}
				}
				if len(kept) == 0 {
					delete(model2channels, modelName)
				} else {
					model2channels[modelName] = kept
				}
			}
			if len(model2channels) == 0 {
				delete(group2model2channels, group)
			}
		}
	}
	if channel.Status != common.ChannelStatusEnabled {
		return
	}
	for _, group := range strings.Split(channel.Group, ",") {
		if group2model2channels[group] == nil {
			group2model2channels[group] = make(map[string][]int)
		}
		for _, modelName := range strings.Split(channel.Models, ",") {
			channelIDs := append(group2model2channels[group][modelName], channel.Id)
			sort.Slice(channelIDs, func(i, j int) bool {
				left := channelsIDM[channelIDs[i]]
				right := channelsIDM[channelIDs[j]]
				if left == nil || right == nil {
					return channelIDs[i] < channelIDs[j]
				}
				return left.GetPriority() > right.GetPriority()
			})
			group2model2channels[group][modelName] = channelIDs
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	CacheUpdateChannels([]*Channel{channel})
}

func CacheUpdateChannels(channels []*Channel) {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		if len(channels) > 0 {
			invalidateTaskAliasView()
		}
		return
	}
	if len(channels) == 0 {
		return
	}
	channelSyncLock.Lock()
	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	if channel2pollingIndex == nil {
		channel2pollingIndex = make(map[int]int)
	}
	changed := false
	pricingAffected := false
	taskAliasAffected := false
	affectedGroups := make(map[string]struct{})
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		channel = cloneChannelForCache(channel)
		oldChannel := channelsIDM[channel.Id]
		pricingAffected = pricingAffected || channelUpdateAffectsPricing(oldChannel, channel)
		taskAliasAffected = taskAliasAffected || channelUpdateAffectsTaskAliases(oldChannel, channel)
		routingAffected := channelUpdateAffectsRouting(oldChannel, channel)
		if channelUpdateAffectsKeyAvailability(oldChannel, channel) {
			addAffectedChannelGroups(affectedGroups, oldChannel)
			addAffectedChannelGroups(affectedGroups, channel)
		}
		if oldChannel != nil {
			oldPollingIndex, hasPollingIndex := channel2pollingIndex[channel.Id]
			logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldPollingIndex)
			if channel.ChannelInfo.IsMultiKey && channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling &&
				oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling && hasPollingIndex {
				channel.ChannelInfo.MultiKeyPollingIndex = oldPollingIndex
			}
		}
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
		}
		channelsIDM[channel.Id] = channel
		if channel.ChannelInfo.IsMultiKey && channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
			channel2pollingIndex[channel.Id] = channel.ChannelInfo.MultiKeyPollingIndex
		} else {
			delete(channel2pollingIndex, channel.Id)
		}
		if routingAffected {
			refreshChannelRoutingLocked(oldChannel, channel)
		}
		delete(channel2advancedCustomConfig, channel.Id)
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				channel2advancedCustomConfig[channel.Id] = config
			}
		}
		logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
		changed = true
	}
	if changed {
		refreshGroupAvailableKeysLocked(affectedGroups)
	}
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	if !changed {
		return
	}
	if pricingAffected {
		InvalidatePricingCache()
	}
	if taskAliasAffected {
		invalidateTaskAliasView()
	}
}

func channelAffectsPricing(channel *Channel) bool {
	return channel != nil && channel.Status == common.ChannelStatusEnabled
}

func channelUpdateAffectsPricing(oldChannel, channel *Channel) bool {
	if oldChannel == nil {
		return channelAffectsPricing(channel)
	}
	if oldChannel.Status != channel.Status {
		return oldChannel.Status == common.ChannelStatusEnabled || channel.Status == common.ChannelStatusEnabled
	}
	if channel.Status != common.ChannelStatusEnabled {
		return false
	}
	return oldChannel.Type != channel.Type || oldChannel.Models != channel.Models || oldChannel.Group != channel.Group ||
		stringValue(oldChannel.ModelMapping) != stringValue(channel.ModelMapping) || oldChannel.OtherSettings != channel.OtherSettings
}

func channelUpdateAffectsRouting(oldChannel, channel *Channel) bool {
	if oldChannel == nil || channel == nil {
		return true
	}
	return oldChannel.Status != channel.Status || oldChannel.Group != channel.Group || oldChannel.Models != channel.Models ||
		oldChannel.GetPriority() != channel.GetPriority()
}

func channelUpdateAffectsKeyAvailability(oldChannel, channel *Channel) bool {
	if oldChannel == nil || channel == nil {
		return true
	}
	if oldChannel.Group != channel.Group {
		return true
	}
	return ChannelHasAvailableKey(oldChannel) != ChannelHasAvailableKey(channel)
}

func channelAffectsTaskAliases(channel *Channel) bool {
	return channel != nil && channel.Status == common.ChannelStatusEnabled && channel.GetModelMapping() != "" && channel.GetModelMapping() != "{}"
}

func channelUpdateAffectsTaskAliases(oldChannel, channel *Channel) bool {
	if oldChannel == nil {
		return channelAffectsTaskAliases(channel)
	}
	if !channelAffectsTaskAliases(oldChannel) && !channelAffectsTaskAliases(channel) {
		return false
	}
	return oldChannel.Status != channel.Status || oldChannel.Models != channel.Models ||
		stringValue(oldChannel.ModelMapping) != stringValue(channel.ModelMapping)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
