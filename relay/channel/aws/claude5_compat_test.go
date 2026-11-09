package aws

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestClaude5CompatAppliedToBothAwsOperations(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	const input = `{"max_tokens":128,"temperature":0,"messages":[{"role":"user","content":"hello"}]}`
	for _, stream := range []bool{false, true} {
		for _, compatibility := range []bool{false, true} {
			for _, include := range []bool{false, true} {
				t.Run(fmt.Sprintf("stream=%v/compat=%v/include=%v", stream, compatibility, include), func(t *testing.T) {
					settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"*"}, DropTemperature: true, ApplyToPassthroughUsers: include}
					c := newAwsTestContext(httptest.NewRecorder(), t.Context())
					info := newAwsTestRelayInfo()
					info.ChannelMeta = &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAws}
					info.ApiKey = "test-access|test-secret|us-east-1"
					info.IsStream = stream
					info.AwsPassthroughCompatibility = compatibility
					info.UserSetting.AwsDirectPassthrough = true
					a := &Adaptor{}
					_, err := doAwsClientRequest(c, info, a, strings.NewReader(input))
					require.NoError(t, err)
					var body []byte
					switch request := a.AwsReq.(type) {
					case *bedrockruntime.InvokeModelInput:
						require.False(t, stream)
						body = request.Body
					case *bedrockruntime.InvokeModelWithResponseStreamInput:
						require.True(t, stream)
						body = request.Body
					default:
						t.Fatalf("unexpected operation %T", a.AwsReq)
					}
					var payload map[string]any
					require.NoError(t, common.Unmarshal(body, &payload))
					if include {
						require.NotContains(t, payload, "temperature")
					} else {
						require.Contains(t, payload, "temperature")
					}
				})
			}
		}
	}
}

func TestApplyClaude5CompatRemovesAttributionAndAddsBinding(t *testing.T) {
	useClaude5Redis(t)
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{
		Enabled:                true,
		TargetModels:           []string{"anthropic.claude-opus-5"},
		AttributionMode:        "remove",
		ThinkingBindingRecover: true,
		BetaCapabilities:       []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-opus-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}},
	}
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.1; cc_entrypoint=sdk-ts;"}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x"}]}]}`)
	result, err := applyClaude5Compat(nil, nil, "anthropic.claude-opus-5", "us-east-1", body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(result, &decoded))
	require.NotContains(t, decoded, "system")
	require.Contains(t, string(result), "thinking-binding-controls-2026-08-01")
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	result, err = applyClaude5Compat(c, nil, "anthropic.claude-opus-5", "us-east-1", body)
	require.NoError(t, err)
	require.True(t, c.GetBool("claude5_compat_beta_injected"))
	_, err = applyClaude5Compat(c, nil, "anthropic.claude-opus-5", "us-east-1", result)
	require.NoError(t, err)
	require.False(t, c.GetBool("claude5_compat_beta_injected"), "a client-supplied beta must not be reported as gateway injection")
}

func TestApplyClaude5CompatDisabledPreservesBody(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{}
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.1;"}]}`)
	result, err := applyClaude5Compat(nil, nil, "anthropic.claude-opus-5", "us-east-1", body)
	require.NoError(t, err)
	require.Equal(t, string(body), string(result))
}

func TestClaude5ThinkingMappingPreservesRecovery(t *testing.T) {
	useClaude5Redis(t)
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	for _, kind := range []string{"enabled", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, ThinkingDisabledMap: true, OutputConfigPolicy: "keep_effort_only", BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
			body := []byte(`{"thinking":{"type":"` + kind + `","budget_tokens":1024},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"signature"},{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"result"}]}]}`)
			result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(result, &decoded))
			thinking := decoded["thinking"].(map[string]any)
			require.Equal(t, map[string]any{"prefix_mismatch_behavior": "drop_block"}, thinking["block_binding"])
			var original map[string]any
			require.NoError(t, common.Unmarshal(body, &original))
			require.Equal(t, original["messages"], decoded["messages"])
		})
	}
}

