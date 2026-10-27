/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, test } from 'vitest'

import type { TaskPluginOption } from '../../api'
import {
  CHANNEL_TYPE_NEW_API,
  CHANNEL_TYPE_VLLM,
  CHANNEL_TYPE_SGLANG,
  CHANNEL_TYPE_OPTIONS,
  MODEL_FETCHABLE_TYPES,
} from '../../constants'
import { channelSchema } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  channelFormSchema,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
} from '../channel-form'
import {
  getChannelPluginExtensions,
  readTaskExtendPluginKeys,
  supportsNewAPIUpstream,
} from '../channel-plugin-extensions'
import { getChannelTypeConfig } from '../channel-type-config'
import { getChannelTypeIcon, getKeyPromptForType } from '../channel-utils'

function newAPIForm(baseUrl: string) {
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: 'New API upstream',
    type: CHANNEL_TYPE_NEW_API,
    base_url: baseUrl,
    key: 'test-key',
    models: 'gpt-5',
  }
}

describe('New API channel', () => {
  test('registers selection, ordering, model discovery, and icon metadata', () => {
    const option = CHANNEL_TYPE_OPTIONS.find(
      (item) => item.value === CHANNEL_TYPE_NEW_API
    )

    expect(option).toEqual({
      value: CHANNEL_TYPE_NEW_API,
      label: 'New API',
    })
    expect(
      CHANNEL_TYPE_OPTIONS.findIndex(
        (item) => item.value === CHANNEL_TYPE_NEW_API
      ) + 1
    ).toBe(CHANNEL_TYPE_OPTIONS.findIndex((item) => item.value === 58))
    expect(MODEL_FETCHABLE_TYPES.has(CHANNEL_TYPE_NEW_API)).toBe(true)
    expect(getChannelTypeIcon(CHANNEL_TYPE_NEW_API)).toBe('NewAPI')
    expect(getKeyPromptForType(CHANNEL_TYPE_NEW_API)).toBe(
      'Enter API key for this channel'
    )
    expect(getChannelTypeConfig(CHANNEL_TYPE_NEW_API).icon).toBe('NewAPI')
  })

  test('requires a non-blank Base URL', () => {
    const blankResult = channelFormSchema.safeParse(newAPIForm('  '))

    expect(blankResult.success).toBe(false)
    if (!blankResult.success) {
      expect(
        blankResult.error.issues.some(
          (issue) =>
            issue.path[0] === 'base_url' &&
            issue.message === 'Base URL is required for this channel type'
        )
      ).toBe(true)
    }

    expect(
      channelFormSchema.safeParse(newAPIForm('https://new-api.example')).success
    ).toBe(true)
  })

  test('keeps Sub2API Base URL validation unchanged', () => {
    const result = channelFormSchema.safeParse({
      ...newAPIForm(''),
      type: 59,
    })

    expect(result.success).toBe(true)
  })
})

