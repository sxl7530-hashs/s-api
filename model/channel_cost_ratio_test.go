package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelCostRatioTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf(
		"file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"),
	)), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&Channel{}, &ChannelCostRatioHistory{}))
	return db
}

func TestChannelCostRatioHistoryKeepsEffectiveVersions(t *testing.T) {
	db := setupChannelCostRatioTestDB(t)
	channel := &Channel{Name: "test", Key: "key", Models: "claude", CostRatio: 0.35}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, EnsureChannelCostRatioHistory(db, channel.Id, 0.35, 100))
	require.NoError(t, db.Model(channel).Update("cost_ratio", 1.0).Error)
	require.NoError(t, db.Model(&ChannelCostRatioHistory{}).
		Where("channel_id = ? AND effective_to IS NULL", channel.Id).
		Update("effective_to", 200).Error)
	require.NoError(t, EnsureChannelCostRatioHistory(db, channel.Id, 1.0, 200))

	before, err := GetChannelCostRatioAt(channel.Id, 150)
	require.NoError(t, err)
	after, err := GetChannelCostRatioAt(channel.Id, 250)
	require.NoError(t, err)
	require.Equal(t, 0.35, before)
	require.Equal(t, 1.0, after)
}