func TestClaude5CompatibilityScopeAndIndependentRules(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	body := []byte(`{ "anthropic_version":"bedrock-2023-05-31", "temperature":0,"unknown":{"number":9007199254740993},"messages":[{"role":"user","content":"keep this string"}] }`)
	for _, tc := range []struct {
		name                     string
		channel                  int
		model, region            string
		direct, include, changed bool
	}{
		{"AWS target", constant.ChannelTypeAws, "target", "us-east-1", false, false, true},
		{"other model", constant.ChannelTypeAws, "other", "us-east-1", false, false, false},
		{"other region", constant.ChannelTypeAws, "target", "us-west-2", false, false, false},
		{"direct excluded", constant.ChannelTypeAws, "target", "us-east-1", true, false, false},
		{"direct included", constant.ChannelTypeAws, "target", "us-east-1", true, true, true},
		{"Anthropic excluded", constant.ChannelTypeAnthropic, "target", "us-east-1", false, true, false},
		{"OpenAI excluded", constant.ChannelTypeOpenAI, "target", "us-east-1", false, true, false},
		{"Gemini excluded", constant.ChannelTypeGemini, "target", "us-east-1", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, TargetRegions: []string{"us-east-1"}, DropTemperature: true, StripContainerFields: true, ApplyToPassthroughUsers: tc.include}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tc.channel}}
			info.UserSetting.AwsDirectPassthrough = tc.direct
			result, err := applyClaude5Compat(nil, info, tc.model, tc.region, body)
			require.NoError(t, err)
			if !tc.changed {
				require.Equal(t, body, result)
				return
			}
			require.NotContains(t, string(result), `"temperature"`)
			require.Contains(t, string(result), `9007199254740993`)
			require.Contains(t, string(result), `"content":"keep this string"`)
			require.Contains(t, string(result), `"anthropic_version":"bedrock-2023-05-31"`)
		})
	}
}

