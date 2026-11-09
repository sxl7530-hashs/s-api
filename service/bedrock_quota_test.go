package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/bedrockmodel"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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
	quota := models["global:sonnet 4"]
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

func TestBedrockQuotaIdentityKeepsVersionAndRoutingScope(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Anthropic Claude Sonnet v4.5", "sonnet 4 5"},
		{"anthropic.claude-sonnet-4-5-20250929-v1:0", "sonnet 4 5"},
		{"Anthropic Claude Sonnet 5.4", "sonnet 5 4"},
		{"Claude 3.5 Sonnet V2", "sonnet 3 5 2"},
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", "sonnet 3 5 2"},
	} {
		assert.Equal(t, tc.want, normalizeBedrockQuotaModel(tc.input))
	}
	for _, tc := range []struct{ input, scope string }{
		{"global.anthropic.claude-sonnet-4-6", "global"},
		{"us.anthropic.claude-sonnet-4-6", "cross-region"},
		{"anthropic.claude-sonnet-4-6", "regional"},
	} {
		scope, _ := bedrockQuotaTarget(tc.input)
		assert.Equal(t, tc.scope, scope)
	}
	models := map[string]*BedrockModelQuota{}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1, 1.5, 1e20} {
		addBedrockQuota(models, servicequotastypes.ServiceQuota{QuotaName: aws.String("Global Cross-Region Inference Requests Per Minute for Anthropic Claude Sonnet 4.6"), Value: aws.Float64(v)})
	}
	assert.Empty(t, models)
	for _, code := range []string{"L-A", "L-B"} {
		addBedrockQuota(models, servicequotastypes.ServiceQuota{QuotaCode: aws.String(code), QuotaName: aws.String("Global Cross-Region Inference Requests Per Minute for Anthropic Claude Sonnet 4.6"), Value: aws.Float64(120)})
	}
	assert.Equal(t, "ambiguous", models["global:sonnet 4 6"].Status)
}

func TestBedrockSelectedFriendlyModelsEnforceMappedCeilings(t *testing.T) {
	useBedrockProtectionRedis(t)
	target := "global.anthropic.claude-sonnet-4-6"
	mapping := map[string]string{"friendly": "intermediate", "intermediate": target}
	settings := &dto.BedrockProtectionSettings{ModelRateLimitEnabled: true, ModelLimits: map[string]dto.BedrockModelLimit{"friendly": {RPM: 1, TPM: 100, ResolvedModel: target, QuotaRegion: "us-east-1"}}}
	resolved, err := ResolveBedrockModelLimits(settings, mapping, "us-east-1")
	require.NoError(t, err)
	assert.Equal(t, 100, resolved.ModelLimits[target].TPM)
	assert.Contains(t, settings.ModelLimits, "friendly", "resolving must not mutate saved configuration")
	allowed, _ := ReserveBedrockUsage(4979, 80, resolved, target)
	require.True(t, allowed)
	allowed, _ = ReserveBedrockUsage(4979, 1, resolved, target)
	assert.False(t, allowed, "friendly model must not bypass mapped model RPM")
	settings.ModelLimits[target] = dto.BedrockModelLimit{RPM: 2}
	_, err = ResolveBedrockModelLimits(settings, mapping, "us-east-1")
	require.ErrorContains(t, err, "conflicting")
	delete(settings.ModelLimits, target)
	_, err = ResolveBedrockModelLimits(settings, mapping, "eu-west-1")
	require.ErrorContains(t, err, "region changed")
	_, err = ResolveBedrockModelLimits(settings, map[string]string{"friendly": "other"}, "us-east-1")
	require.ErrorContains(t, err, "target changed")
	settings.ModelRateLimitEnabled = false
	bypass, err := ResolveBedrockModelLimits(settings, map[string]string{"friendly": "friendly"}, "eu-west-1")
	require.NoError(t, err)
	assert.Same(t, settings, bypass)
	_, err = bedrockmodel.ResolveMapping("a", map[string]string{"a": "b", "b": "a"})
	require.Error(t, err)
	assert.Equal(t, "us.anthropic.claude-opus-4-6-v1", bedrockmodel.FinalID("claude-opus-4-6", "us-east-1"))
	assert.Equal(t, target, bedrockmodel.FinalID(target, "us-east-1"))
}

