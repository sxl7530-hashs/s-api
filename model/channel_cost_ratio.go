package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ChannelCostRatioHistory stores an effective-dated supplier cost ratio.
type ChannelCostRatioHistory struct {
	Id            int     `json:"id"`
	ChannelId     int     `json:"channel_id" gorm:"index:idx_channel_cost_ratio_effective"`
	Ratio         float64 `json:"ratio"`
	EffectiveFrom int64   `json:"effective_from" gorm:"index:idx_channel_cost_ratio_effective"`
	EffectiveTo   *int64  `json:"effective_to,omitempty" gorm:"index:idx_channel_cost_ratio_effective"`
	CreatedTime   int64   `json:"created_time"`
}

func NormalizeChannelCostRatio(ratio float64) float64 {
	if ratio <= 0 {
		return 1
	}
	return ratio
}

func EnsureChannelCostRatioHistory(tx *gorm.DB, channelId int, ratio float64, at int64) error {
	if channelId <= 0 {
		return errors.New("channel id is required")
	}
	ratio = NormalizeChannelCostRatio(ratio)
	if at <= 0 {
		at = time.Now().Unix()
	}

	var active ChannelCostRatioHistory
	err := tx.Where("channel_id = ? AND effective_to IS NULL", channelId).
		Order("effective_from DESC").First(&active).Error
	if err == nil {
		if active.Ratio == ratio {
			return nil
		}
		if err = tx.Model(&active).Updates(map[string]interface{}{"effective_to": at}).Error; err != nil {
			return err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	return tx.Create(&ChannelCostRatioHistory{
		ChannelId: channelId, Ratio: ratio, EffectiveFrom: at, CreatedTime: at,
	}).Error
}

// GetChannelCostRatioAt returns the ratio effective at usageAt.
func GetChannelCostRatioAt(channelId int, usageAt int64) (float64, error) {
	if usageAt <= 0 {
		usageAt = common.GetTimestamp()
	}
	var history ChannelCostRatioHistory
	err := DB.Where("channel_id = ? AND effective_from <= ? AND (effective_to IS NULL OR effective_to > ?)",
		channelId, usageAt, usageAt).Order("effective_from DESC").First(&history).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var channel Channel
		if channelErr := DB.Select("cost_ratio").First(&channel, channelId).Error; channelErr != nil {
			return 0, channelErr
		}
		return NormalizeChannelCostRatio(channel.CostRatio), nil
	}
	if err != nil {
		return 0, err
	}
	return NormalizeChannelCostRatio(history.Ratio), nil
}

func GetChannelCostRatioHistory(channelId int) ([]ChannelCostRatioHistory, error) {
	var history []ChannelCostRatioHistory
	err := DB.Where("channel_id = ?", channelId).Order("effective_from DESC").Find(&history).Error
	return history, err
}