func TestClaude5FieldRulesDoNotAlterUnselectedFields(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	for _, tc := range []struct {
		name, body, want string
		configure        func(*model_setting.Claude5CompatSettings)
	}{
		{"disabled thinking", `{"thinking":{"type":"disabled","budget_tokens":1000}}`, `{"thinking":{"type":"between_tools"}}`, func(s *model_setting.Claude5CompatSettings) { s.ThinkingDisabledMap = true }},
		{"enabled and display", `{"thinking":{"type":"enabled","budget_tokens":1000,"adaptive":{"display":"bad"}}}`, `{"thinking":{"type":"adaptive","adaptive":{"display":"summarized"}}}`, func(s *model_setting.Claude5CompatSettings) {
			s.ThinkingEnabledMap = true
			s.ThinkingDisplayNormalize = true
			s.OutputConfigPolicy = "keep_effort_only"
		}},
		{"budget effort", `{"thinking":{"type":"enabled","budget_tokens":1000},"output_config":{"format":{"type":"json"}}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, func(s *model_setting.Claude5CompatSettings) {
			s.ThinkingEnabledMap = true
			s.OutputConfigPolicy = "keep_effort_only"
			s.ThinkingBudgetEffortTable = []model_setting.ThinkingBudgetEffort{{MaxBudget: 2000, Effort: "low"}}
		}},
		{"context only", `{"context_management":{},"unknown":false}`, `{"unknown":false}`, func(s *model_setting.Claude5CompatSettings) { s.StripContextManagement = true }},
		{"container only", `{"messages":[{"role":"assistant","content":[{"type":"text","text":"keep","container":{},"unknown":0}]}]}`, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"keep","unknown":0}]}]}`, func(s *model_setting.Claude5CompatSettings) { s.StripContainerFields = true }},
		{"tool choice opt in", `{"tool_choice":{"type":"tool","name":"lookup"},"tools":[{"name":"lookup"}]}`, `{"tool_choice":{"type":"auto"},"tools":[{"name":"lookup"}]}`, func(s *model_setting.Claude5CompatSettings) { s.MapToolChoiceAuto = true }},
		{"empty system", `{"system":[],"messages":[]}`, `{"messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"string system rolls back", `{"system":"","messages":[]}`, `{"system":"","messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"blank system cleaning disabled", `{"system":" \n\t ","messages":[]}`, `{"system":" \n\t ","messages":[]}`, func(s *model_setting.Claude5CompatSettings) {}},
		{"display absent", `{"thinking":{"type":"adaptive"}}`, `{"thinking":{"type":"adaptive"}}`, func(s *model_setting.Claude5CompatSettings) { s.ThinkingDisplayNormalize = true }},
		{"strip output without conversion", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, `{"thinking":{"type":"adaptive"}}`, func(s *model_setting.Claude5CompatSettings) { s.OutputConfigPolicy = "strip_all" }},
		{"preserve newer search", `{"tools":[{"type":"web_search_20250305"},{"type":"web_search_20990101"}]}`, `{"tools":[{"type":"web_search_20990101"}]}`, func(s *model_setting.Claude5CompatSettings) { s.StripUnsupportedWebSearch = true }},
		{"move cache", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","content":[{"type":"text","text":"ok","cache_control":{"type":"ephemeral"}}]}]}]}`, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"ok"}]}]}]}`, func(s *model_setting.Claude5CompatSettings) { s.FixToolResultCacheControl = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}}
			tc.configure(&s)
			settings.Claude5Compat = s
			result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", []byte(tc.body))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(result))
		})
	}
}

func TestClaude5ForgePreservesFingerprintAndToolConversation(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, AttributionMode: "forge", ForgeVersion: "2.1.280", ThinkingBindingRecover: true}
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.0db; cc_entrypoint=sdk-ts;"},{"type":"text","text":"actual instructions"}],"anthropic_beta":["existing-beta"],"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]}]}`)
	result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.Contains(t, string(result), "cc_version=2.1.280.0db; cc_entrypoint=sdk-ts;")
	require.Contains(t, string(result), "existing-beta")
	require.Contains(t, string(result), "actual instructions")
	again, err := applyClaude5Compat(nil, nil, "target", "us-east-1", result)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(again))
}

func TestClaude5RulesPreserveUntargetedSignedContent(t *testing.T) {
	// A field-cleaning switch must not rewrite thinking data as a side effect.
	for _, settings := range []model_setting.Claude5CompatSettings{
		{DropTemperature: true},
		{ThinkingDisabledMap: true},
		{ThinkingEnabledMap: true},
		{ThinkingDisplayNormalize: true},
	} {
		thinking := claude5JSON(`{ "type": "adaptive", "signature": "opaque", "adaptive": { "display": "omitted" } }`)
		request := map[string]claude5JSON{"thinking": thinking}
		var changed []string
		applyClaude5ThinkingMapping(request, settings, &changed)
		require.Equal(t, thinking, request["thinking"])
		require.Empty(t, changed)
	}
	request := map[string]claude5JSON{"system": claude5JSON(`[{ "type": "text", "text": "keep prefix" }]`)}
	original := string(request["system"])
	var changed []string
	cleanClaude5System(request, &changed)
	require.Equal(t, original, string(request["system"]))
	require.Empty(t, changed)
}

func TestClaude5RecoveryRejectsMalformedControls(t *testing.T) {
	useClaude5Redis(t)
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true}
	for _, control := range []string{`"thinking":"bad"`, `"thinking":{"block_binding":[]}`, `"thinking":{"block_binding":{"prefix_mismatch_behavior":null}}`, `"anthropic_beta":"bad"`} {
		body := []byte(`{` + control + `,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`)
		result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
		require.NoError(t, err, control)
		require.Equal(t, body, result, control)
	}
	settings.Claude5Compat.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}
	settings.Claude5Compat.BlockBindingWrite = true
	body := []byte(`{"temperature":1,"thinking":{"block_binding":{"prefix_mismatch_behavior":null}},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`)
	settings.Claude5Compat.DropTemperature = true
	result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.Equal(t, body, result)
}

func TestClaude5ConflictingCachePoliciesRemainUntouched(t *testing.T) {
	raw := `{"type":"tool_result","tool_use_id":"tool-1","cache_control":{"type":"ephemeral","ttl":"1h"},"content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`
	var block map[string]claude5JSON
	require.NoError(t, common.Unmarshal([]byte(raw), &block))
	moved, reason := moveToolResultCacheControl(block)
	require.False(t, moved)
	require.Equal(t, "cache_control_conflict", reason)
	encoded, err := common.Marshal(block)
	require.NoError(t, err)
	require.JSONEq(t, raw, string(encoded))
}

func TestClaude5DiagnosticsDoNotLeakAcrossAwsRetries(t *testing.T) {
	for _, model := range []string{"amazon.nova-lite-v1:0", awsTestModel} {
		t.Run(model, func(t *testing.T) {
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			c.Set(claude5CompatContextKey, []string{"attribution_removed"})
			info := newAwsTestRelayInfo()
			info.ApiKey = "test-access|test-secret|us-east-1"
			info.UpstreamModelName = model
			info.IsStream = false
			info.AwsPassthroughCompatibility = true
			body := `{"messages":[]}`
			if isNovaModel(model) {
				body = `{"schemaVersion":"messages-v1","messages":[]}`
			}
			_, err := doAwsClientRequest(c, info, &Adaptor{}, strings.NewReader(body))
			require.NoError(t, err)
			require.Empty(t, c.GetStringSlice(claude5CompatContextKey))
		})
	}
}

// This verifies the gateway's multi-turn contract, not Bedrock's validation of
// opaque signatures. Only a real AWS conversation can prove drop_block recovery.
func TestClaude5MultiTurnPrefixAndToolHistoryRemainStable(t *testing.T) {
	useClaude5Redis(t)
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	for _, mode := range []string{"remove", "forge"} {
		t.Run(mode, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{
				Enabled: true, TargetModels: []string{"target"}, AttributionMode: mode,
				ForgeVersion: "2.1.280", ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, OutputConfigPolicy: "keep_effort_only",
				BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}},
			}
			prefix := `[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.0db; cc_entrypoint=sdk-ts;"},{"type":"text","text":"Stable instructions","cache_control":{"type":"ephemeral","ttl":"1h"}}]`
			history := `[{"role":"user","content":"Read the record"},{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque-previous-prefix"},{"type":"tool_use","id":"tool-1","name":"read","input":{"record":9007199254740993}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"record result"}]}]`
			messages := history
			var stableSystem claude5JSON
			for _, round := range []string{"N", "N+1"} {
				if round == "N+1" {
					messages = strings.TrimSuffix(history, "]") + `,{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque-next-round"},{"type":"tool_use","id":"tool-2","name":"verify","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-2","content":"verified"}]}]`
				}
				body := []byte(`{"anthropic_version":"bedrock-2023-05-31","anthropic_beta":["existing-beta"],"max_tokens":1024,"temperature":0,"thinking":{"type":"enabled","budget_tokens":512},"system":` + prefix + `,"messages":` + messages + `}`)
				result, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
				require.NoError(t, err)
				var payload map[string]claude5JSON
				require.NoError(t, common.Unmarshal(result, &payload))
				require.Equal(t, messages, string(payload["messages"]), "signed history and tool pairings must remain byte-identical")
				require.Equal(t, `"bedrock-2023-05-31"`, string(payload["anthropic_version"]))
				require.Equal(t, "0", string(payload["temperature"]))
				if round == "N" {
					stableSystem = payload["system"]
				} else {
					require.Equal(t, stableSystem, payload["system"], "system cache prefix must remain stable between rounds")
				}
				require.Contains(t, string(payload["system"]), `"ttl":"1h"`)
				var thinking map[string]any
				require.NoError(t, common.Unmarshal(payload["thinking"], &thinking))
				require.Equal(t, "adaptive", thinking["type"])
				require.Equal(t, map[string]any{"prefix_mismatch_behavior": "drop_block"}, thinking["block_binding"])
				var betas []string
				require.NoError(t, common.Unmarshal(payload["anthropic_beta"], &betas))
				require.Equal(t, []string{"existing-beta", "thinking-binding-controls-2026-08-01"}, betas)
				repeated, err := applyClaude5Compat(nil, nil, "target", "us-east-1", result)
				require.NoError(t, err)
				require.Equal(t, result, repeated, "normalization must be idempotent")
			}
		})
	}
}