describe('Bedrock protection channel settings', () => {
  test('defaults every runtime switch to off', () => {
    expect(CHANNEL_FORM_DEFAULT_VALUES).toMatchObject({
      bedrock_rate_limit_enabled: false,
      bedrock_cooldown_enabled: false,
      bedrock_model_rate_limit_enabled: false,
      bedrock_ramp_up_enabled: false,
      bedrock_retry_backoff_enabled: false,
    })
  })

  test('round-trips Bedrock protection settings through the channel setting JSON', () => {
    const setting = buildSettingJSON({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: 33,
      bedrock_rate_limit_enabled: true,
      bedrock_rpm: 120,
      bedrock_tpm: 24000,
      bedrock_cooldown_enabled: true,
      bedrock_429_threshold: 4,
      bedrock_observation_window_seconds: 90,
      bedrock_minimum_429_span_seconds: 3,
      bedrock_minimum_sample_size: 30,
      bedrock_throttling_ratio_percent: 60,
      bedrock_cooldown_seconds: 600,
      bedrock_model_rate_limit_enabled: true,
      bedrock_model_limits: '{"opus":{"rpm":30,"tpm":12000}}',
      bedrock_ramp_up_enabled: true,
      bedrock_ramp_up_start_unix: 1800000000,
      bedrock_ramp_up_days: 7,
      bedrock_ramp_up_initial_percent: 5,
      bedrock_retry_backoff_enabled: true,
      bedrock_retry_backoff_base_ms: 100,
      bedrock_retry_backoff_max_ms: 1000,
    })
    const defaults = transformChannelToFormDefaults(
      channelSchema.parse({
        id: 33,
        name: 'Bedrock',
        type: 33,
        key: '',
        status: 1,
        models: 'claude',
        group: 'default',
        created_time: 0,
        test_time: 0,
        response_time: 0,
        balance_updated_time: 0,
        setting,
        settings: '{}',
        channel_info: {},
      })
    )

    expect(defaults).toMatchObject({
      bedrock_rate_limit_enabled: true,
      bedrock_rpm: 120,
      bedrock_tpm: 24000,
      bedrock_cooldown_enabled: true,
      bedrock_429_threshold: 4,
      bedrock_observation_window_seconds: 90,
      bedrock_minimum_429_span_seconds: 3,
      bedrock_minimum_sample_size: 30,
      bedrock_throttling_ratio_percent: 60,
      bedrock_cooldown_seconds: 600,
      bedrock_model_rate_limit_enabled: true,
      bedrock_model_limits: JSON.stringify(
        { opus: { rpm: 30, tpm: 12000 } },
        null,
        2
      ),
      bedrock_ramp_up_enabled: true,
      bedrock_ramp_up_start_unix: 1800000000,
      bedrock_ramp_up_days: 7,
      bedrock_ramp_up_initial_percent: 5,
      bedrock_retry_backoff_enabled: true,
      bedrock_retry_backoff_base_ms: 100,
      bedrock_retry_backoff_max_ms: 1000,
    })
  })

  test.each([
    { bedrock_model_limits: '{"opus":{"rpm":-1}}' },
    { bedrock_model_limits: '{"opus":{"tpm":1.5}}' },
    { bedrock_model_limits: '{"opus":10}' },
    { bedrock_ramp_up_days: 8 },
    { bedrock_ramp_up_enabled: true },
    { bedrock_retry_backoff_base_ms: 1000, bedrock_retry_backoff_max_ms: 100 },
  ])('rejects invalid Bedrock configuration %j', (invalid) => {
    expect(
      channelFormSchema.safeParse({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        type: 33,
        name: 'Bedrock',
        key: 'key',
        ...invalid,
      }).success
    ).toBe(false)
  })

  test('does not save Bedrock protection settings for other channel types', () => {
    const setting = JSON.parse(
      buildSettingJSON({
        ...CHANNEL_FORM_DEFAULT_VALUES,
        type: 1,
        bedrock_rate_limit_enabled: true,
        bedrock_cooldown_enabled: true,
      })
    )

    expect(setting).not.toHaveProperty('bedrock_protection')
  })
})

describe.each([
  { type: CHANNEL_TYPE_VLLM, name: 'vLLM', icon: 'Vllm' },
  { type: CHANNEL_TYPE_SGLANG, name: 'SGLang', icon: 'SGLang' },
])('$name channel', ({ type, name, icon }) => {
  test('can be selected and discover served models', () => {
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: type,
      label: name,
    })
    expect(MODEL_FETCHABLE_TYPES.has(type)).toBe(true)
    expect(getChannelTypeIcon(type)).toBe(icon)
    expect(getChannelTypeConfig(type).icon).toBe(icon)
    expect(getKeyPromptForType(type)).toBe(
      `${name} API key, or EMPTY if authentication is disabled`
    )
  })

  test('requires an upstream address and submits the served model name', () => {
    const form = {
      ...newAPIForm(''),
      type,
      models: 'deepseek-v4-flash-vision-exp',
      key: 'EMPTY',
    }
    const blank = channelFormSchema.safeParse(form)
    expect(blank.success).toBe(false)
    if (!blank.success) {
      expect(blank.error.issues).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            path: ['base_url'],
            message: 'Base URL is required for this channel type',
          }),
        ])
      )
    }
    const parsed = channelFormSchema.parse({
      ...form,
      base_url: 'http://vllm:8000/',
    })
    const payload = transformFormDataToCreatePayload(parsed)
    expect(payload.channel).toMatchObject({
      type,
      base_url: 'http://vllm:8000',
      models: 'deepseek-v4-flash-vision-exp',
      key: 'EMPTY',
    })
  })
})

