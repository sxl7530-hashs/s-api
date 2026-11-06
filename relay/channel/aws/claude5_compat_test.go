package aws

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
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
					info.ChannelType = constant.ChannelTypeAws
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
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{
		Enabled:                true,
		TargetModels:           []string{"anthropic.claude-opus-5"},
		AttributionMode:        "remove",
		ThinkingBindingRecover: true,
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
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	for _, kind := range []string{"enabled", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true, ThinkingEnabledMap: true, ThinkingDisabledMap: true}
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
		}},
		{"budget effort and strip", `{"thinking":{"type":"enabled","budget_tokens":1000},"output_config":{"format":{"type":"json"}}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"low"}}`, func(s *model_setting.Claude5CompatSettings) {
			s.ThinkingEnabledMap = true
			s.StripOutputConfig = true
			s.ThinkingBudgetEffortTable = []model_setting.ThinkingBudgetEffort{{MaxBudget: 2000, Effort: "low"}}
		}},
		{"context only", `{"context_management":{},"unknown":false}`, `{"unknown":false}`, func(s *model_setting.Claude5CompatSettings) { s.StripContextManagement = true }},
		{"container only", `{"messages":[{"role":"assistant","content":[{"type":"text","text":"keep","container":{},"unknown":0}]}]}`, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"keep","unknown":0}]}]}`, func(s *model_setting.Claude5CompatSettings) { s.StripContainerFields = true }},
		{"tool choice opt in", `{"tool_choice":{"type":"tool","name":"lookup"},"tools":[{"name":"lookup"}]}`, `{"tool_choice":{"type":"auto"},"tools":[{"name":"lookup"}]}`, func(s *model_setting.Claude5CompatSettings) { s.MapToolChoiceAuto = true }},
		{"empty system", `{"system":[],"messages":[]}`, `{"messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"empty string system", `{"system":"","messages":[]}`, `{"messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"blank string system", `{"system":" \n\t ","messages":[]}`, `{"messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"nonempty string system", `{"system":"  keep instructions  ","messages":[]}`, `{"system":"  keep instructions  ","messages":[]}`, func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"blank system cleaning disabled", `{"system":" \n\t ","messages":[]}`, `{"system":" \n\t ","messages":[]}`, func(s *model_setting.Claude5CompatSettings) {}},
		{"display absent", `{"thinking":{"type":"adaptive"}}`, `{"thinking":{"type":"adaptive"}}`, func(s *model_setting.Claude5CompatSettings) { s.ThinkingDisplayNormalize = true }},
		{"strip output without conversion", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`, `{"thinking":{"type":"adaptive"}}`, func(s *model_setting.Claude5CompatSettings) { s.StripOutputConfig = true; s.ThinkingEnabledMap = true }},
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
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"target"}, ThinkingBindingRecover: true}
	for _, control := range []string{`"thinking":"bad"`, `"thinking":{"block_binding":[]}`, `"anthropic_beta":"bad"`} {
		body := []byte(`{` + control + `,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`)
		_, err := applyClaude5Compat(nil, nil, "target", "us-east-1", body)
		require.Error(t, err, control)
	}
}

func TestClaude5ConflictingCachePoliciesRemainUntouched(t *testing.T) {
	raw := `{"type":"tool_result","tool_use_id":"tool-1","cache_control":{"type":"ephemeral","ttl":"1h"},"content":[{"type":"text","text":"result","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`
	var block map[string]claude5JSON
	require.NoError(t, common.Unmarshal([]byte(raw), &block))
	require.False(t, moveToolResultCacheControl(block))
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
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	for _, mode := range []string{"remove", "forge"} {
		t.Run(mode, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{
				Enabled: true, TargetModels: []string{"target"}, AttributionMode: mode,
				ForgeVersion: "2.1.280", ThinkingBindingRecover: true, ThinkingEnabledMap: true,
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