func useClaude5Redis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled, common.RDB = true, client
	claude5BetaDenyCache.Lock()
	previousCache := claude5BetaDenyCache.entries
	claude5BetaDenyCache.entries = make(map[string]claude5BetaDenyCacheEntry)
	claude5BetaDenyCache.Unlock()
	t.Cleanup(func() {
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		_ = client.Close()
		claude5BetaDenyCache.Lock()
		claude5BetaDenyCache.entries = previousCache
		claude5BetaDenyCache.Unlock()
	})
	return server
}

func TestClaude5V39AtomicBoundaries(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, tc := range []struct {
		name, body       string
		configure        func(*model_setting.Claude5CompatSettings)
		unchanged        bool
		absent, contains string
	}{
		{name: "late thinking error rolls back removed attribution", body: `{ "system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215;"}],"thinking":null,"temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) {
			s.AttributionMode = "remove"
			s.ThinkingBindingRecover = true
		}, unchanged: true},
		{name: "system string rolls back", body: `{"system":"business instructions","temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }, unchanged: true},
		{name: "missing system does not block cleaning", body: `{"temperature":1}`, absent: "temperature"},
		{name: "duplicate attribution skips only attribution", body: `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215;"},{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.318;"}],"temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) {
			s.AttributionMode = "remove"
			s.ThinkingBindingRecover = true
		}, absent: "temperature", contains: "2.1.215"},
		{name: "mixed attribution preserves business instruction", body: `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215; do not delete business text"}],"temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) {
			s.AttributionMode = "remove"
			s.ThinkingBindingRecover = true
		}, absent: "temperature", contains: "do not delete business text"},
		{name: "malformed beta untouched without pending beta", body: `{"anthropic_beta":null,"temperature":1}`, absent: "temperature", contains: `"anthropic_beta":null`},
		{name: "malformed beta with pending injection rolls back", body: `{"anthropic_beta":[null],"temperature":1,"messages":[{"content":[{"type":"redacted_thinking","data":"private"}]}]}`, configure: func(s *model_setting.Claude5CompatSettings) {
			s.ThinkingBindingRecover = true
			s.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}
		}, unchanged: true},
		{name: "unrelated malformed thinking and messages ignored", body: `{"thinking":null,"messages":42,"temperature":1}`, absent: "temperature", contains: `"thinking":null`},
		{name: "runtime budget config conflict bypasses all rules", body: `{"temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) {
			s.ThinkingBudgetEffortTable = []model_setting.ThinkingBudgetEffort{{MaxBudget: 1, Effort: "max"}}
		}, unchanged: true},
		{name: "disabled bypasses invalid historical config", body: `{ "temperature":1 }`, configure: func(s *model_setting.Claude5CompatSettings) { s.Enabled = false; s.TargetModels = []string{"["} }, unchanged: true},
		{name: "zero matched rules preserves bytes", body: `{ "unknown":9007199254740993 }`, unchanged: true},
		{name: "already normalized effort preserves bytes", body: `{ "output_config": { "effort": "high" } }`, configure: func(s *model_setting.Claude5CompatSettings) { s.OutputConfigPolicy = "keep_effort_only" }, unchanged: true},
		{name: "non text system blocks are kept", body: `{"system":[{"type":"image","text":""}],"temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }, absent: "temperature", contains: `"type":"image"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, DropTemperature: true}
			if tc.configure != nil {
				tc.configure(&s)
			}
			global.Claude5Compat = s
			body := []byte(tc.body)
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			got, err := applyClaude5Compat(c, nil, "target", "us-east-1", body)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(body), "input must not be mutated")
			if tc.unchanged {
				require.Equal(t, body, got)
			}
			if tc.absent != "" {
				var payload map[string]claude5JSON
				require.NoError(t, common.Unmarshal(got, &payload))
				require.NotContains(t, payload, tc.absent)
			}
			if tc.contains != "" {
				require.Contains(t, string(got), tc.contains)
			}
		})
	}
}

