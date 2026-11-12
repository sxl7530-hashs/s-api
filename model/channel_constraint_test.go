package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterCandidateIDs(t *testing.T) {
	alphaSetting := `{"task_plugin_key":"alpha"}`
	betaSetting := `{"task_plugin_key":"beta"}`
	alpha := &Channel{Id: 900001, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Setting: &alphaSetting}
	beta := &Channel{Id: 900002, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Setting: &betaSetting}
	ordinary := &Channel{Id: 900003, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}
	kling := &Channel{Id: 900004, Type: constant.ChannelTypeKling, Status: common.ChannelStatusEnabled}
	jimeng := &Channel{Id: 900005, Type: constant.ChannelTypeJimeng, Status: common.ChannelStatusEnabled}
	matchingCustom := &Channel{Id: 900010, Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled}
	matchingCustom.SetOtherSettings(kitdto.ChannelOtherSettings{
		AdvancedCustom: &kitdto.AdvancedCustomConfig{
			Routes: []kitdto.AdvancedCustomRoute{{
				IncomingPath: "/v1/chat/completions",
				Models:       []string{"gpt-4"},
			}},
		},
	})
	otherCustom := &Channel{Id: 900011, Type: constant.ChannelTypeAdvancedCustom, Status: common.ChannelStatusEnabled}
	otherCustom.SetOtherSettings(kitdto.ChannelOtherSettings{
		AdvancedCustom: &kitdto.AdvancedCustomConfig{
			Routes: []kitdto.AdvancedCustomRoute{{
				IncomingPath: "/v1/responses",
				Models:       []string{"gpt-4"},
			}},
		},
	})

	pathFilter := dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: "/v1/chat/completions"}
	emptyPathFilter := dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: ""}

	tests := []struct {
		name      string
		ids       []int
		modelName string
		filters   []dto.ChannelFilter
		wantKept  []int
		wantEmpty dto.ChannelFilterKind
	}{
		{
			name:      "identity keeps matching type-59 key",
			ids:       []int{900001, 900002},
			modelName: "shared",
			filters:   identityFilters("alpha", nil),
			wantKept:  []int{900001},
		},
		{
			name:      "identity empty key drops all type-59",
			ids:       []int{900001, 900002},
			modelName: "shared",
			filters:   identityFilters("", nil),
			wantKept:  []int{},
			wantEmpty: dto.FilterTaskPluginIdentity,
		},
		{
			name:      "identity empty key keeps ordinary channel",
			ids:       []int{900003},
			modelName: "ordinary",
			filters:   identityFilters("", nil),
			wantKept:  []int{900003},
		},
		{
			name:      "identity keeps matching legacy type",
			ids:       []int{900004, 900005},
			modelName: "legacy",
			filters:   identityFilters("legacy-alpha", []int{constant.ChannelTypeKling}),
			wantKept:  []int{900004},
		},
		{
			name:      "identity keeps all listed legacy types",
			ids:       []int{900004, 900005},
			modelName: "legacy",
			filters:   identityFilters("legacy-alpha", []int{constant.ChannelTypeKling, constant.ChannelTypeJimeng}),
			wantKept:  []int{900004, 900005},
		},
		{
			name:      "identity keyed with no types drops legacy",
			ids:       []int{900004, 900005},
			modelName: "legacy",
			filters:   identityFilters("legacy-alpha", nil),
			wantKept:  []int{},
			wantEmpty: dto.FilterTaskPluginIdentity,
		},
		{
			name:      "identity drops missing cache entry",
			ids:       []int{900004, 999999},
			modelName: "legacy",
			filters:   identityFilters("legacy-alpha", []int{constant.ChannelTypeKling}),
			wantKept:  []int{900004},
		},
		{
			name:      "empty request path is a passthrough including missing ids",
			ids:       []int{900003, 900010, 999999},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{emptyPathFilter},
			wantKept:  []int{900003, 900010, 999999},
		},
		{
			name:      "request path keeps missing cache entry for consistency",
			ids:       []int{900003, 999999},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{pathFilter},
			wantKept:  []int{900003, 999999},
		},
		{
			name:      "request path keeps matching type-58 and ordinary",
			ids:       []int{900003, 900010, 900011},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{pathFilter},
			wantKept:  []int{900003, 900010},
		},
		{
			name:      "request path empties when only unmatched type-58 remains",
			ids:       []int{900011},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{pathFilter},
			wantKept:  []int{},
			wantEmpty: dto.FilterRequestPath,
		},
		{
			name:      "intersection attributes empty set to identity after path keeps candidates",
			ids:       []int{900001, 900010},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{pathFilter, identityFilters("missing", nil)[0]},
			wantKept:  []int{},
			wantEmpty: dto.FilterTaskPluginIdentity,
		},
		{
			name:      "intersection attributes empty set to path when path runs first",
			ids:       []int{900011},
			modelName: "gpt-4",
			filters:   []dto.ChannelFilter{identityFilters("", nil)[0], pathFilter},
			wantKept:  []int{},
			wantEmpty: dto.FilterRequestPath,
		},
	}

	channelSyncLock.Lock()
	previous := channelsIDM
	channelsIDM = map[int]*Channel{
		900001: alpha,
		900002: beta,
		900003: ordinary,
		900004: kling,
		900005: jimeng,
		900010: matchingCustom,
		900011: otherCustom,
	}
	t.Cleanup(func() {
		channelsIDM = previous
		channelSyncLock.Unlock()
	})

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			kept, emptiedBy := filterCandidateIDs(testCase.ids, testCase.modelName, testCase.filters)
			if testCase.wantKept == nil {
				assert.Nil(t, kept)
			} else {
				assert.Equal(t, testCase.wantKept, kept)
			}
			assert.Equal(t, testCase.wantEmpty, emptiedBy)
		})
	}
}

