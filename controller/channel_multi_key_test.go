package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiKeyStatusDoesNotExposeShortKeys(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	channel := model.Channel{Name: "preview-test", Key: "shortkey\nlong-key-secret-value", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
	require.NoError(t, model.DB.Create(&channel).Error)
	payload, err := common.Marshal(MultiKeyManageRequest{ChannelId: channel.Id, Action: "get_key_status"})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key/manage", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	ManageMultiKeys(c)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	assert.NotContains(t, recorder.Body.String(), "shortkey")
	assert.NotContains(t, recorder.Body.String(), "long-key-secret-value")
	assert.Contains(t, recorder.Body.String(), `"key_preview":"***"`)
}

func TestMultiKeyEnableRestoresOnlyExhaustedChannels(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousCache, previousRedis, previousSQLite := common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousType, previousLogType)
		common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled, common.SQLitePath = previousMaster, previousCache, previousRedis, previousSQLite
	})
	t.Setenv("SQL_DSN", os.Getenv("TEST_CHANNEL_SQL_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	common.IsMasterNode, common.MemoryCacheEnabled, common.RedisEnabled = false, false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "channel.db")
	require.NoError(t, model.InitDB())
	database := model.DB
	sqlDB, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	model.LOG_DB = database
	common.SetLogDatabaseType(common.MainDatabaseType())
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}, &model.AuditLog{}))
	root := &model.User{Username: "multi-key-review-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, database.Create(root).Error)
	t.Cleanup(func() { require.NoError(t, database.Unscoped().Delete(root).Error) })
	versionQuery := "SELECT VERSION()"
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database=%s version=%s", common.MainDatabaseType(), version)

	for _, cacheEnabled := range []bool{false, true} {
		for _, action := range []string{"enable_key", "enable_all_keys"} {
			for _, tc := range []struct {
				name           string
				initialStatus  int
				manualOverride string
				wantStatus     int
			}{
				{name: "key exhaustion restores", initialStatus: common.ChannelStatusEnabled, wantStatus: common.ChannelStatusEnabled},
				{name: "manual disable is preserved", initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "manual disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "status", wantStatus: common.ChannelStatusManuallyDisabled},
				{name: "tag disable after exhaustion is preserved", initialStatus: common.ChannelStatusEnabled, manualOverride: "tag", wantStatus: common.ChannelStatusManuallyDisabled},
			} {
				t.Run(fmt.Sprintf("cache=%t/%s/%s", cacheEnabled, action, tc.name), func(t *testing.T) {
					common.MemoryCacheEnabled = cacheEnabled
					tag := t.Name()
					channel := &model.Channel{Name: t.Name(), Type: 1, Key: "key-one\nkey-two", Status: tc.initialStatus, Models: "test-model", Group: "default", Tag: &tag,
						ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}},
					}
					require.NoError(t, channel.Insert())
					t.Cleanup(func() {
						require.NoError(t, channel.Delete())
						model.InitChannelCache()
					})
					for _, operation := range []string{"disable_key", action} {
						if operation == action {
							if tc.manualOverride == "status" {
								model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation")
							} else if tc.manualOverride == "tag" {
								require.NoError(t, model.DisableChannelByTag(tag))
							}
						}
						payload, err := common.Marshal(MultiKeyManageRequest{ChannelId: channel.Id, Action: operation, KeyIndex: common.GetPointer(0)})
						require.NoError(t, err)
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Set("id", root.Id)
						c.Set("role", common.RoleRootUser)
						c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", bytes.NewReader(payload))
						c.Request.Header.Set("Content-Type", "application/json")
						ManageMultiKeys(c)
						var result struct {
							Success bool `json:"success"`
						}
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
						require.True(t, result.Success, recorder.Body.String())
					}
					loaded, err := model.GetChannelById(channel.Id, true)
					require.NoError(t, err)
					assert.Equal(t, tc.wantStatus, loaded.Status)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyStatusList, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledReason, 0)
					assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledTime, 0)
					var ability model.Ability
					require.NoError(t, database.Where("channel_id = ?", channel.Id).First(&ability).Error)
					assert.Equal(t, tc.wantStatus == common.ChannelStatusEnabled, ability.Enabled)
				})
			}
		}
	}
}