func TestClaude5OutputPolicyTypeMatrix(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, policy := range []string{"none", "strip_all", "keep_effort_only"} {
		for _, value := range []string{`null`, `"bad"`, `123`, `[]`, `true`, `{}`, `{"effort":null}`, `{"effort":"max"}`, `{"effort":"High"}`, `{"effort":"medium","format":{}}`, `{"effort":"high"}`} {
			t.Run(policy+"/"+value, func(t *testing.T) {
				global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, OutputConfigPolicy: policy, DropTemperature: true}
				got, err := applyClaude5Compat(nil, nil, "target", "us-east-1", []byte(`{"temperature":1,"output_config":`+value+`}`))
				require.NoError(t, err)
				var payload map[string]claude5JSON
				require.NoError(t, common.Unmarshal(got, &payload))
				require.NotContains(t, payload, "temperature", "invalid output_config must not roll back other rules")
				switch {
				case policy == "none":
					require.JSONEq(t, value, string(payload["output_config"]))
				case policy == "keep_effort_only" && strings.Contains(value, `"medium"`):
					require.JSONEq(t, `{"effort":"medium"}`, string(payload["output_config"]))
				case policy == "keep_effort_only" && value == `{"effort":"high"}`:
					require.JSONEq(t, value, string(payload["output_config"]))
				default:
					require.NotContains(t, payload, "output_config")
				}
			})
		}
	}
}

func TestClaude5AttributionGrammarAndFloor(t *testing.T) {
	for _, text := range []string{
		"cc_version=2.1.280.6d4; cc_entrypoint=sdk-cli;",
		"cc_version=2.1.312.03a; cc_entrypoint=cli;",
		"cc_version=2.1.318.0db; cc_entrypoint=claude-vscode;",
		"cc_version=2.1.318.148; cc_entrypoint=sdk-ts;",
		"cc_version=2.1.318.f02; cc_entrypoint=claude-vscode;",
		"cc_version=2.1.318;", "cc_version=2.1.318.foo.bar;",
	} {
		require.Equal(t, "pure", parseClaude5Attribution("x-anthropic-billing-header: "+text), text)
	}
	for _, tc := range []struct{ text, status string }{
		{"cc_version=2.1;", "unrecognized"}, {"cc_version=2.x.318;", "unrecognized"}, {"cc_version=+2.1.318;", "unrecognized"}, {"cc_version=02.1.318;", "unrecognized"}, {"cc_version=1000000000.1.318;", "unrecognized"}, {"cc_entrypoint=cli;", "unrecognized"}, {"", "unrecognized"},
		{"cc_version=2.1.318; cc_version=2.1.215;", "mixed"}, {"cc_version=2.1.318; cc_foo=bar;", "mixed"}, {"cc_version=;", "mixed"}, {"cc_version=2.1.318=x;", "mixed"}, {"cc_version=2.1.318;\nbusiness instructions", "mixed"},
	} {
		require.Equal(t, tc.status, parseClaude5Attribution("x-anthropic-billing-header: "+tc.text), tc.text)
	}
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, tc := range []struct{ original, want, policy string }{
		{"2.1.215.0db", "2.1.280.0db", "preserve"}, {"2.1.215.foo.bar", "2.1.280.foo.bar", "preserve"}, {"2.1.215.0db", "2.1.280", "remove"}, {"2.1.318.148", "2.1.318.148", "preserve"}, {"2.1.280.6d4", "2.1.280.6d4", "remove"},
	} {
		t.Run(tc.original+"/"+tc.policy, func(t *testing.T) {
			global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, AttributionMode: "forge", ForgeVersion: "2.1.280", FingerprintPolicy: tc.policy, ThinkingBindingRecover: true}
			text := "  x-anthropic-billing-header: cc_entrypoint=sdk-ts; cc_version =  " + tc.original + " ;  "
			body := mustClaude5Raw(map[string]any{"system": []any{map[string]any{"type": "text", "text": text}}})
			got, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
			require.NoError(t, err)
			var payload struct {
				System []struct {
					Text string `json:"text"`
				} `json:"system"`
			}
			require.NoError(t, common.Unmarshal(got, &payload))
			require.Equal(t, strings.Replace(text, tc.original, tc.want, 1), payload.System[0].Text)
		})
	}
}

