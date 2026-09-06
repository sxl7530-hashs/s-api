package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetChannelCostRatioHistory(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "渠道 ID 无效")
		return
	}
	history, err := model.GetChannelCostRatioHistory(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, history)
}
