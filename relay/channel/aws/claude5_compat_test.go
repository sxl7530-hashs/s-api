package aws

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaude5StringSystemFinalAwsBody(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, compatibility := range []bool{false, true} {
		for _, mode := range []string{"off", "remove", "forge"} {
			for _, clean := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					for _, system := range []string{"business instructions", "  x-anthropic-billing-header: cc_version=2.1.215.6d4; cc_entrypoint=cli;\nKeep business instructions.  ", ""} {
						t.Run(fmt.Sprintf("compat=%v/mode=%s/clean=%v/stream=%v/system=%q", compatibility, mode, clean, stream, system), func(t *testing.T) {
							global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"*"}, AttributionMode: mode, ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, CleanEmptySystem: clean, DropTemperature: true}
							input := mustClaude5Raw(map[string]any{"system": system, "temperature": 0, "max_tokens": 128, "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
							c := newAwsTestContext(httptest.NewRecorder(), t.Context())
							info := newAwsTestRelayInfo()
							info.ChannelType = constant.ChannelTypeAws
							info.UpstreamModelName = "anthropic.claude-sonnet-4-6"
							info.ApiKey = "test-access|test-secret|us-west-1"
							info.IsStream = stream
							info.AwsPassthroughCompatibility = compatibility
							a := &Adaptor{}
							_, err := doAwsClientRequest(c, info, a, strings.NewReader(string(input)))
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
							assert.Equal(t, system, payload["system"])
							assert.NotContains(t, payload, "temperature")
							assert.Contains(t, c.GetStringSlice(claude5CompatContextKey), "temperature_dropped")
							value, _ := c.Get(claude5CompatAuditKey)
							audit, ok := value.(map[string]any)
							require.True(t, ok)
							assert.NotContains(t, audit, "config_error")
							assert.NotContains(t, audit, "error_code")
							if mode != "off" {
								assert.Equal(t, "system_string", audit["attribution_skip_reason"])
							}
							if clean {
								assert.Equal(t, "system_string", audit["system_cleanup_skip_reason"])
							}
						})
					}
				}
			}
		}
	}
}

func TestClaude5FailureDiagnosticsAndAtomicRollback(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, tc := range []struct {
		name, body, stage, code string
		configure               func(*model_setting.Claude5CompatSettings)
	}{
		{"attribution invalid system", `{"system":42,"temperature":0}`, "attribution", "system_invalid_type", func(s *model_setting.Claude5CompatSettings) { s.AttributionMode = "remove" }},
		{"system prevalidation", `{"system":false,"temperature":0}`, "system_validation", "system_invalid_type", func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }},
		{"thinking object", `{"thinking":null,"temperature":0}`, "thinking_validation", "thinking_invalid_type", nil},
		{"thinking type", `{"thinking":{"type":false},"temperature":0}`, "thinking_validation", "thinking_type_invalid", nil},
		{"budget type", `{"thinking":{"type":"enabled","budget_tokens":1024.0},"temperature":0}`, "thinking_validation", "budget_not_integer", nil},
		{"budget bound", `{"thinking":{"budget_tokens":9007199254740992},"temperature":0}`, "thinking_validation", "budget_out_of_range", nil},
		{"adaptive type", `{"thinking":{"adaptive":null},"temperature":0}`, "thinking_validation", "adaptive_invalid_type", nil},
		{"messages type", `{"messages":42,"temperature":0}`, "message_validation", "messages_invalid_type", nil},
		{"messages entry", `{"messages":[null],"temperature":0}`, "message_validation", "messages_invalid_array", nil},
		{"content entry", `{"messages":[{"content":[null]}],"temperature":0}`, "message_validation", "content_invalid_blocks", nil},
		{"beta entry", `{"anthropic_beta":[null],"messages":[{"content":[{"type":"redacted_thinking","data":"private"}]}],"temperature":0}`, "binding_recovery", "beta_invalid_entry", nil},
		{"binding type", `{"thinking":{"block_binding":false},"messages":[{"content":[{"type":"thinking","thinking":"private"}]}],"temperature":0}`, "binding_recovery", "binding_invalid_type", nil},
		{"decode excludes prompt", `{"system":"private-prompt",`, "request_decode", "processing_failed", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"*"}, DropTemperature: true, ThinkingBindingRecover: true, BlockBindingWrite: true}
			if tc.configure != nil {
				tc.configure(&s)
			}
			global.Claude5Compat = s
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-4-6", "us-west-1", []byte(tc.body))
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(got))
			value, _ := c.Get(claude5CompatAuditKey)
			audit, ok := value.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, audit["config_error"])
			assert.Equal(t, false, audit["body_changed"])
			assert.Equal(t, tc.stage, audit["error_stage"])
			assert.Equal(t, tc.code, audit["error_code"])
			encoded, err := common.Marshal(audit)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private")
			info := newAwsTestRelayInfo()
			info.ChannelType = constant.ChannelTypeAws
			other := model.NewLogOther()
			service.AppendRelayLogAdminInfo(c, info, other)
			snapshot := other.Snapshot()
			admin, ok := snapshot["admin_info"].(map[string]any)
			require.True(t, ok)
			metadata, ok := admin["claude5_compat"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.stage, metadata["error_stage"])
			assert.Equal(t, tc.code, metadata["error_code"])
			assert.NotContains(t, snapshot, "claude5_compat")
			encoded, err = common.Marshal(snapshot)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private")

		})
	}
}