func TestClaude5CapabilityFailureAndPassiveLearning(t *testing.T) {
	server := useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true, DropTemperature: true, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	global.Claude5Compat = s
	body := []byte(`{"temperature":0,"messages":[{"content":[{"type":"redacted_thinking","data":"opaque"}]}]}`)
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	got, err := applyClaude5Compat(c, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.Contains(t, string(got), claude5CompatBetaName)
	rejection := &smithy.GenericAPIError{Code: "ValidationException", Message: fmt.Sprintf("beta %s is not supported", claude5CompatBetaName)}
	recordClaude5BetaRejection(c, &smithy.GenericAPIError{Code: "ValidationException", Message: "invalid tool schema"})
	require.Empty(t, server.Keys())
	recordClaude5BetaRejection(c, rejection)
	require.Len(t, server.Keys(), 1)
	key := server.Keys()[0]
	ttl := server.TTL(key)
	server.FastForward(60 * 1e9)
	recordClaude5BetaRejection(c, rejection)
	require.Equal(t, ttl-60*1e9, server.TTL(key), "concurrent/repeated denials must not extend expiry")
	require.Equal(t, "unsupported", claude5BetaCapability(s, "target", "us-east-1"))
	s.CapabilityTableVersion = "after-admin-clear"
	require.Equal(t, "supported", claude5BetaCapability(s, "target", "us-east-1"), "version change invalidates Redis and local cache together")
	s.BetaCapabilities = append(s.BetaCapabilities, model_setting.Claude5BetaCapability{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "deny", Source: "static"})
	require.Equal(t, "supported", claude5BetaCapability(s, "target", "us-east-1"), "admin allow overrides static deny")
	s.BetaCapabilities[1].Source = "admin"
	require.Equal(t, "unsupported", claude5BetaCapability(s, "target", "us-east-1"))
	global.Claude5Compat.CapabilityTableVersion = "fault-test"
	require.NoError(t, common.RDB.Close())
	c = newAwsTestContext(httptest.NewRecorder(), t.Context())
	got, err = applyClaude5Compat(c, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.NotContains(t, string(got), claude5CompatBetaName)
	require.NotContains(t, string(got), "temperature", "Redis failure skips only B1")
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "b1_capability_read_error")
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "b1_skipped_redis_error")
}

func TestClaude5InvalidThinkingBudgetRollsBack(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, DropTemperature: true, ThinkingDisabledMap: true}
	for _, budget := range []string{`"1024"`, `null`, `true`, `-1`, `1.5`, `1e3`, `9007199254740992`} {
		t.Run(budget, func(t *testing.T) {
			body := []byte(`{ "temperature":0,"thinking":{"type":"disabled","budget_tokens":` + budget + `}}`)
			got, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
			require.NoError(t, err)
			require.Equal(t, body, got)
		})
	}
}

func TestClaude5AuditDoesNotEvaluateRulesAfterRollback(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingDisabledMap: true, DropTemperature: true, OutputConfigPolicy: "strip_all"}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	body := []byte(`{"thinking":null,"temperature":1,"output_config":{"effort":"high"}}`)
	got, err := applyClaude5Compat(c, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	value, _ := c.Get(claude5CompatAuditKey)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var audit struct {
		ConfigError bool `json:"config_error"`
		States      map[string]struct {
			Evaluated bool `json:"rule_evaluated"`
			Matched   bool `json:"rule_matched"`
			Changed   bool `json:"body_changed"`
		} `json:"rule_states"`
	}
	require.NoError(t, common.Unmarshal(encoded, &audit))
	require.True(t, audit.ConfigError)
	require.False(t, audit.States["temperature_dropped"].Evaluated)
	require.False(t, audit.States["output_config_stripped"].Evaluated)
	require.False(t, audit.States["temperature_dropped"].Changed)
	require.False(t, audit.States["temperature_dropped"].Matched)
	require.False(t, audit.States["output_config_stripped"].Matched)
}

