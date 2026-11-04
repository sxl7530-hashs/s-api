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
			previousDB := DB
			previousType := common.MainDatabaseType()
			previousOptions := common.OptionMap
			previousMetrics := modelsetting.GetGlobalSettings().AwsUsageMetricsEnabled
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
				sqlDB, dbErr := db.DB()
				if dbErr == nil {
					_ = sqlDB.Close()
				}
			})
			require.NoError(t, db.AutoMigrate(&Option{}))
			mappings := `{"enabled":true,"mappings":{"sara":"Supplier 1"}}`
			require.NoError(t, UpdateOptionsBulk(map[string]string{
				"global.aws_usage_metrics_enabled": "true",
				"global.channel_name_display":      mappings,
			}))
			var stored Option
			require.NoError(t, db.Where(&Option{Key: "global.channel_name_display"}).First(&stored).Error)
			assert.JSONEq(t, mappings, stored.Value)
			assert.True(t, modelsetting.GetGlobalSettings().AwsUsageMetricsEnabled)
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
