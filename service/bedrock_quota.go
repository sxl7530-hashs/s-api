package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	servicequotastypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
)

type BedrockModelQuota struct {
	Model string `json:"model"`
	RPM   *int64 `json:"rpm,omitempty"`
	TPM   *int64 `json:"tpm,omitempty"`
}

type BedrockQuotaResult struct {
	Region string              `json:"region"`
	Models []BedrockModelQuota `json:"models"`
}

var bedrockQuotaVersionPattern = regexp.MustCompile(`\bv\d+\b`)
var bedrockQuotaModelCleanupPattern = regexp.MustCompile(`[^a-z0-9. ]+`)

// GetBedrockModelQuotas reads account-level Bedrock quotas through the
// channel's configured proxy. It deliberately requires a proxy because this
// endpoint is an administrator-triggered control-plane query, not a relay
// request path.
func GetBedrockModelQuotas(ctx context.Context, channel *model.Channel) (*BedrockQuotaResult, error) {
	if channel == nil || channel.Type != constant.ChannelTypeAws {
		return nil, fmt.Errorf("channel is not an AWS Bedrock channel")
	}
	settings := channel.GetSetting()
	if strings.TrimSpace(settings.Proxy) == "" {
		return nil, fmt.Errorf("please fill in the channel proxy before querying AWS quotas")
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return nil, fmt.Errorf("AWS channel credentials are missing")
	}
	parts := strings.Split(keys[0], "|")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return nil, fmt.Errorf("AWS quota query requires AccessKey|SecretKey|Region credentials")
	}
	region := strings.TrimSpace(parts[2])
	httpClient, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, fmt.Errorf("create proxy client: %w", err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	config := aws.Config{
		Region:      region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(parts[0], parts[1], "")),
		HTTPClient:  httpClient,
	}
	client := servicequotas.NewFromConfig(config)
	paginator := servicequotas.NewListServiceQuotasPaginator(client, &servicequotas.ListServiceQuotasInput{
		ServiceCode: aws.String("bedrock"),
	})
	quotas := make(map[string]*BedrockModelQuota)
	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(queryCtx)
		if pageErr != nil {
			return nil, fmt.Errorf("query AWS Service Quotas: %w", pageErr)
		}
		for _, quota := range page.Quotas {
			addBedrockQuota(quotas, quota)
		}
	}
	models := make([]BedrockModelQuota, 0, len(quotas))
	for _, quota := range quotas {
		models = append(models, *quota)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Model < models[j].Model })
	return &BedrockQuotaResult{Region: region, Models: models}, nil
}

func addBedrockQuota(models map[string]*BedrockModelQuota, quota servicequotastypes.ServiceQuota) {
	if quota.QuotaName == nil || quota.Value == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(aws.ToString(quota.QuotaName)))
	if !strings.HasPrefix(name, "global cross-region") || (!strings.Contains(name, "anthropic") && !strings.Contains(name, "claude")) {
		return
	}
	parts := strings.SplitN(name, " for ", 2)
	if len(parts) != 2 {
		return
	}
	modelName := normalizeBedrockQuotaModel(parts[1])
	if modelName == "" {
		return
	}
	entry := models[modelName]
	if entry == nil {
		entry = &BedrockModelQuota{Model: modelName}
		models[modelName] = entry
	}
	value := int64(*quota.Value)
	if strings.Contains(name, "tokens per minute") {
		entry.TPM = &value
	}
	if strings.Contains(name, "requests per minute") {
		entry.RPM = &value
	}
}

func normalizeBedrockQuotaModel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "anthropic", "")
	value = strings.ReplaceAll(value, "claude", "")
	value = bedrockQuotaVersionPattern.ReplaceAllString(value, "")
	value = bedrockQuotaModelCleanupPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}