func TestChannelSatisfiesFilters(t *testing.T) {
	alphaSetting := `{"task_plugin_key":"alpha"}`
	alpha := &Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Setting: &alphaSetting}
	ordinary := &Channel{Id: 2, Type: constant.ChannelTypeOpenAI}
	custom := &Channel{Id: 3, Type: constant.ChannelTypeAdvancedCustom}
	custom.SetOtherSettings(kitdto.ChannelOtherSettings{
		AdvancedCustom: &kitdto.AdvancedCustomConfig{
			Routes: []kitdto.AdvancedCustomRoute{{
				IncomingPath: "/v1/chat/completions",
				Models:       []string{"gpt-4"},
			}},
		},
	})

	ok, kind := ChannelSatisfiesFilters(nil, "gpt-4", nil)
	assert.False(t, ok)
	assert.Equal(t, dto.ChannelFilterKind(""), kind)

	ok, kind = ChannelSatisfiesFilters(alpha, "shared", identityFilters("alpha", nil))
	require.True(t, ok)
	assert.Equal(t, dto.ChannelFilterKind(""), kind)

	ok, kind = ChannelSatisfiesFilters(alpha, "shared", identityFilters("beta", nil))
	assert.False(t, ok)
	assert.Equal(t, dto.FilterTaskPluginIdentity, kind)

	ok, kind = ChannelSatisfiesFilters(ordinary, "gpt-4", []dto.ChannelFilter{{
		Kind:        dto.FilterRequestPath,
		RequestPath: "/v1/chat/completions",
	}})
	require.True(t, ok)
	assert.Equal(t, dto.ChannelFilterKind(""), kind)

	ok, kind = ChannelSatisfiesFilters(custom, "gpt-4", []dto.ChannelFilter{{
		Kind:        dto.FilterRequestPath,
		RequestPath: "/v1/responses",
	}})
	assert.False(t, ok)
	assert.Equal(t, dto.FilterRequestPath, kind)
}

