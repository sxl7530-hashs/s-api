package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	modelsetting "github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestValidateOptionValueRejectsInvalidMaxTokenAutoGroups(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("MaxTokenAutoGroups", value))
		})
	}
	require.NoError(t, validateOptionValue("MaxTokenAutoGroups", "999999"))
}

func TestGlobalDisplayAndAwsMetricsOptionsPersistAcrossDatabaseDialects(t *testing.T) {
	dialects := []struct {
		name string
		dsn  string
		open func(string) gorm.Dialector
	}{
		{name: "sqlite", open: func(string) gorm.Dialector { return sqlite.Open(":memory:") }},
		{name: "mysql", dsn: os.Getenv("TEST_MYSQL_DSN"), open: mysql.Open},
		{name: "postgres", dsn: os.Getenv("TEST_POSTGRES_DSN"), open: postgres.Open},
	}
	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			if dialect.name != "sqlite" && dialect.dsn == "" {
				t.Skip("database DSN is not configured")
			}
			db, err := gorm.Open(dialect.open(dialect.dsn), &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: "option_setting_test_"},
			})
			require.NoError(t, err)
			var version string
			versionQuery := "SELECT version()"
			if dialect.name == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("%s version: %s", dialect.name, version)
			previousDB := DB
			previousType := common.MainDatabaseType()
			previousOptions := common.OptionMap
			previousMetrics := modelsetting.GetGlobalSettings().AwsUsageMetricsEnabled
			previousCompat := modelsetting.GetGlobalSettings().Claude5Compat
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect.name))
			common.OptionMapRWMutex.Lock()
			common.OptionMap = make(map[string]string)
			common.OptionMapRWMutex.Unlock()
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
				modelsetting.GetGlobalSettings().AwsUsageMetricsEnabled = previousMetrics
				modelsetting.GetGlobalSettings().Claude5Compat = previousCompat
				sqlDB, dbErr := db.DB()
				if dbErr == nil {
					_ = sqlDB.Close()
				}
			})
			require.NoError(t, db.AutoMigrate(&Option{}))
			compat := `{"enabled":true,"target_models":["target"],"thinking_enabled_map":true,"output_config_policy":"none","binding_capability_policy":"official","allow_thinking_downgrade":true,"drop_temperature":true,"control_percent":20,"audit_sample_percent":25,"fingerprint_statuses":[{"model":"target","region":"us-east-1","status":"preserve_rejected"}]}`
			mappings := `{"enabled":true,"mappings":{"sara":"Supplier 1"}}`
			require.NoError(t, UpdateOptionsBulk(map[string]string{
				"global.aws_usage_metrics_enabled": "true",
				"global.channel_name_display":      mappings,
				"global.claude5_compat":            compat,
			}))
			var stored Option
			require.NoError(t, db.Where(&Option{Key: "global.channel_name_display"}).First(&stored).Error)
			assert.JSONEq(t, mappings, stored.Value)
			assert.True(t, modelsetting.GetGlobalSettings().AwsUsageMetricsEnabled)
			var compatStored Option
			require.NoError(t, db.Where(&Option{Key: "global.claude5_compat"}).First(&compatStored).Error)
			assert.JSONEq(t, compat, compatStored.Value)
			assert.True(t, modelsetting.GetClaude5CompatSettings().DropTemperature)
			assert.True(t, modelsetting.GetClaude5CompatSettings().ThinkingEnabledMap)
			assert.True(t, modelsetting.GetClaude5CompatSettings().AllowThinkingDowngrade)
			assert.Equal(t, "none", modelsetting.GetClaude5CompatSettings().OutputConfigPolicy)
			assert.Equal(t, "official", modelsetting.GetClaude5CompatSettings().BindingCapabilityPolicy)
			assert.Equal(t, 20, modelsetting.GetClaude5CompatSettings().ControlPercent)
			require.NotNil(t, modelsetting.GetClaude5CompatSettings().AuditSamplePercent)
			assert.Equal(t, 25, *modelsetting.GetClaude5CompatSettings().AuditSamplePercent)
			assert.Equal(t, "preserve_rejected", modelsetting.GetClaude5CompatSettings().FingerprintStatuses[0].Status)
			require.NoError(t, UpdateOptionsBulk(map[string]string{"global.claude5_compat": `{}`}))
			assert.False(t, modelsetting.GetClaude5CompatSettings().Enabled)
			assert.False(t, modelsetting.GetClaude5CompatSettings().DropTemperature)
			assert.False(t, modelsetting.GetClaude5CompatSettings().AllowThinkingDowngrade)
			assert.Empty(t, modelsetting.GetClaude5CompatSettings().BindingCapabilityPolicy)
			// Replacing the JSON value must remove stale map entries, not merge them.
			updated := `{"enabled":false,"mappings":{}}`
			require.NoError(t, UpdateOptionsBulk(map[string]string{"global.channel_name_display": updated}))
			require.NoError(t, db.Where(&Option{Key: "global.channel_name_display"}).First(&stored).Error)
			assert.JSONEq(t, updated, stored.Value)
		})
	}
}

