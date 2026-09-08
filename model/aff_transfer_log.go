package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AffTransferLog struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId    int    `json:"user_id" gorm:"index;not null"`
	Quota     int    `json:"quota" gorm:"not null"`
	TradeNo   string `json:"-" gorm:"type:varchar(255);uniqueIndex"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

func CreateAffTransferLog(tx *gorm.DB, userId int, quota int, tradeNo string) error {
	log := &AffTransferLog{
		UserId:    userId,
		Quota:     quota,
		TradeNo:   tradeNo,
		CreatedAt: common.GetTimestamp(),
	}
	return tx.Create(log).Error
}

type AffTransferLogResponse struct {
	Items []*AffTransferLog `json:"items"`
	Total int64             `json:"total"`
}

func GetAffTransferLogs(userId int, page int, pageSize int) (*AffTransferLogResponse, error) {
	var logs []*AffTransferLog
	var total int64

	tx := DB.Where("user_id = ?", userId)

	err := tx.Model(&AffTransferLog{}).Count(&total).Error
	if err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	err = tx.Order("id desc").Limit(pageSize).Offset(offset).Find(&logs).Error
	if err != nil {
		return nil, err
	}

	return &AffTransferLogResponse{
		Items: logs,
		Total: total,
	}, nil
}