func TestClaude5StringSystemPreservesThinkingToolHistory(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"*"}, AttributionMode: "remove", ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, CleanEmptySystem: true, DropTemperature: true}
	body := []byte(`{"system":"Keep business instructions.","temperature":0,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"signature"},{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"result"}]}]}`)
	var original map[string]any
	require.NoError(t, common.Unmarshal(body, &original))
	for _, model := range []string{"anthropic.claude-sonnet-4-6", "anthropic.claude-opus-4-6-v1"} {
		t.Run(model, func(t *testing.T) {
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			got, err := applyClaude5Compat(c, nil, model, "us-west-1", body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(got, &payload))
			assert.Equal(t, original["system"], payload["system"])
			assert.Equal(t, original["messages"], payload["messages"])
			assert.NotContains(t, payload, "temperature")
			thinking, ok := payload["thinking"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "enabled", thinking["type"])
			assert.Equal(t, map[string]any{"prefix_mismatch_behavior": "drop_block"}, thinking["block_binding"])
			assert.Contains(t, payload["anthropic_beta"], claude5CompatBetaName)
			assert.NotContains(t, c.GetStringSlice(claude5CompatContextKey), "config_error")
		})
	}
}

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
					require.Contains(t, payload, "temperature", "direct passthrough always takes precedence over the legacy override")
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
			settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, ThinkingDisabledMap: true, OutputConfigPolicy: "keep_effort_only", BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
			body := []byte(`{"thinking":{"type":"` + kind + `","budget_tokens":1024},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"signature"},{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"result"}]}]}`)
			result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(result, &decoded))
			thinking := decoded["thinking"].(map[string]any)
			if kind == "disabled" {
				require.Equal(t, "between_tools", thinking["type"])
				require.NotContains(t, thinking, "block_binding")
			} else {
				require.Equal(t, map[string]any{"prefix_mismatch_behavior": "drop_block"}, thinking["block_binding"])
			}
			var original map[string]any
			require.NoError(t, common.Unmarshal(body, &original))
			require.Equal(t, original["messages"], decoded["messages"])
		})
	}
}

func TestClaude5DisabledBindingCleanup(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, writeBinding := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled/write_binding=%v", writeBinding), func(t *testing.T) {
			global.Claude5Compat = model_setting.Claude5CompatSettings{
				Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-4-6"},
				ThinkingBindingRecover: true, BlockBindingWrite: writeBinding,
				BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-4-6", Region: "us-west-1", Beta: claude5CompatBetaName, Status: "allow"}},
			}
			body := []byte(`{"thinking":{"type":"disabled","block_binding":{"prefix_mismatch_behavior":"drop_block"},"budget_tokens":64,"display":"full"},"anthropic_beta":["client-beta","thinking-binding-controls-2026-08-01"],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"opaque","signature":"opaque"}]}]}`)
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-4-6", "us-west-1", body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(got, &payload))
			thinking := payload["thinking"].(map[string]any)
			require.Equal(t, "disabled", thinking["type"])
			require.NotContains(t, thinking, "block_binding")
			require.Equal(t, float64(64), thinking["budget_tokens"])
			require.Equal(t, "full", thinking["display"])
			require.Equal(t, []any{"client-beta", claude5CompatBetaName}, payload["anthropic_beta"])
			value, ok := c.Get(claude5CompatAuditKey)
			require.True(t, ok)
			audit := value.(map[string]any)
			require.Equal(t, "client_supplied_disabled", audit["block_binding_strip_reason"])
			require.Equal(t, []string{"budget_tokens", "display"}, audit["disabled_extra_fields"])
			require.False(t, c.GetBool(claude5CompatBetaContextKey), "a client-supplied beta must not be recorded as gateway-injected")
		})
	}

	for _, kind := range []string{"adaptive", "enabled", "between_tools"} {
		t.Run(kind+"/binding_preserved", func(t *testing.T) {
			global.Claude5Compat = model_setting.Claude5CompatSettings{
				Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-4-6"},
				ThinkingBindingRecover: true, BlockBindingWrite: true,
				BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-4-6", Region: "us-west-1", Beta: claude5CompatBetaName, Status: "allow"}},
			}
			body := []byte(`{"thinking":{"type":"` + kind + `","block_binding":{"prefix_mismatch_behavior":"drop_block"}},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"opaque","signature":"opaque"}]}]}`)
			got, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-4-6", "us-west-1", body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(got, &payload))
			thinking := payload["thinking"].(map[string]any)
			require.Equal(t, map[string]any{"prefix_mismatch_behavior": "drop_block"}, thinking["block_binding"])
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
		{"AWS target", constant.ChannelTypeAws, "anthropic.claude-sonnet-5-5", "us-east-1", false, false, true},
		{"other model", constant.ChannelTypeAws, "other", "us-east-1", false, false, false},
		{"other region", constant.ChannelTypeAws, "anthropic.claude-sonnet-5-5", "us-west-2", false, false, false},
		{"direct excluded", constant.ChannelTypeAws, "anthropic.claude-sonnet-5-5", "us-east-1", true, false, false},
		{"direct override ignored", constant.ChannelTypeAws, "anthropic.claude-sonnet-5-5", "us-east-1", true, true, false},
		{"Anthropic excluded", constant.ChannelTypeAnthropic, "anthropic.claude-sonnet-5-5", "us-east-1", false, true, false},
		{"OpenAI excluded", constant.ChannelTypeOpenAI, "anthropic.claude-sonnet-5-5", "us-east-1", false, true, false},
		{"Gemini excluded", constant.ChannelTypeGemini, "anthropic.claude-sonnet-5-5", "us-east-1", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, TargetRegions: []string{"us-east-1"}, DropTemperature: true, StripContainerFields: true, ApplyToPassthroughUsers: tc.include}
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
		{"enabled and display", `{"thinking":{"type":"enabled","budget_tokens":1000,"adaptive":{"display":"bad"}}}`, `{"thinking":{"type":"adaptive","display":"summarized"}}`, func(s *model_setting.Claude5CompatSettings) {
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
			s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}}
			tc.configure(&s)
			settings.Claude5Compat = s
			result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", []byte(tc.body))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(result))
		})
	}
}

