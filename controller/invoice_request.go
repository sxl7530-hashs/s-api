package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type createInvoiceRequestInput struct {
	CompanyName string `json:"company_name" binding:"required"`
	TaxId       string `json:"tax_id"`
	Amount      string `json:"amount" binding:"required"`
	Email       string `json:"email" binding:"required"`
	Remark      string `json:"remark"`
}

func CreateInvoiceRequest(c *gin.Context) {
	var input createInvoiceRequestInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiErrorMsg(c, "参数错误: "+err.Error())
		return
	}

	userId := c.GetInt("id")
	username := c.GetString("username")

	req := &model.InvoiceRequest{
		UserId:      userId,
		Username:    username,
		CompanyName: input.CompanyName,
		TaxId:       input.TaxId,
		Amount:      input.Amount,
		Email:       input.Email,
		Remark:      input.Remark,
	}

	if err := model.CreateInvoiceRequest(req); err != nil {
		common.ApiErrorMsg(c, "提交失败: "+err.Error())
		return
	}

	common.ApiSuccess(c, req)
}

func GetUserInvoiceRequests(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)

	requests, total, err := model.GetUserInvoiceRequests(userId, pageInfo)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	common.ApiSuccess(c, gin.H{
		"items": requests,
		"total": total,
	})
}

func GetAllInvoiceRequests(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	status, _ := strconv.Atoi(c.Query("status"))

	requests, total, err := model.GetAllInvoiceRequests(pageInfo, status)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	common.ApiSuccess(c, gin.H{
		"items": requests,
		"total": total,
	})
}

type updateInvoiceStatusInput struct {
	Status      int    `json:"status" binding:"required"`
	AdminRemark string `json:"admin_remark"`
}

func UpdateInvoiceRequestStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}

	var input updateInvoiceStatusInput
	if err := c.ShouldBindJSON(&input); err != nil {
		common.ApiErrorMsg(c, "参数错误: "+err.Error())
		return
	}

	if input.Status != model.InvoiceStatusApproved && input.Status != model.InvoiceStatusRejected {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}

	if err := model.UpdateInvoiceRequestStatus(id, input.Status, input.AdminRemark); err != nil {
		common.ApiErrorMsg(c, "更新失败: "+err.Error())
		return
	}

	common.ApiSuccess(c, nil)
}
