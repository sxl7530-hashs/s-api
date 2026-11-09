package aws

import (
	"strings"

	"github.com/QuantumNous/new-api/pkg/bedrockmodel"
)

var awsModelIDMap = bedrockmodel.IDs
var awsModelCanCrossRegionMap = bedrockmodel.CrossRegion
var awsRegionCrossModelPrefixMap = bedrockmodel.RegionPrefixes

var ChannelName = "aws"

// 判断是否为Nova模型
func isNovaModel(modelId string) bool {
	return strings.Contains(modelId, "nova-")
}