func TestBedrockQuotaRejectsMultiKeyImportBeforeNetwork(t *testing.T) {
	raw, err := common.Marshal(dto.ChannelSettings{Proxy: "http://127.0.0.1:1"})
	require.NoError(t, err)
	setting := string(raw)
	channel := &model.Channel{Type: 33, Key: "ak|sk|us-east-1\nak2|sk2|us-east-1", Setting: &setting}
	_, err = GetBedrockModelQuotas(t.Context(), channel)
	require.ErrorContains(t, err, "single credential")
}

func TestBedrockQuotaQueryUsesProxyAndDraftModelMapping(t *testing.T) {
	var upstreamCalls, proxyCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		assert.Equal(t, "servicequotas.us-east-1.amazonaws.com", r.Host)
		assert.Contains(t, r.Header.Get("Authorization"), "/us-east-1/servicequotas/aws4_request")
		assert.Contains(t, r.Header.Get("X-Amz-Target"), "ListServiceQuotas")
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = io.WriteString(w, `{"Quotas":[{"QuotaName":"Global Cross-Region Inference Requests Per Minute for Anthropic Claude Sonnet 4.6","QuotaCode":"L-RPM","Value":120},{"QuotaName":"Global Cross-Region Inference Tokens Per Minute for Anthropic Claude Sonnet 4.6","QuotaCode":"L-TPM","Value":24000}]}`)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		assert.Equal(t, http.MethodConnect, r.Method)
		assert.Equal(t, "servicequotas.us-east-1.amazonaws.com:443", r.Host)
		upstreamConn, err := net.Dial("tcp", strings.TrimPrefix(upstream.URL, "https://"))
		if !assert.NoError(t, err) {
			w.WriteHeader(502)
			return
		}
		downstream, buffer, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			_ = upstreamConn.Close()
			return
		}
		defer downstream.Close()
		defer upstreamConn.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffer.Flush()
		go func() { _, _ = io.Copy(upstreamConn, buffer); _ = upstreamConn.Close() }()
		_, _ = io.Copy(downstream, upstreamConn)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}}
	defer transport.CloseIdleConnections()
	settings := dto.ChannelSettings{Proxy: proxy.URL}
	cacheKey := clientCacheKey(proxy.URL, NormalizeHTTPTransportPolicy(settings))
	proxyClients.store(cacheKey, &http.Client{Transport: transport})
	t.Cleanup(func() { proxyClients.mutex.Lock(); delete(proxyClients.clients, cacheKey); proxyClients.mutex.Unlock() })
	channel := &model.Channel{Type: 33, Key: "test-ak|test-sk|us-east-1", Models: "friendly,unknown"}
	channel.SetSetting(settings)
	mapping := `{"friendly":"alias","alias":"global.anthropic.claude-sonnet-4-6"}`
	channel.ModelMapping = &mapping
	result, err := GetBedrockModelQuotas(t.Context(), channel)
	require.NoError(t, err)
	require.Len(t, result.Models, 2)
	assert.Equal(t, "friendly", result.Models[0].Model)
	assert.Equal(t, "global.anthropic.claude-sonnet-4-6", result.Models[0].Target)
	assert.Equal(t, "matched", result.Models[0].Status)
	require.NotNil(t, result.Models[0].RPM)
	assert.EqualValues(t, 120, *result.Models[0].RPM)
	require.NotNil(t, result.Models[0].TPM)
	assert.EqualValues(t, 24000, *result.Models[0].TPM)
	assert.Equal(t, "unmatched", result.Models[1].Status)
	assert.Greater(t, proxyCalls.Load(), int32(0))
	assert.EqualValues(t, 1, upstreamCalls.Load())
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = GetBedrockModelQuotas(canceled, channel)
	require.Error(t, err)
	assert.EqualValues(t, 1, upstreamCalls.Load(), "cancellation must not send another query")
}