describe('New API channel task plugin extensions', () => {
  const plugins: TaskPluginOption[] = [
    {
      key: 'doubao',
      name: 'Doubao',
      models: ['seedance'],
      channelTypes: [45],
      upstreams: ['vendor', 'new_api'],
    },
    {
      key: 'kling',
      name: 'Kling',
      models: ['kling-v2'],
      upstreams: ['new_api'],
    },
    {
      key: 'acme',
      name: 'Acme',
      models: ['acme-video'],
      upstreams: ['vendor'],
    },
  ]

  test('only plugins declaring a New API upstream can be bound to a New API channel', () => {
    expect(plugins.filter(supportsNewAPIUpstream).map((p) => p.key)).toEqual([
      'doubao',
      'kling',
    ])
    expect(supportsNewAPIUpstream({ upstreams: null })).toBe(false)
    expect(supportsNewAPIUpstream({})).toBe(false)
  })

  test('reads the extension list together with a single key on New API channels only', () => {
    expect(
      readTaskExtendPluginKeys(CHANNEL_TYPE_NEW_API, {
        task_plugin_key: 'kling',
        task_extend_plugin_keys: ['doubao', 'kling', '', 'doubao'],
      })
    ).toEqual(['kling', 'doubao'])
    expect(readTaskExtendPluginKeys(CHANNEL_TYPE_NEW_API, {})).toEqual([])
    expect(
      readTaskExtendPluginKeys(1, { task_extend_plugin_keys: ['doubao'] })
    ).toEqual([])
  })

  test('lists only bound plugins as extensions while other types keep declared channel types', () => {
    expect(
      getChannelPluginExtensions(CHANNEL_TYPE_NEW_API, plugins, ['kling'])
    ).toEqual([plugins[1]])
    expect(getChannelPluginExtensions(CHANNEL_TYPE_NEW_API, plugins)).toEqual(
      []
    )
    expect(getChannelPluginExtensions(45, plugins, ['kling'])).toEqual([
      plugins[0],
    ])
  })

  test('round-trips bound plugins through the channel setting for New API channels only', () => {
    const channel = channelSchema.parse({
      id: 7,
      name: 'Gateway',
      type: CHANNEL_TYPE_NEW_API,
      key: '',
      status: 1,
      created_time: 1,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      models: 'seedance',
      group: 'default',
      base_url: 'https://gateway.example',
      setting: JSON.stringify({
        task_plugin_key: 'kling',
        task_extend_plugin_keys: ['doubao'],
      }),
    })
    expect(
      transformChannelToFormDefaults(channel).task_extend_plugin_keys
    ).toEqual(['kling', 'doubao'])

    const gateway = transformFormDataToCreatePayload(
      channelFormSchema.parse({
        ...newAPIForm('https://gateway.example'),
        task_extend_plugin_keys: ['doubao', 'kling'],
      })
    )
    const gatewaySetting = JSON.parse(gateway.channel.setting ?? '{}')
    expect(gatewaySetting).toMatchObject({
      task_extend_plugin_keys: ['doubao', 'kling'],
    })
    expect(gatewaySetting).not.toHaveProperty('task_plugin_key')

    const openai = transformFormDataToCreatePayload(
      channelFormSchema.parse({
        ...newAPIForm('https://gateway.example'),
        type: 1,
        task_extend_plugin_keys: ['doubao'],
      })
    )
    expect(JSON.parse(openai.channel.setting ?? '{}')).not.toHaveProperty(
      'task_extend_plugin_keys'
    )
  })
})