func TestBedrockQuotaUpdateValidatesStoredRegionWithoutReplacingSecret(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	setting := `{"bedrock_protection":{"model_rate_limit_enabled":true,"model_limits":{"friendly":{"rpm":120,"resolved_model":"global.anthropic.claude-sonnet-4-6","quota_region":"us-east-1"}}}}`
	mapping := `{"friendly":"global.anthropic.claude-sonnet-4-6"}`
	channel := model.Channel{Type: 33, Status: common.ChannelStatusEnabled, Name: "aws-quota", Models: "friendly", Group: "default", Key: "test-ak|test-sk|us-east-1", Setting: &setting, ModelMapping: &mapping}
	require.NoError(t, channel.Insert())
	for _, region := range []string{"eu-west-1", "us-east-1"} {
		draft := strings.ReplaceAll(setting, "us-east-1", region)
		body, err := common.Marshal(map[string]any{"id": channel.Id, "type": 33, "name": channel.Name, "models": "friendly", "group": "default", "key": "", "setting": draft, "model_mapping": mapping})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Set("id", 1)
		ctx.Set("role", common.RoleRootUser)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		UpdateChannel(ctx)
		if region == "eu-west-1" {
			assert.Contains(t, recorder.Body.String(), `"success":false`)
			assert.Contains(t, recorder.Body.String(), "region changed")
		} else {
			assert.Contains(t, recorder.Body.String(), `"success":true`)
		}
		stored, err := model.GetChannelById(channel.Id, true)
		require.NoError(t, err)
		assert.Equal(t, "test-ak|test-sk|us-east-1", stored.Key)
		assert.NotContains(t, recorder.Body.String(), "test-sk")
	}
}

func TestBedrockQuotaUpdateCannotBypassTargetValidationWithPartialPatch(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	setting := `{"bedrock_protection":{"model_rate_limit_enabled":true,"model_limits":{"friendly":{"rpm":120,"resolved_model":"global.anthropic.claude-sonnet-4-6","quota_region":"us-east-1"}}}}`
	mapping := `{"friendly":"global.anthropic.claude-sonnet-4-6"}`
	channel := model.Channel{Type: 33, Status: common.ChannelStatusEnabled, Name: "aws-quota", Models: "friendly", Group: "default", Key: "test-ak|test-sk|us-east-1", Setting: &setting, ModelMapping: &mapping}
	require.NoError(t, channel.Insert())
	body, err := common.Marshal(map[string]any{"id": channel.Id, "model_mapping": `{"friendly":"global.anthropic.claude-opus-4-6-v1"}`})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleRootUser)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateChannel(ctx)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "target changed")
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, mapping, stored.GetModelMapping())
}

func TestBedrockQuotaAppendValidatesExistingCredentialRegions(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	setting := `{"bedrock_protection":{"model_rate_limit_enabled":true,"model_limits":{"friendly":{"rpm":120,"resolved_model":"global.anthropic.claude-sonnet-4-6","quota_region":"us-east-1"}}}}`
	mapping := `{"friendly":"global.anthropic.claude-sonnet-4-6"}`
	channel := model.Channel{Type: 33, Status: common.ChannelStatusEnabled, Name: "aws-quota-append", Models: "friendly", Group: "default", Key: "old-ak|old-sk|eu-west-1", Setting: &setting, ModelMapping: &mapping, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1}}
	require.NoError(t, channel.Insert())
	body, err := common.Marshal(map[string]any{"id": channel.Id, "type": 33, "key_mode": "append", "key": "new-ak|new-sk|us-east-1", "setting": setting, "model_mapping": mapping})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleRootUser)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateChannel(ctx)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "region changed")
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "old-ak|old-sk|eu-west-1", stored.Key)
}

func TestBedrockQuotaPreviewDoesNotPersistDraftOrReturnCredentials(t *testing.T) {
	setupTaskPluginBindChannelTest(t)
	mapping := `{"friendly":"global.anthropic.claude-sonnet-4-6"}`
	channel := model.Channel{Type: 33, Name: "aws-preview", Key: "saved-ak|saved-sk|us-east-1", Models: "friendly", ModelMapping: &mapping}
	require.NoError(t, channel.Insert())
	body, err := common.Marshal(map[string]any{"channel_id": channel.Id, "key": "draft-ak|draft-sk|eu-west-1", "models": "draft-model", "model_mapping": "{}", "proxy": ""})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/bedrock/quotas/preview", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	PreviewBedrockQuotas(ctx)
	assert.Contains(t, recorder.Body.String(), "please fill in the channel proxy")
	for _, secret := range []string{"saved-ak", "saved-sk", "draft-ak", "draft-sk"} {
		assert.NotContains(t, recorder.Body.String(), secret)
	}
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, channel.Key, stored.Key)
	assert.Equal(t, channel.Models, stored.Models)
	assert.Equal(t, mapping, stored.GetModelMapping())
}