func TestClaude5ForgePreservesFingerprintAndToolConversation(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	previous := settings.Claude5Compat
	t.Cleanup(func() { settings.Claude5Compat = previous })
	settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, AttributionMode: "forge", ForgeVersion: "2.1.280", ThinkingBindingRecover: true}
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.0db; cc_entrypoint=sdk-ts;"},{"type":"text","text":"actual instructions"}],"anthropic_beta":["existing-beta"],"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]}]}`)
	result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
	require.NoError(t, err)
	require.Contains(t, string(result), "cc_version=2.1.280.0db; cc_entrypoint=sdk-ts;")
	require.Contains(t, string(result), "existing-beta")
	require.Contains(t, string(result), "actual instructions")
	again, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", result)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(again))
}

func TestClaude5RulesPreserveUntargetedSignedContent(t *testing.T) {
	// A field-cleaning switch must not rewrite thinking data as a side effect.
	for _, settings := range []model_setting.Claude5CompatSettings{
		{DropTemperature: true},
		{ThinkingDisabledMap: true},
		{ThinkingEnabledMap: true},
	} {
		thinking := claude5JSON(`{ "type": "adaptive", "signature": "opaque", "adaptive": { "display": "omitted" } }`)
		request := map[string]claude5JSON{"thinking": thinking}
		var changed []string
		applyClaude5ThinkingMapping(request, settings, "anthropic.claude-sonnet-5-5", &changed)
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
	settings.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingBindingRecover: true}
	for _, control := range []string{`"thinking":"bad"`, `"thinking":{"block_binding":[]}`, `"thinking":{"block_binding":{"prefix_mismatch_behavior":null}}`, `"anthropic_beta":"bad"`} {
		body := []byte(`{` + control + `,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`)
		result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
		require.NoError(t, err, control)
		require.Equal(t, body, result, control)
	}
	settings.Claude5Compat.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}
	settings.Claude5Compat.BlockBindingWrite = true
	body := []byte(`{"temperature":1,"thinking":{"block_binding":{"prefix_mismatch_behavior":null}},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`)
	settings.Claude5Compat.DropTemperature = true
	result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
				Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, AttributionMode: mode,
				ForgeVersion: "2.1.280", ThinkingBindingRecover: true, BlockBindingWrite: true, ThinkingEnabledMap: true, OutputConfigPolicy: "keep_effort_only",
				BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}},
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
				result, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
				repeated, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", result)
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
		{name: "system string preserves independent cleaning", body: `{"system":"business instructions","temperature":1}`, configure: func(s *model_setting.Claude5CompatSettings) { s.CleanEmptySystem = true }, absent: "temperature", contains: "business instructions"},
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
			s.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}
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
			s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, DropTemperature: true}
			if tc.configure != nil {
				tc.configure(&s)
			}
			global.Claude5Compat = s
			body := []byte(tc.body)
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
				global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, OutputConfigPolicy: policy, DropTemperature: true}
				got, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", []byte(`{"temperature":1,"output_config":`+value+`}`))
				require.NoError(t, err)
				var payload map[string]claude5JSON
				require.NoError(t, common.Unmarshal(got, &payload))
				require.NotContains(t, payload, "temperature", "invalid output_config must not roll back other rules")
				switch {
				case policy == "none":
					require.JSONEq(t, value, string(payload["output_config"]))
				case policy == "keep_effort_only" && strings.Contains(value, `"medium"`):
					require.JSONEq(t, `{"effort":"medium"}`, string(payload["output_config"]))
				case policy == "keep_effort_only" && (value == `{"effort":"high"}` || value == `{"effort":"max"}`):
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
	for _, mode := range []string{"remove", "forge"} {
		for _, metadata := range []string{
			"cc_version=2.1.215.0db; cc_entrypoint=cli Follow business instructions;",
			"cc_version=2.1.215.0db Follow business instructions; cc_entrypoint=cli;",
			"cc_version=2.1.215.0db; cc_entrypoint=cli\tFollow business instructions;",
			"cc_version=2.1.215.0db; cc_entrypoint=cli\x00business;",
		} {
			t.Run(mode+"/"+metadata, func(t *testing.T) {
				global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"*"}, AttributionMode: mode, ThinkingBindingRecover: true}
				body := mustClaude5Raw(map[string]any{"system": []any{map[string]any{"type": "text", "text": "x-anthropic-billing-header: " + metadata}}})
				c := newAwsTestContext(httptest.NewRecorder(), t.Context())
				got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
				require.NoError(t, err)
				require.Equal(t, []byte(body), got, "ambiguous metadata values must not permit removing or rewriting business instructions")
				require.Equal(t, "mixed", c.GetString("claude5_compat_attribution"))
			})
		}
	}
	for _, tc := range []struct{ original, want, policy string }{
		{"2.1.215.0db", "2.1.280.0db", "preserve"}, {"2.1.215.foo.bar", "2.1.280.foo.bar", "preserve"}, {"2.1.215.0db", "2.1.280", "remove"}, {"2.1.318.148", "2.1.318.148", "preserve"}, {"2.1.280.6d4", "2.1.280.6d4", "remove"},
	} {
		t.Run(tc.original+"/"+tc.policy, func(t *testing.T) {
			global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, AttributionMode: "forge", ForgeVersion: "2.1.280", FingerprintPolicy: tc.policy, ThinkingBindingRecover: true}
			text := "  x-anthropic-billing-header: cc_entrypoint=sdk-ts; cc_version =  " + tc.original + " ;  "
			body := mustClaude5Raw(map[string]any{"system": []any{map[string]any{"type": "text", "text": text}}})
			got, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
	s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingBindingRecover: true, DropTemperature: true, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	global.Claude5Compat = s
	body := []byte(`{"temperature":0,"messages":[{"content":[{"type":"redacted_thinking","data":"opaque"}]}]}`)
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
	require.Equal(t, "unsupported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
	s.CapabilityTableVersion = "after-admin-clear"
	require.Equal(t, "supported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"), "version change invalidates Redis and local cache together")
	s.BetaCapabilities = append(s.BetaCapabilities, model_setting.Claude5BetaCapability{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "deny", Source: "static"})
	require.Equal(t, "supported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"), "admin allow overrides static deny")
	s.BetaCapabilities[1].Source = "admin"
	require.Equal(t, "unsupported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
	global.Claude5Compat.CapabilityTableVersion = "fault-test"
	require.NoError(t, common.RDB.Close())
	c = newAwsTestContext(httptest.NewRecorder(), t.Context())
	got, err = applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
	require.NoError(t, err)
	require.Contains(t, string(got), claude5CompatBetaName, "explicit allow remains effective during Redis failure")
	require.NotContains(t, string(got), "temperature")
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "b1_capability_read_error")
	require.NotContains(t, c.GetStringSlice(claude5CompatContextKey), "b1_skipped_redis_error")
}

func TestClaude5InvalidThinkingBudgetRollsBack(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, DropTemperature: true, ThinkingDisabledMap: true}
	for _, budget := range []string{`"1024"`, `null`, `true`, `-1`, `1.5`, `1e3`, `9007199254740992`} {
		t.Run(budget, func(t *testing.T) {
			body := []byte(`{ "temperature":0,"thinking":{"type":"disabled","budget_tokens":` + budget + `}}`)
			got, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
			require.NoError(t, err)
			require.Equal(t, body, got)
		})
	}
}

func TestClaude5AuditDoesNotEvaluateRulesAfterRollback(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingDisabledMap: true, DropTemperature: true, OutputConfigPolicy: "strip_all"}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	body := []byte(`{"thinking":null,"temperature":1,"output_config":{"effort":"high"}}`)
	got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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

func TestClaude5RedisDisabledPreservesExplicitBetaAllow(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() {
		global.Claude5Compat = previous
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
	})
	common.RedisEnabled, common.RDB = false, nil
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingBindingRecover: true, DropTemperature: true, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	body := []byte(`{"temperature":1,"messages":[{"content":[{"type":"redacted_thinking","data":"private"}]}]}`)
	got, err := applyClaude5Compat(nil, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
	require.NoError(t, err)
	require.Contains(t, string(got), claude5CompatBetaName)
	require.NotContains(t, string(got), "temperature")
}

func TestClaude5ControlCohortPreservesBodyAndNeverInjectsBeta(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ControlPercent: 100, DropTemperature: true, ThinkingBindingRecover: true}
	body := []byte(`{ "temperature": 0, "messages": [] }`)
	for _, percent := range []int{100, 0} {
		global.Claude5Compat.ControlPercent = percent
		c := newAwsTestContext(httptest.NewRecorder(), t.Context())
		c.Set(common.RequestIdKey, "stable-request")
		got, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", body)
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
	s := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingBindingRecover: true, CapabilityTableVersion: version, BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	key := claude5BetaCapabilityKey("anthropic.claude-sonnet-5-5", "us-east-1", version)
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
	_, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", []byte(`{"messages":[{"content":[{"type":"thinking","thinking":"opaque","signature":"opaque"}]}]}`))
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
	require.Equal(t, "unsupported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
	s.CapabilityTableVersion += "-cleared"
	require.Equal(t, "supported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
}

func TestClaude5MappedAdaptiveDisplayAuditMatchesItsActualChange(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-sonnet-5-5"}, ThinkingEnabledMap: true, ThinkingDisplayNormalize: true, OutputConfigPolicy: "keep_effort_only"}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	result, err := applyClaude5Compat(c, nil, "anthropic.claude-sonnet-5-5", "us-east-1", []byte(`{"thinking":{"type":"enabled","budget_tokens":1024,"adaptive":{"display":"invalid"}}}`))
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
	s := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	key := claude5BetaCapabilityKey("anthropic.claude-sonnet-5-5", "us-east-1", "")
	// A slow GET can observe a missing key before another request records a
	// rejection, then finish after that rejection has updated the local cache.
	setClaude5BetaDenyCache(key, true)
	claude5BetaDenyCache.Lock()
	originalExpiry := claude5BetaDenyCache.entries[key].expires
	claude5BetaDenyCache.Unlock()
	setClaude5BetaDenyCache(key, false)
	require.Equal(t, "unsupported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
	claude5BetaDenyCache.Lock()
	currentExpiry := claude5BetaDenyCache.entries[key].expires
	// Expiration still permits the next authoritative lookup to replace denial.
	claude5BetaDenyCache.entries[key] = claude5BetaDenyCacheEntry{denied: true, expires: time.Now().Add(-time.Second)}
	claude5BetaDenyCache.Unlock()
	require.Equal(t, originalExpiry, currentExpiry)
	setClaude5BetaDenyCache(key, false)
	require.Equal(t, "supported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
}

func TestClaude5CancelledRequestSkipsCapabilityLookupAndInjection(t *testing.T) {
	server := useClaude5Redis(t)
	settings := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := newAwsTestContext(httptest.NewRecorder(), ctx)
	before := server.CommandCount()
	require.Equal(t, "unknown", claude5BetaCapabilityForRequest(c, settings, "anthropic.claude-sonnet-5-5", "us-east-1"))
	require.Equal(t, before, server.CommandCount(), "disconnected clients must not trigger an independent Redis lookup")
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "diag_b1_request_cancelled")
}

func TestClaude5PassiveLearningRequiresActualValidationError(t *testing.T) {
	server := useClaude5Redis(t)
	s := model_setting.Claude5CompatSettings{BetaCapabilities: []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-sonnet-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "allow"}}}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	c.Set(claude5CompatBetaContextKey, true)
	c.Set(claude5CompatModelContextKey, "anthropic.claude-sonnet-5-5")
	c.Set(claude5CompatRegionContextKey, "us-east-1")
	c.Set(claude5CompatCapabilityKey, claude5BetaCapabilityKey("anthropic.claude-sonnet-5-5", "us-east-1", ""))
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
	require.Equal(t, "unsupported", claude5BetaCapability(s, "anthropic.claude-sonnet-5-5", "us-east-1"))
}

// These contracts exercise the serialized settings used during an upgrade,
// rather than assuming new fields have already been saved by the administrator.
func TestClaudeCompatibilityUpgradeContracts(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() {
		global.Claude5Compat = previous
		common.RedisEnabled, common.RDB = previousEnabled, previousRDB
	})
	common.RedisEnabled, common.RDB = false, nil
	for _, tc := range []struct {
		name, model, settings, body, want string
	}{
		{"legacy mapping preserves structured output", "global.anthropic.claude-opus-5", `{"thinking_enabled_map":true,"thinking_budget_effort_table":[{"max_budget":2048,"effort":"medium"}]}`, `{"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}},"future":false}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium","format":{"type":"json_schema","schema":{"type":"object"}},"future":false}}`},
		{"client effort wins over budget mapping", "anthropic.claude-opus-5", `{"thinking_enabled_map":true,"output_config_policy":"none","thinking_budget_effort_table":[{"max_budget":2048,"effort":"low"}]}`, `{"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"max","format":{"type":"json_schema"}}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max","format":{"type":"json_schema"}}}`},
		{"4.6 retains supported enabled format", "us.anthropic.claude-opus-4-6-v1", `{"thinking_enabled_map":true}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`},
		{"4.5 does not gain unsupported adaptive", "anthropic.claude-sonnet-4-5-20250929-v1:0", `{"thinking_enabled_map":true}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`},
		{"unknown model is not guessed", "anthropic.claude-opus-50", `{"thinking_enabled_map":true}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`, `{"thinking":{"type":"enabled","budget_tokens":1024}}`},
		{"sonnet 5.5 disabled maps to between tools", "global.anthropic.claude-sonnet-5-5", `{"thinking_disabled_map":true}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, `{"thinking":{"type":"between_tools"},"output_config":{"effort":"high"}}`},
		{"sonnet 5 preserves supported disabled", "anthropic.claude-sonnet-5", `{"thinking_disabled_map":true}`, `{"thinking":{"type":"disabled"}}`, `{"thinking":{"type":"disabled"}}`},
		{"opus 5.5 needs explicit semantic downgrade", "anthropic.claude-opus-5-5", `{"thinking_disabled_map":true}`, `{"thinking":{"type":"disabled"}}`, `{"thinking":{"type":"disabled"}}`},
		{"opus 5.5 explicit downgrade", "anthropic.claude-opus-5-5", `{"thinking_disabled_map":true,"allow_thinking_downgrade":true}`, `{"thinking":{"type":"disabled"}}`, `{"thinking":{"type":"adaptive"}}`},
		{"sonnet between tools rejects max without downgrade", "anthropic.claude-sonnet-5-5", `{"thinking_disabled_map":true}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`},
		{"sonnet max explicit downgrade preserves effort", "anthropic.claude-sonnet-5-5", `{"thinking_disabled_map":true,"allow_thinking_downgrade":true}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`},
		{"haiku 5.5 disabled preserves high effort", "anthropic.claude-haiku-5-5", `{"thinking_disabled_map":true}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`},
		{"haiku 5.5 max explicit downgrade", "anthropic.claude-haiku-5-5", `{"thinking_disabled_map":true,"allow_thinking_downgrade":true}`, `{"thinking":{"type":"disabled"},"output_config":{"effort":"max"}}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`},
		{"explicit cleanup retains valid max effort", "anthropic.claude-opus-5-5", `{"output_config_policy":"keep_effort_only"}`, `{"output_config":{"effort":"max","format":{"type":"json_schema"}}}`, `{"output_config":{"effort":"max"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settings model_setting.Claude5CompatSettings
			require.NoError(t, common.Unmarshal([]byte(tc.settings), &settings))
			settings.Enabled, settings.TargetModels = true, []string{"*"}
			require.NoError(t, model_setting.ValidateClaude5CompatSettings(settings))
			global.Claude5Compat = settings
			result, err := applyClaude5Compat(nil, nil, tc.model, "us-east-1", []byte(tc.body))
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(result))
		})
	}
}

func TestClaudeBindingCapabilityStrategiesWithoutRedis(t *testing.T) {
	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousRDB })
	common.RedisEnabled, common.RDB = false, nil
	for _, tc := range []struct {
		name, policy, model, want string
		override                  string
	}{
		{"legacy omitted", "", "anthropic.claude-opus-5", "supported", ""},
		{"legacy explicit", "legacy", "anthropic.claude-opus-5", "supported", ""},
		{"official supported model", "official", "global.anthropic.claude-opus-5-5", "supported", ""},
		{"official unknown model", "official", "anthropic.claude-opus-50", "unknown", ""},
		{"strict requires allow", "strict", "anthropic.claude-opus-5-5", "unknown", ""},
		{"strict allow works without redis", "strict", "anthropic.claude-opus-5-5", "supported", "allow"},
		{"legacy deny wins", "legacy", "anthropic.claude-opus-5-5", "unsupported", "deny"},
		{"official deny wins", "official", "global.anthropic.claude-opus-5-5", "unsupported", "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settings model_setting.Claude5CompatSettings
			require.NoError(t, common.Unmarshal([]byte(`{"binding_capability_policy":"`+tc.policy+`"}`), &settings))
			if tc.override != "" {
				settings.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: tc.model, Region: "us-east-1", Beta: claude5CompatBetaName, Status: tc.override}}
			}
			require.Equal(t, tc.want, claude5BetaCapability(settings, tc.model, "us-east-1"))
		})
	}
}

func TestClaudeCompatibilityMappedAwsPayloadPreservesFormatAndHistory(t *testing.T) {
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"global.anthropic.claude-opus-5-5"}, ThinkingEnabledMap: true,
		ThinkingBudgetEffortTable: []model_setting.ThinkingBudgetEffort{{MaxBudget: 2048, Effort: "medium"}}}
	const body = `{"model":"client-alias","max_tokens":4096,"system":[{"type":"text","text":"Keep stable","cache_control":{"type":"ephemeral","ttl":"1h"}}],"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"format":{"type":"json_schema","schema":{"type":"object","additionalProperties":false}}},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}]}`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			info := newAwsTestRelayInfo()
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAws}
			info.UpstreamModelName = "global.anthropic.claude-opus-5-5"
			info.ApiKey = "test-access|test-secret|us-east-1"
			info.IsStream, info.AwsPassthroughCompatibility = stream, true
			c := newAwsTestContext(httptest.NewRecorder(), t.Context())
			a := &Adaptor{}
			_, err := doAwsClientRequest(c, info, a, strings.NewReader(body))
			require.NoError(t, err)
			var got []byte
			if stream {
				got = a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput).Body
			} else {
				got = a.AwsReq.(*bedrockruntime.InvokeModelInput).Body
			}
			var original, payload map[string]claude5JSON
			require.NoError(t, common.Unmarshal([]byte(body), &original))
			require.NoError(t, common.Unmarshal(got, &payload))
			require.JSONEq(t, `{"type":"adaptive"}`, string(payload["thinking"]))
			require.JSONEq(t, `{"effort":"medium","format":{"type":"json_schema","schema":{"type":"object","additionalProperties":false}}}`, string(payload["output_config"]))
			require.Equal(t, original["system"], payload["system"])
			require.JSONEq(t, string(original["messages"]), string(payload["messages"]), "existing request preparation may reorder object keys; history and tool pairings must be preserved")
			require.Equal(t, original["max_tokens"], payload["max_tokens"])
		})
	}
}