func TestValidateGlobalAwsUsageAndChannelNameDisplayOptions(t *testing.T) {
	for _, value := range []string{"", "1", "yes", "TRUE"} {
		assert.Error(t, validateOptionValue("global.aws_usage_metrics_enabled", value))
	}
	for _, value := range []string{
		`null`, `{}`, `{"enabled":"true","mappings":{}}`,
		`{"enabled":true,"mappings":[]}`, `{"enabled":true,"mappings":{" ":"alias"}}`,
		`{"enabled":true,"mappings":{"source":" "}}`,
	} {
		assert.Error(t, validateOptionValue("global.channel_name_display", value), value)
	}
	require.NoError(t, validateOptionValue("global.aws_usage_metrics_enabled", "false"))
	require.NoError(t, validateOptionValue("global.channel_name_display", `{"enabled":false,"mappings":{}}`))
	require.NoError(t, validateOptionValue("global.channel_name_display", `{"enabled":true,"mappings":{"sara":"供应商1"}}`))
}

func TestClaude5CompatibilityOptionValidation(t *testing.T) {
	for _, value := range []string{
		`{"binding_capability_policy":"automatic"}`, `{"control_percent":101}`, `{"control_percent":-1}`, `{"audit_sample_percent":101}`, `{"fingerprint_statuses":[{"model":"target","region":"us-east-1","status":"bad"}]}`, `{"fingerprint_statuses":[{"model":"target","region":"us-east-1","status":"unknown"},{"model":"target","region":"US-EAST-1","status":"remove_verified"}]}`,
		`null`, `[]`, `{"enabled":"true"}`,
		`{"attribution_mode":"remove"}`,
		`{"target_models":["["]}`,
		`{"thinking_budget_effort_table":[{"max_budget":8000,"effort":"low"},{"max_budget":4000,"effort":"high"}]}`,
		`{"thinking_budget_effort_table":[{"max_budget":0,"effort":"low"}]}`,
		`{"forge_version":"2.1.280.123"}`,
		`{"forge_version":"02.1.280"}`,
		`{"attribution_mode":"forge","thinking_binding_recover":true,"fingerprint_policy":"keep_original"}`,
		`{"thinking_enabled_map":true,"output_config_policy":"strip_all"}`,
		`{"thinking_enabled_map":true,"strip_output_config":true,"output_config_policy":"keep_effort_only"}`,
		`{"thinking_budget_effort_table":[{"max_budget":8000,"effort":"max"}]}`,
		`{"beta_capabilities":[{"model":"target","region":"us-east-1","beta":"other","status":"allow"}]}`,
	} {
		assert.Error(t, validateOptionValue("global.claude5_compat", value), value)
	}
	for _, value := range []string{`{"thinking_enabled_map":true}`, `{"thinking_enabled_map":true,"output_config_policy":"none"}`, `{"binding_capability_policy":"official"}`, `{"binding_capability_policy":"legacy"}`, `{"binding_capability_policy":"strict"}`, `{}`, `{"enabled":true,"drop_temperature":true}`, `{"attribution_mode":"remove","thinking_binding_recover":true}`, `{"target_models":["*claude-opus-5*"],"thinking_budget_effort_table":[{"max_budget":8000,"effort":"low"},{"max_budget":32000,"effort":"high"}]}`} {
		require.NoError(t, validateOptionValue("global.claude5_compat", value), value)
	}
}
