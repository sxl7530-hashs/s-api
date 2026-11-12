package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/bedrockmodel"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	servicequotastypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
)

type BedrockModelQuota struct {
	Model      string   `json:"model"`
	RPM        *int64   `json:"rpm,omitempty"`
	TPM        *int64   `json:"tpm,omitempty"`
	Target     string   `json:"target,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	RPMCode    string   `json:"rpm_code,omitempty"`
	TPMCode    string   `json:"tpm_code,omitempty"`
	QuotaNames []string `json:"quota_names,omitempty"`
	Status     string   `json:"status,omitempty"`
}

type BedrockQuotaResult struct {
	Region    string              `json:"region"`
	QueriedAt int64               `json:"queried_at"`
	Models    []BedrockModelQuota `json:"models"`
}

var bedrockQuotaVersionPattern = regexp.MustCompile(`\bv([0-9]+)\b`)
var bedrockQuotaRevisionPattern = regexp.MustCompile(`-v[0-9]+(?::[0-9]+)?$`)
var bedrockQuotaDatePattern = regexp.MustCompile(`\b[0-9]{8}\b`)
var bedrockQuotaModelCleanupPattern = regexp.MustCompile(`[^a-z0-9 ]+`)

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
	var mapping map[string]string
	if raw := channel.GetModelMapping(); raw != "" && common.UnmarshalJsonStr(raw, &mapping) != nil {
		return nil, fmt.Errorf("invalid model mapping")
	}
	for name := range strings.SplitSeq(channel.Models, ",") {
		if _, err := bedrockmodel.ResolveMapping(strings.TrimSpace(name), mapping); err != nil {
			return nil, err
		}
	}
	keys := channel.GetKeys()
	if len(keys) != 1 {
		return nil, fmt.Errorf("automatic AWS quota import requires a single credential; configure multi-key channel limits manually")
	}
	parts := strings.Split(keys[0], "|")
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return nil, fmt.Errorf("AWS quota query requires AccessKey|SecretKey|Region credentials")
	}
	region := strings.TrimSpace(parts[2])
	httpClient, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, fmt.Errorf("invalid channel proxy configuration")
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
	if channel.Models != "" {
		selected := make([]BedrockModelQuota, 0)
		for name := range strings.SplitSeq(channel.Models, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			mapped, mapErr := bedrockmodel.ResolveMapping(name, mapping)
			if mapErr != nil {
				return nil, mapErr
			}
			target := bedrockmodel.FinalID(mapped, region)
			item := BedrockModelQuota{Model: name, Target: target, Status: "unmatched"}
			scope, base := bedrockQuotaTarget(target)
			identity := normalizeBedrockQuotaModel(base)
			for _, candidate := range models {
				if candidate.Scope == scope && candidate.Model == identity {
					if candidate.Status == "ambiguous" {
						item.Status = "ambiguous"
						break
					}
					item = candidate
					item.Model = name
					item.Target = target
					item.Status = "matched"
					if item.RPM != nil && *item.RPM == 0 || item.TPM != nil && *item.TPM == 0 {
						item.Status = "zero_quota"
					}
				}
			}
			selected = append(selected, item)
		}
		models = selected
	}
	return &BedrockQuotaResult{Region: region, Models: models, QueriedAt: time.Now().Unix()}, nil
}

func addBedrockQuota(models map[string]*BedrockModelQuota, quota servicequotastypes.ServiceQuota) {
	if quota.QuotaName == nil || quota.Value == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(aws.ToString(quota.QuotaName)))

	scope := "regional"
	switch {
	case strings.HasPrefix(name, "global cross-region"):
		scope = "global"
	case strings.Contains(name, "cross-region"):
		scope = "cross-region"
	case strings.Contains(name, "on-demand"):
		scope = "regional"
	default:
		return
	}
	if !strings.Contains(name, "anthropic") && !strings.Contains(name, "claude") {
		return
	}
	metric := ""
	maximum := int64(0)
	if strings.Contains(name, "tokens per minute") {
		metric = "tpm"
		maximum = 1_000_000_000
	}
	if strings.Contains(name, "requests per minute") {
		metric = "rpm"
		maximum = 10_000_000
	}
	value := *quota.Value
	if metric == "" || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.Trunc(value) != value || value > float64(maximum) {
		return
	}
	_, modelName, ok := strings.Cut(name, " for ")
	if !ok {
		return
	}
	modelName = normalizeBedrockQuotaModel(modelName)
	if modelName == "" {
		return
	}
	key := scope + ":" + modelName
	entry := models[key]
	if entry == nil {
		entry = &BedrockModelQuota{Model: modelName, Scope: scope}
		models[key] = entry
	}
	number := int64(value)
	code := aws.ToString(quota.QuotaCode)
	if metric == "tpm" {
		if entry.TPM != nil && (*entry.TPM != number || entry.TPMCode != code) {
			entry.Status = "ambiguous"
		}
		entry.TPM = &number
		entry.TPMCode = code
	} else {
		if entry.RPM != nil && (*entry.RPM != number || entry.RPMCode != code) {
			entry.Status = "ambiguous"
		}
		entry.RPM = &number
		entry.RPMCode = code
	}
	entry.QuotaNames = append(entry.QuotaNames, aws.ToString(quota.QuotaName))
}

func bedrockQuotaTarget(target string) (string, string) {
	for _, prefix := range []string{"global.", "us.", "eu.", "apac."} {
		if base, ok := strings.CutPrefix(target, prefix); ok {
			scope := "cross-region"
			if prefix == "global." {
				scope = "global"
			}
			return scope, strings.TrimPrefix(base, "anthropic.")
		}
	}
	return "regional", strings.TrimPrefix(target, "anthropic.")
}

func normalizeBedrockQuotaModel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = bedrockQuotaRevisionPattern.ReplaceAllStringFunc(value, func(revision string) string {
		version, _, _ := strings.Cut(strings.TrimPrefix(revision, "-v"), ":")
		if version == "1" {
			return ""
		}
		return " " + version
	})
	value = strings.ReplaceAll(value, "anthropic", "")
	value = strings.ReplaceAll(value, "claude", "")
	value = bedrockQuotaVersionPattern.ReplaceAllString(value, "$1")
	value = bedrockQuotaModelCleanupPattern.ReplaceAllString(value, " ")
	value = bedrockQuotaDatePattern.ReplaceAllString(value, "")
	fields := strings.Fields(value)
	family := ""
	rest := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "sonnet" || field == "opus" || field == "haiku" {
			family = field
		} else {
			rest = append(rest, field)
		}
	}
	if family == "" {
		return ""
	}
	return family + " " + strings.Join(rest, " ")
}