func TestClaudeBindingAliasAndOfflineRejection(t *testing.T) {
	var request map[string]claude5JSON
	require.NoError(t, common.Unmarshal([]byte(`{"thinking":{"type":"adaptive","block_binding":{"mismatch_behavior":"error"}},"anthropic_beta":["existing"]}`), &request))
	changed, injected, err := addThinkingBindingRecovery(request, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, injected)
	require.JSONEq(t, `{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, string(request["thinking"]))
	changed, injected, err = addThinkingBindingRecovery(request, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.False(t, injected)

	previousEnabled, previousRDB := common.RedisEnabled, common.RDB
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousRDB })
	common.RedisEnabled, common.RDB = false, nil
	settings := model_setting.Claude5CompatSettings{CapabilityTableVersion: t.Name()}
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	require.Equal(t, "supported", claude5BetaCapabilityForRequest(c, settings, "anthropic.claude-opus-5", "us-east-1"))
	c.Set(claude5CompatBetaContextKey, true)
	c.Set(claude5CompatModelContextKey, "anthropic.claude-opus-5")
	c.Set(claude5CompatRegionContextKey, "us-east-1")
	recordClaude5BetaRejection(c, &smithy.GenericAPIError{Code: "ValidationException", Message: "Unsupported beta: " + claude5CompatBetaName})
	require.Equal(t, "unsupported", claude5BetaCapability(settings, "anthropic.claude-opus-5", "us-east-1"))
	require.Equal(t, "supported", claude5BetaCapability(settings, "anthropic.claude-opus-5", "us-west-2"), "a regional rejection must not disable other regions")
}

func TestClaudeAttributionRequiresAvailableBindingCapability(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	t.Cleanup(func() { global.Claude5Compat = previous })
	for _, mode := range []string{"remove", "forge"} {
		for _, status := range []string{"unknown", "deny"} {
			t.Run(mode+"/"+status, func(t *testing.T) {
				settings := model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"anthropic.claude-opus-5-5"}, AttributionMode: mode, ThinkingBindingRecover: true, BlockBindingWrite: true, BindingCapabilityPolicy: "strict", DropTemperature: true}
				if status == "deny" {
					settings.BetaCapabilities = []model_setting.Claude5BetaCapability{{Model: "anthropic.claude-opus-5-5", Region: "us-east-1", Beta: claude5CompatBetaName, Status: "deny"}}
				}
				global.Claude5Compat = settings
				for _, history := range []string{`[{"role":"user","content":"First turn"}]`, `[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"},{"type":"tool_use","id":"tool-1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok"}]}]`} {
					body := []byte(`{"temperature":0,"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.215.0db; cc_entrypoint=cli;"},{"type":"text","text":"Business instructions","cache_control":{"type":"ephemeral"}}],"thinking":{"type":"adaptive"},"messages":` + history + `}`)
					c := newAwsTestContext(httptest.NewRecorder(), t.Context())
					got, err := applyClaude5Compat(c, nil, "anthropic.claude-opus-5-5", "us-east-1", body)
					require.NoError(t, err)
					var original, payload map[string]claude5JSON
					require.NoError(t, common.Unmarshal(body, &original))
					require.NoError(t, common.Unmarshal(got, &payload))
					require.Equal(t, original["system"], payload["system"], "do not edit the signed prefix if recovery cannot run")
					require.Equal(t, original["messages"], payload["messages"])
					require.NotContains(t, payload, "temperature", "independent field cleanup must still run")
					require.NotContains(t, payload, "anthropic_beta")
					require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "diag_attribution_skipped_binding_unavailable")
					global.Claude5Compat.DropTemperature = false
					unchangedContext := newAwsTestContext(httptest.NewRecorder(), t.Context())
					unchanged, err := applyClaude5Compat(unchangedContext, nil, "anthropic.claude-opus-5-5", "us-east-1", body)
					require.NoError(t, err)
					require.Equal(t, body, unchanged, "diagnostics alone must not re-encode the body")
					value, _ := unchangedContext.Get(claude5CompatAuditKey)
					audit := value.(map[string]any)
					require.Equal(t, false, audit["body_changed"])
					require.NotContains(t, audit, "version_change", "a rolled back rewrite must not appear as an applied version change")
					global.Claude5Compat = settings
				}
			})
		}
	}
}

