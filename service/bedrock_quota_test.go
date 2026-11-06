package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	servicequotastypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/stretchr/testify/require"
)

func TestAddBedrockQuotaGroupsRPMAndTPMByModel(t *testing.T) {
	models := make(map[string]*BedrockModelQuota)
	addBedrockQuota(models, servicequotastypes.ServiceQuota{
		QuotaName: aws.String("Global Cross-Region Inference Requests Per Minute for Anthropic Claude Sonnet v4"),
		Value:     aws.Float64(120),
	})
	addBedrockQuota(models, servicequotastypes.ServiceQuota{
		QuotaName: aws.String("Global Cross-Region Inference Tokens Per Minute for Anthropic Claude Sonnet v4"),
		Value:     aws.Float64(24000),
	})
	quota := models["sonnet"]
	require.NotNil(t, quota)
	require.NotNil(t, quota.RPM)
	require.NotNil(t, quota.TPM)
	require.Equal(t, int64(120), *quota.RPM)
	require.Equal(t, int64(24000), *quota.TPM)
}

func TestGetBedrockModelQuotasRequiresProxyAndAwsCredentials(t *testing.T) {
	channel := &model.Channel{Type: 33, Key: "ak|sk|us-east-1"}
	_, err := GetBedrockModelQuotas(t.Context(), channel)
	require.EqualError(t, err, "please fill in the channel proxy before querying AWS quotas")
}