func TestGroupHasAvailableKeyIgnoresModelSupport(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true

	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousGroupAvailability := group2hasAvailableKey
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled, Group: "empty", Key: "   ", Models: "gpt-empty"},
		2: {
			Id:     2,
			Status: common.ChannelStatusEnabled,
			Group:  "disabled-multi",
			Key:    "key-a\nkey-b",
			Models: "gpt-disabled",
			ChannelInfo: ChannelInfo{
				IsMultiKey:         true,
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusManuallyDisabled},
			},
		},
		3: {Id: 3, Status: common.ChannelStatusEnabled, Group: "live", Key: "key-live", Models: "different-model"},
		4: {Id: 4, Status: common.ChannelStatusManuallyDisabled, Group: "disabled-channel", Key: "key-live"},
		5: {
			Id:     5,
			Status: common.ChannelStatusEnabled,
			Group:  "live-multi, other",
			Key:    "key-a\nkey-b",
			ChannelInfo: ChannelInfo{
				IsMultiKey:         true,
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
			},
		},
	}
	group2hasAvailableKey = buildGroupAvailableKeyMap(channelsIDM)
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		group2hasAvailableKey = previousGroupAvailability
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	tests := []struct {
		group string
		want  bool
	}{
		{group: "empty", want: false},
		{group: "disabled-multi", want: false},
		{group: "disabled-channel", want: false},
		{group: "missing", want: false},
		{group: "live", want: true},
		{group: "live-multi", want: true},
		{group: "other", want: true},
		{group: "auto", want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.group, func(t *testing.T) {
			hasKey, err := GroupHasAvailableKey(testCase.group)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, hasKey)
		})
	}

	CacheUpdateChannelStatus(3, common.ChannelStatusAutoDisabled)
	hasKey, err := GroupHasAvailableKey("live")
	require.NoError(t, err)
	assert.False(t, hasKey)

	CacheUpdateChannelStatus(3, common.ChannelStatusEnabled)
	hasKey, err = GroupHasAvailableKey("live")
	require.NoError(t, err)
	assert.True(t, hasKey)
}

func TestCacheUpdateChannelRefreshesRoutingIndex(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true

	oldChannel := &Channel{Id: 41, Status: common.ChannelStatusEnabled, Group: "old", Models: "old-model", Key: "old-key"}
	otherChannel := &Channel{Id: 42, Status: common.ChannelStatusEnabled, Group: "new", Models: "new-model", Key: "other-key"}
	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousRouting := group2model2channels
	previousGroupAvailability := group2hasAvailableKey
	previousAdvancedConfigs := channel2advancedCustomConfig
	channelsIDM = map[int]*Channel{41: oldChannel, 42: otherChannel}
	group2model2channels = map[string]map[string][]int{
		"old": {"old-model": {41}},
		"new": {"new-model": {42}},
	}
	group2hasAvailableKey = buildGroupAvailableKeyMap(channelsIDM)
	channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		group2model2channels = previousRouting
		group2hasAvailableKey = previousGroupAvailability
		channel2advancedCustomConfig = previousAdvancedConfigs
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	updated := &Channel{Id: 41, Status: common.ChannelStatusEnabled, Group: "new", Models: "new-model", Key: "new-key", Priority: common.GetPointer(int64(10))}
	CacheUpdateChannel(updated)

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	assert.Empty(t, group2model2channels["old"]["old-model"])
	assert.Equal(t, []int{41, 42}, group2model2channels["new"]["new-model"])
	assert.False(t, group2hasAvailableKey["old"])
	assert.True(t, group2hasAvailableKey["new"])
}

func TestCacheChannelSnapshotsDoNotMutateStoredChannel(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	cachedChannel := &Channel{
		Id:     43,
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusEnabled},
		},
	}
	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousPollingIndexes := channel2pollingIndex
	channelsIDM = map[int]*Channel{43: cachedChannel}
	channel2pollingIndex = map[int]int{43: 2}
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		channel2pollingIndex = previousPollingIndexes
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	channelSnapshot, err := CacheGetChannel(43)
	require.NoError(t, err)
	channelSnapshot.Status = common.ChannelStatusAutoDisabled

	infoSnapshot, err := CacheGetChannelInfo(43)
	require.NoError(t, err)
	assert.Equal(t, 2, infoSnapshot.MultiKeyPollingIndex)
	infoSnapshot.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	assert.Equal(t, common.ChannelStatusEnabled, channelsIDM[43].Status)
	assert.Equal(t, common.ChannelStatusEnabled, channelsIDM[43].ChannelInfo.MultiKeyStatusList[0])
}