func TestClaude5RedisDisabledDoesNotInjectUnverifiedBeta(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() {
		global.Claude5Compat = previous
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
	})
	common.RedisEnabled, common.RDB = false, nil
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true, DropTemperature: true, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	body := []byte(`{"temperature":1,"messages":[{"content":[{"type":"redacted_thinking","data":"private"}]}]}`)
	got, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
	require.NoError(t, err)
	require.NotContains(t, string(got), claude5CompatBetaName)
	require.NotContains(t, string(got), "temperature")
}

func TestClaude5ControlCohortPreservesBodyAndNeverInjectsBeta(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ControlPercent: 100, DropTemperature: true, ThinkingBindingRecover: true}
	body := []byte(`{ "temperature": 0, "messages": [] }`)
	for _, percent := range []int{100, 0} {
		global.Claude5Compat.ControlPercent = percent
		c := newAwsTestContext(httptest.NewRecorder(), t.Context())
		c.Set(common.RequestIdKey, "stable-request")
		got, err := applyClaude5Compat(c, nil, "target", "us-east-1", body)
		require.NoError(t, err)
		value, _ := c.Get(claude5CompatAuditKey)
		audit := value.(map[string]any)
		if percent == 100 {
			require.Equal(t, body, got)
			require.Equal(t, "control", audit["cohort"])
			require.Nil(t, audit["rule_states"])
		} else {
			require.NotContains(t, string(got), "temperature")
			require.Equal(t, "compatibility", audit["cohort"])
		}
		require.Len(t, audit["request_id_hmac"], 64)
	}
}

// This verifies real Redis visibility across independent client connections.
// Clearing the process cache models a fresh worker; deployment load is separate.
func TestClaude5CapabilityRealRedisVisibility(t *testing.T) {
	address := os.Getenv("TEST_CLAUDE5_REDIS_ADDR")
	if address == "" {
		t.Skip("TEST_CLAUDE5_REDIS_ADDR is not configured")
	}
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	previousSettings := model_setting.GetGlobalSettings().Claude5Compat
	claude5BetaDenyCache.Lock()
	previousCache := claude5BetaDenyCache.entries
	claude5BetaDenyCache.entries = make(map[string]claude5BetaDenyCacheEntry)
	claude5BetaDenyCache.Unlock()
	first := redis.NewClient(&redis.Options{Addr: address})
	second := redis.NewClient(&redis.Options{Addr: address})
	version := "test-" + common.GenerateHMAC(t.Name() + time.Now().String())[:32]
	s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true, CapabilityTableVersion: version, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	key := claude5BetaCapabilityKey("target", "us-east-1", version)
	t.Cleanup(func() {
		_ = first.Del(context.Background(), key).Err()
		_ = first.Close()
		_ = second.Close()
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
		model_setting.GetGlobalSettings().Claude5Compat = previousSettings
		claude5BetaDenyCache.Lock()
		claude5BetaDenyCache.entries = previousCache
		claude5BetaDenyCache.Unlock()
	})
	require.NoError(t, first.Ping(t.Context()).Err())
	common.RedisEnabled, common.RDB = true, first
	model_setting.GetGlobalSettings().Claude5Compat = s
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	_, err := applyClaude5Compat(c, nil, "target", "us-east-1", []byte(`{"messages":[{"content":[{"type":"thinking","thinking":"opaque","signature":"opaque"}]}]}`))
	require.NoError(t, err)
	recordClaude5BetaRejection(c, &smithy.GenericAPIError{Code: "ValidationException", Message: fmt.Sprintf("beta %s is not supported", claude5CompatBetaName)})
	require.Equal(t, "unsupported", second.Get(t.Context(), key).Val())
	ttl, err := second.TTL(t.Context(), key).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	common.RDB = second
	claude5BetaDenyCache.Lock()
	claude5BetaDenyCache.entries = make(map[string]claude5BetaDenyCacheEntry)
	claude5BetaDenyCache.Unlock()
	require.Equal(t, "unsupported", claude5BetaCapability(s, "target", "us-east-1"))
	s.CapabilityTableVersion += "-cleared"
	require.Equal(t, "supported", claude5BetaCapability(s, "target", "us-east-1"))
}

