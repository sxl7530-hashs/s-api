package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	InvoiceStatusPending  = 1
	InvoiceStatusApproved = 2
	InvoiceStatusRejected = 3
)

type InvoiceRequest struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId      int    `json:"user_id" gorm:"index;not null"`
	Username    string `json:"username" gorm:"type:varchar(64)"`
	CompanyName string `json:"company_name" gorm:"type:varchar(255);not null"`
	TaxId       string `json:"tax_id" gorm:"type:varchar(64)"`
	Amount      string `json:"amount" gorm:"type:varchar(32);not null"`
	Email       string `json:"email" gorm:"type:varchar(128);not null"`
	Remark      string `json:"remark" gorm:"type:text"`
	Status      int    `json:"status" gorm:"type:int;default:1;index"`
	AdminRemark string `json:"admin_remark" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64  `json:"updated_at" gorm:"bigint"`
}

func (InvoiceRequest) TableName() string {
	return "invoice_requests"
}

func CreateInvoiceRequest(req *InvoiceRequest) error {
	req.Status = InvoiceStatusPending
	req.CreatedAt = time.Now().Unix()
	req.UpdatedAt = req.CreatedAt
	return DB.Create(req).Error
}

func GetUserInvoiceRequests(userId int, pageInfo *common.PageInfo) ([]*InvoiceRequest, int64, error) {
	var requests []*InvoiceRequest
	var total int64

	tx := DB.Model(&InvoiceRequest{}).Where("user_id = ?", userId)
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&requests).Error; err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}

func GetAllInvoiceRequests(pageInfo *common.PageInfo, status int) ([]*InvoiceRequest, int64, error) {
	var requests []*InvoiceRequest
	var total int64

	tx := DB.Model(&InvoiceRequest{})
	if status > 0 {
		tx = tx.Where("status = ?", status)
	}
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&requests).Error; err != nil {
		return nil, 0, err
	}
	return requests, total, nil
}

func UpdateInvoiceRequestStatus(id int, status int, adminRemark string) error {
	return DB.Model(&InvoiceRequest{}).Where("id = ?", id).Updates(map[string]any{
		"status":       status,
		"admin_remark": adminRemark,
		"updated_at":   time.Now().Unix(),
	}).Error
}
