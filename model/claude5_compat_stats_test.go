package model

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/clickhouse"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestClaude5CompatStatisticsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres", "clickhouse"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			case "clickhouse":
				if os.Getenv("TEST_CLICKHOUSE_DSN") == "" {
					t.Skip("TEST_CLICKHOUSE_DSN is not configured")
				}
				driver = clickhouse.Open(os.Getenv("TEST_CLICKHOUSE_DSN"))
			}
			prefix := "claude5_stats_test_"
			if dialect == "clickhouse" {
				// Use the native schema in a dedicated test database, as production
				// log storage does; ClickHouse does not use GORM AutoMigrate.
				prefix = ""
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			previous := LOG_DB
			LOG_DB = db
			t.Cleanup(func() {
				LOG_DB = previous
				assert.NoError(t, db.Migrator().DropTable(&Log{}))
				assert.NoError(t, sqlDB.Close())
			})
			if dialect == "clickhouse" {
				require.NoError(t, db.Exec(clickHouseLogCreateTableSQL(0)).Error)
			} else {
				require.NoError(t, db.AutoMigrate(&Log{}))
			}
			var version string
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("%s version: %s", dialect, version)
			metadata := `{"admin_info":{"claude5_compat":{"rules":["temperature_dropped","temperature_dropped"]}}}`
			logs := []Log{
				{CreatedAt: 1000, RequestId: "retry", Type: LogTypeError, ModelName: "a", Other: metadata},
				{CreatedAt: 1400, RequestId: "retry", Type: LogTypeConsume, ModelName: "a", Other: metadata},
				{CreatedAt: 1500, RequestId: "retry", Type: LogTypeError, ModelName: "b", Other: metadata},
				{CreatedAt: 1600, RequestId: "second", Type: LogTypeConsume, ModelName: "a", Other: metadata},
				{CreatedAt: 1700, RequestId: "empty", Type: LogTypeConsume, Other: `null`},
				{CreatedAt: 1800, RequestId: "invalid", Type: LogTypeConsume, Other: `{"admin_info":{"claude5_compat":{"rules":[1]}}}`},
				{CreatedAt: 1900, RequestId: "topup", Type: LogTypeTopup, Other: metadata},
				{CreatedAt: 999, RequestId: "before", Type: LogTypeError, Other: metadata},
				{CreatedAt: 2000, RequestId: "end", Type: LogTypeError, Other: metadata},
				{CreatedAt: 1950, RequestId: "", Type: LogTypeError, Other: metadata},
			}
			require.NoError(t, db.Create(&logs).Error)
			stats, err := GetClaude5CompatStats(t.Context(), 1000, 2000)
			require.NoError(t, err)
			assert.False(t, stats.Partial)
			assert.Equal(t, 7, stats.Scanned)
			assert.Equal(t, 2, stats.Requests)
			assert.Equal(t, []Claude5RuleHit{{Model: "a", Rule: "temperature_dropped", Requests: 2}, {Model: "b", Rule: "temperature_dropped", Requests: 1}}, stats.Rows)
			assert.Equal(t, []Claude5RuleHitBucket{{Start: 1000, Requests: 1}, {Start: 1300}, {Start: 1600, Requests: 1}, {Start: 1900}}, stats.Buckets)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = GetClaude5CompatStats(ctx, 1000, 2000)
			require.ErrorIs(t, err, context.Canceled)
			for _, span := range [][2]int64{{-1, 100}, {100, 100}, {100, 3701}} {
				_, err = GetClaude5CompatStats(t.Context(), span[0], span[1])
				require.Error(t, err)
			}
			// The metadata budget stops before parsing an oversized record.
			// MySQL's existing TEXT column cannot hold this fixture, so keep that
			// application memory boundary on SQLite rather than altering log schemas.
			if dialect == "sqlite" {
				require.NoError(t, db.Create(&Log{CreatedAt: 1999, Type: LogTypeError, RequestId: "oversized", Other: strings.Repeat("x", 8<<20+1)}).Error)
				stats, err = GetClaude5CompatStats(t.Context(), 1000, 2000)
				require.NoError(t, err)
				assert.True(t, stats.Partial)
				assert.Zero(t, stats.Scanned)
				require.NoError(t, db.Where("1 = 1").Delete(&Log{}).Error)
				// Exercise the documented row boundary, including the exact-limit
				// case where the result must not be labelled incomplete.
				entries := make([]Log, 10000)
				for i := range entries {
					entries[i] = Log{CreatedAt: 1500, Type: LogTypeConsume}
				}
				require.NoError(t, db.CreateInBatches(entries, 100).Error)
				stats, err = GetClaude5CompatStats(t.Context(), 1000, 2000)
				require.NoError(t, err)
				assert.Equal(t, 10000, stats.Scanned)
				assert.False(t, stats.Partial)
				require.NoError(t, db.Create(&Log{CreatedAt: 1500, Type: LogTypeConsume}).Error)
				stats, err = GetClaude5CompatStats(t.Context(), 1000, 2000)
				require.NoError(t, err)
				assert.Equal(t, 10000, stats.Scanned)
				assert.True(t, stats.Partial)
			}
		})
	}
}