func TestClaude5MappedAdaptiveDisplayAuditMatchesItsActualChange(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingEnabledMap: true, ThinkingDisplayNormalize: true, OutputConfigPolicy: "keep_effort_only"}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	result, err := applyClaude5Compat(c, nil, "target", "us-east-1", []byte(`{"thinking":{"type":"enabled","budget_tokens":1024,"adaptive":{"display":"invalid"}}}`))
	require.NoError(t, err)
	require.Contains(t, string(result), `"display":"summarized"`)
	value, _ := c.Get(claude5CompatAuditKey)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var audit struct {
		States map[string]claude5RuleState `json:"rule_states"`
	}
	require.NoError(t, common.Unmarshal(encoded, &audit))
	state := audit.States["thinking_display_normalized"]
	require.True(t, state.Evaluated)
	require.True(t, state.Changed)
	require.True(t, state.Matched, "a normalization performed after enabled-to-adaptive mapping must be reported as matched")
	require.False(t, state.Skipped)
}

func TestClaude5CapabilityLateReadCannotOverwriteObservedRejection(t *testing.T) {
	useClaude5Redis(t)
	s := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	key := claude5BetaCapabilityKey("target", "us-east-1", "")
	// A slow GET can observe a missing key before another request records a
	// rejection, then finish after that rejection has updated the local cache.
	setClaude5BetaDenyCache(key, true)
	claude5BetaDenyCache.Lock()
	originalExpiry := claude5BetaDenyCache.entries[key].expires
	claude5BetaDenyCache.Unlock()
	setClaude5BetaDenyCache(key, false)
	require.Equal(t, "unsupported", claude5BetaCapability(s, "target", "us-east-1"))
	claude5BetaDenyCache.Lock()
	currentExpiry := claude5BetaDenyCache.entries[key].expires
	// Expiration still permits the next authoritative lookup to replace denial.
	claude5BetaDenyCache.entries[key] = claude5BetaDenyCacheEntry{denied: true, expires: time.Now().Add(-time.Second)}
	claude5BetaDenyCache.Unlock()
	require.Equal(t, originalExpiry, currentExpiry)
	setClaude5BetaDenyCache(key, false)
	require.Equal(t, "supported", claude5BetaCapability(s, "target", "us-east-1"))
}

func TestClaude5CancelledRequestSkipsCapabilityLookupAndInjection(t *testing.T) {
	server := useClaude5Redis(t)
	settings := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := newAwsTestContext(httptest.NewRecorder(), ctx)
	before := server.CommandCount()
	require.Equal(t, "unknown", claude5BetaCapabilityForRequest(c, settings, "target", "us-east-1"))
	require.Equal(t, before, server.CommandCount(), "disconnected clients must not trigger an independent Redis lookup")
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "diag_b1_request_cancelled")
}

func TestClaude5PassiveLearningRequiresActualValidationError(t *testing.T) {
	server := useClaude5Redis(t)
	s := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "target", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	c.Set(claude5CompatBetaContextKey, true)
	c.Set(claude5CompatModelContextKey, "target")
	c.Set(claude5CompatRegionContextKey, "us-east-1")
	c.Set(claude5CompatCapabilityKey, claude5BetaCapabilityKey("target", "us-east-1", ""))
	text := fmt.Sprintf("ValidationException: beta %s is not supported", claude5CompatBetaName)
	for _, err := range []error{
		fmt.Errorf("transport error: %s", text),
		&smithy.GenericAPIError{Code: "AccessDeniedException", Message: text},
		&smithy.GenericAPIError{Code: "ThrottlingException", Message: text},
		fmt.Errorf("%s: %w", text, &smithy.GenericAPIError{Code: "ValidationException", Message: "invalid tool schema"}),
		&smithy.GenericAPIError{Code: "ValidationException", Message: "unknown beta: another-beta"},
		&smithy.GenericAPIError{Code: "ValidationException", Message: fmt.Sprintf("tool_choice type tool is not supported with beta %s", claude5CompatBetaName)},
		&smithy.GenericAPIError{Code: "ValidationException", Message: fmt.Sprintf("unsupported tool schema; request includes beta %s", claude5CompatBetaName)},
		&smithy.GenericAPIError{Code: "ValidationException", Message: fmt.Sprintf("beta %s-extra is not supported", claude5CompatBetaName)},
	} {
		recordClaude5BetaRejection(c, err)
		require.Empty(t, server.Keys(), "text alone must not classify transport, authorization or throttling failures as beta rejection")
	}
	for _, message := range []string{
		fmt.Sprintf("beta %s is not supported", claude5CompatBetaName),
		fmt.Sprintf("The beta flag '%s' is not supported for this model", claude5CompatBetaName),
		fmt.Sprintf("Unsupported beta: %s", claude5CompatBetaName),
		fmt.Sprintf("Unknown beta value: \"%s\"", claude5CompatBetaName),
	} {
		server.FlushAll()
		recordClaude5BetaRejection(c, fmt.Errorf("wrapped: %w", &smithy.GenericAPIError{Code: "ValidationException", Message: message}))
		require.Len(t, server.Keys(), 1, message)
	}
	require.Equal(t, "unsupported", claude5BetaCapability(s, "target", "us-east-1"))
}