func TestClaudeRedisReadErrorCannotOverrideConcurrentRejection(t *testing.T) {
	useClaude5Redis(t)
	settings := model_setting.Claude5CompatSettings{BindingCapabilityPolicy: "official", CapabilityTableVersion: t.Name()}
	key := claude5BetaCapabilityKey("anthropic.claude-opus-5-5", "us-east-1", settings.CapabilityTableVersion)
	// The network boundary records a rejection from another in-flight request
	// while this request's Redis read fails. No timers or external services.
	client := redis.NewClient(&redis.Options{Addr: "test.invalid:6379", MaxRetries: -1, Dialer: func(context.Context, string, string) (net.Conn, error) {
		setClaude5BetaDenyCache(key, true)
		return nil, fmt.Errorf("test Redis connection failure")
	}})
	t.Cleanup(func() { _ = client.Close() })
	common.RDB = client
	c := newAwsTestContext(httptest.NewRecorder(), t.Context())
	require.Equal(t, "unsupported", claude5BetaCapabilityForRequest(c, settings, "anthropic.claude-opus-5-5", "us-east-1"))
	require.Contains(t, c.GetStringSlice(claude5CompatContextKey), "b1_capability_read_error")
}

func TestClaudeCompatibilityHonorsGlobalBetaBlacklist(t *testing.T) {
	useClaude5Redis(t)
	global := model_setting.GetGlobalSettings()
	previous := global.Claude5Compat
	previousFilter, previousBlacklist := global.AwsBetaFilterEnabled, global.AwsBetaBlacklist
	t.Cleanup(func() {
		global.Claude5Compat = previous
		global.AwsBetaFilterEnabled, global.AwsBetaBlacklist = previousFilter, previousBlacklist
	})
	global.Claude5Compat = model_setting.Claude5CompatSettings{Enabled: true, TargetModels: []string{"global.anthropic.claude-opus-5-5"}, ThinkingBindingRecover: true, BlockBindingWrite: true, BindingCapabilityPolicy: "official"}
	global.AwsBetaBlacklist = []string{" " + claude5CompatBetaName + " "}
	const input = `{"max_tokens":1024,"thinking":{"type":"adaptive"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"opaque"}]}]}`
	for _, enabled := range []bool{true, false} {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("blacklist=%v/stream=%v", enabled, stream), func(t *testing.T) {
				global.AwsBetaFilterEnabled = enabled
				info := newAwsTestRelayInfo()
				info.ChannelMeta = &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAws}
				info.UpstreamModelName = "global.anthropic.claude-opus-5-5"
				info.ApiKey = "test-access|test-secret|us-east-1"
				info.IsStream, info.AwsPassthroughCompatibility = stream, true
				c := newAwsTestContext(httptest.NewRecorder(), t.Context())
				a := &Adaptor{}
				_, err := doAwsClientRequest(c, info, a, strings.NewReader(input))
				require.NoError(t, err)
				var body []byte
				if stream {
					body = a.AwsReq.(*bedrockruntime.InvokeModelWithResponseStreamInput).Body
				} else {
					body = a.AwsReq.(*bedrockruntime.InvokeModelInput).Body
				}
				if enabled {
					require.NotContains(t, string(body), claude5CompatBetaName)
					require.NotContains(t, string(body), "block_binding")
					require.False(t, c.GetBool(claude5CompatBetaContextKey))
				} else {
					require.Contains(t, string(body), claude5CompatBetaName)
					require.Contains(t, string(body), "block_binding")
				}
			})
		}
	}
}