func TestGetRandomSatisfiedChannelHonorsRetryPriority(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	highPriority := int64(100)
	lowPriority := int64(50)
	weight := uint(1)

	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousRouting := group2model2channels
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled, Priority: &highPriority, Weight: &weight},
		2: {Id: 2, Status: common.ChannelStatusEnabled, Priority: &lowPriority, Weight: &weight},
		3: {Id: 3, Status: common.ChannelStatusEnabled, Priority: &lowPriority, Weight: &weight},
	}
	group2model2channels = map[string]map[string][]int{
		"default": {"model": {1, 2, 3}},
	}
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		group2model2channels = previousRouting
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	selected, err := GetRandomSatisfiedChannel("default", "model", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 1, selected.Id)

	for _, retry := range []int{1, 99} {
		selected, err = GetRandomSatisfiedChannel("default", "model", retry, nil)
		require.NoError(t, err)
		require.NotNil(t, selected)
		assert.Contains(t, []int{2, 3}, selected.Id)
	}
}

func TestCacheDeleteChannelRemovesRoutingAndChannel(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	channel := &Channel{Id: 51, Status: common.ChannelStatusEnabled, Group: "default", Models: "model", Key: "key"}
	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousRouting := group2model2channels
	previousAvailability := group2hasAvailableKey
	previousAdvancedConfigs := channel2advancedCustomConfig
	channelsIDM = map[int]*Channel{51: channel}
	group2model2channels = map[string]map[string][]int{"default": {"model": {51}}}
	group2hasAvailableKey = buildGroupAvailableKeyMap(channelsIDM)
	channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		group2model2channels = previousRouting
		group2hasAvailableKey = previousAvailability
		channel2advancedCustomConfig = previousAdvancedConfigs
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	CacheDeleteChannel(51)
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	assert.NotContains(t, channelsIDM, 51)
	assert.Empty(t, group2model2channels["default"]["model"])
	assert.False(t, group2hasAvailableKey["default"])
}

func TestCacheBatchUpdateAndDeleteChannels(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	highPriority := int64(10)
	lowPriority := int64(1)
	channelSyncLock.Lock()
	previousChannels := channelsIDM
	previousRouting := group2model2channels
	previousAvailability := group2hasAvailableKey
	previousAdvancedConfigs := channel2advancedCustomConfig
	channelsIDM = map[int]*Channel{
		61: {Id: 61, Status: common.ChannelStatusEnabled, Group: "old", Models: "model", Key: "key-a", Priority: &lowPriority},
		62: {Id: 62, Status: common.ChannelStatusEnabled, Group: "old", Models: "model", Key: "key-b", Priority: &lowPriority},
	}
	group2model2channels = map[string]map[string][]int{"old": {"model": {61, 62}}}
	group2hasAvailableKey = buildGroupAvailableKeyMap(channelsIDM)
	channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = previousChannels
		group2model2channels = previousRouting
		group2hasAvailableKey = previousAvailability
		channel2advancedCustomConfig = previousAdvancedConfigs
		channelSyncLock.Unlock()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	CacheUpdateChannels([]*Channel{
		{Id: 61, Status: common.ChannelStatusEnabled, Group: "new", Models: "model", Key: "key-a", Priority: &highPriority},
		{Id: 62, Status: common.ChannelStatusManuallyDisabled, Group: "old", Models: "model", Key: "key-b", Priority: &lowPriority},
	})
	channelSyncLock.RLock()
	assert.Equal(t, []int{61}, group2model2channels["new"]["model"])
	assert.Empty(t, group2model2channels["old"]["model"])
	assert.True(t, group2hasAvailableKey["new"])
	assert.False(t, group2hasAvailableKey["old"])
	channelSyncLock.RUnlock()

	CacheDeleteChannels([]int{61, 62})
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	assert.Empty(t, channelsIDM)
	assert.Empty(t, group2model2channels)
	assert.Empty(t, group2hasAvailableKey)
}
