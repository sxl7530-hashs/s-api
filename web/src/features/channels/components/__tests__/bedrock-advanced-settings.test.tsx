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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import { previewBedrockQuotas } from '../../api'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { BedrockAdvancedSettings } from '../bedrock-advanced-settings'

vi.mock('../../api', () => ({ previewBedrockQuotas: vi.fn() }))
const quotaResponse = {
  success: true,
  data: {
    region: 'us-east-1',
    models: [
      {
        model: 'claude-opus-4-6',
        target: 'global.anthropic.claude-opus-4-6-v1',
        status: 'matched',
        rpm: 120,
        tpm: 24000,
      },
    ],
  },
}

function SettingsForm(props: {
  disabled?: boolean
  defaults?: Partial<ChannelFormValues>
  onSave: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: 33,
      models: 'claude-opus-4-6,claude-sonnet-4-5',
      ...props.defaults,
    },
  })
  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit((values) =>
          props.onSave(buildSettingJSON(values))
        )}
      >
        <BedrockAdvancedSettings
          form={form}
          disabled={props.disabled === true}
        />
        <Button type='submit'>Save</Button>
      </form>
    </Form>
  )
}

describe('Bedrock advanced protection settings', () => {
  beforeEach(() => {
    vi.mocked(previewBedrockQuotas).mockReset()
  })
  test('switches default off and independently reveal their configuration', async () => {
    const user = userEvent.setup()
    const saved: string[] = []
    render(<SettingsForm onSave={(value) => saved.push(value)} />)
    expect(screen.getAllByRole('switch')).toHaveLength(3)
    for (const toggle of screen.getAllByRole('switch')) {
      expect(toggle).not.toBeChecked()
    }
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()
    expect(previewBedrockQuotas).not.toHaveBeenCalled()

    const ramp = screen.getByRole('switch', { name: 'Bedrock gradual ramp-up' })
    ramp.focus()
    await user.keyboard(' ')
    expect(
      screen.getByRole('spinbutton', { name: 'Ramp-up days (1–7)' })
    ).toHaveValue(7)
    expect(
      screen.queryByRole('spinbutton', { name: 'Base backoff (ms)' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    const initial = JSON.parse(saved[0]).bedrock_protection
    expect(initial.ramp_up_enabled).toBe(true)
    expect(initial.ramp_up_start_unix).toBeGreaterThan(0)
    expect(initial.retry_backoff_enabled).toBe(false)

    await user.click(
      screen.getByRole('switch', { name: 'Bedrock randomized retry backoff' })
    )
    expect(
      screen.getByRole('spinbutton', { name: 'Base backoff (ms)' })
    ).toHaveValue(200)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(JSON.parse(saved[1]).bedrock_protection.ramp_up_start_unix).toBe(
      initial.ramp_up_start_unix
    )
    expect(JSON.parse(saved[1]).bedrock_protection.retry_backoff_enabled).toBe(
      true
    )
    await user.click(ramp)
    expect(
      screen.queryByRole('spinbutton', { name: 'Ramp-up days (1–7)' })
    ).not.toBeInTheDocument()
  })

  test('selects unique channel models and saves RPM and TPM in separate fields', async () => {
    const user = userEvent.setup()
    const saved: string[] = []
    render(<SettingsForm onSave={(value) => saved.push(value)} />)

    await user.click(
      screen.getByRole('switch', { name: 'Bedrock model rate limits' })
    )
    await user.click(screen.getByRole('button', { name: 'Add Row' }))
    const selectors = screen.getAllByRole('combobox', { name: 'Model' })
    await user.click(selectors[0])
    await user.click(screen.getByRole('option', { name: 'claude-opus-4-6' }))

    await user.type(
      screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' }),
      '30'
    )
    await user.type(
      screen.getByRole('spinbutton', { name: 'TPM claude-opus-4-6' }),
      '60000'
    )

    await user.click(screen.getByRole('button', { name: 'Add Row' }))
    const updatedSelectors = screen.getAllByRole('combobox', { name: 'Model' })
    await user.click(updatedSelectors[1])
    expect(
      screen.queryByRole('option', { name: 'claude-opus-4-6' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'claude-sonnet-4-5' }))
    await user.type(
      screen.getByRole('spinbutton', { name: 'RPM claude-sonnet-4-5' }),
      '12'
    )
    await user.type(
      screen.getByRole('spinbutton', { name: 'TPM claude-sonnet-4-5' }),
      '24000'
    )
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(JSON.parse(saved[0]).bedrock_protection.model_limits).toEqual({
      'claude-opus-4-6': { rpm: 30, tpm: 60000 },
      'claude-sonnet-4-5': { rpm: 12, tpm: 24000 },
    })
  })

  test('locked settings cannot enable protection', async () => {
    const user = userEvent.setup()
    render(<SettingsForm disabled onSave={() => undefined} />)
    for (const toggle of screen.getAllByRole('switch')) {
      expect(toggle).toHaveAttribute('aria-disabled', 'true')
      await user.click(toggle)
      expect(toggle).not.toBeChecked()
    }
  })
  test('imports quotas through draft proxy, keeps manual edits on refresh and restores explicitly', async () => {
    vi.mocked(previewBedrockQuotas).mockResolvedValue(quotaResponse)
    const user = userEvent.setup()
    const saved: string[] = []
    render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          proxy: 'http://draft-proxy:8080',
          key: 'ak|sk|us-east-1',
          model_mapping:
            '{"claude-opus-4-6":"global.anthropic.claude-opus-4-6-v1"}',
        }}
        onSave={(value) => saved.push(value)}
      />
    )
    await user.click(screen.getByRole('button', { name: 'Add Row' }))
    await user.click(screen.getByRole('combobox', { name: 'Model' }))
    await user.click(screen.getByRole('option', { name: 'claude-opus-4-6' }))
    await waitFor(() =>
      expect(
        screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
      ).toHaveValue(120)
    )
    expect(previewBedrockQuotas).toHaveBeenCalledWith(
      expect.objectContaining({
        proxy: 'http://draft-proxy:8080',
        key: 'ak|sk|us-east-1',
      }),
      expect.any(AbortSignal)
    )
    const rpm = screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
    await user.clear(rpm)
    await user.type(rpm, '35')
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Restore AWS quotas' })
      ).toBeInTheDocument()
    )
    expect(rpm).toHaveValue(35)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      JSON.parse(saved[0]).bedrock_protection.model_limits['claude-opus-4-6']
    ).toEqual({
      rpm: 35,
      tpm: 24000,
      resolved_model: 'global.anthropic.claude-opus-4-6-v1',
      quota_region: 'us-east-1',
    })
    await user.click(screen.getByRole('button', { name: 'Restore AWS quotas' }))
    expect(rpm).toHaveValue(120)
  })

  test('missing proxy makes no query and still allows manual limits', async () => {
    const user = userEvent.setup()
    render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          key: 'ak|sk|us-east-1',
        }}
        onSave={() => undefined}
      />
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      'Please fill in the channel proxy'
    )
    expect(previewBedrockQuotas).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Add Row' }))
    await user.click(screen.getByRole('combobox', { name: 'Model' }))
    await user.click(screen.getByRole('option', { name: 'claude-opus-4-6' }))
    expect(
      screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
    ).toBeEnabled()
  })

  test('query failure keeps saved limits and unmount cancels pending queries', async () => {
    vi.mocked(previewBedrockQuotas).mockRejectedValue(new Error('AccessDenied'))
    const { unmount } = render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          proxy: 'http://draft-proxy:8080',
          key: 'ak|sk|us-east-1',
          bedrock_model_limits: '{"claude-opus-4-6":{"rpm":30}}',
        }}
        onSave={() => undefined}
      />
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('AccessDenied')
    )
    expect(
      screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
    ).toHaveValue(30)
    const signal = vi.mocked(previewBedrockQuotas).mock.calls[0][1]
    unmount()
    expect(signal?.aborted).toBe(true)
  })
  test('a canceled quota response cannot overwrite a newer configuration', async () => {
    let resolveOld: ((response: typeof quotaResponse) => void) | undefined
    vi.mocked(previewBedrockQuotas).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        })
    )
    const first = render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          proxy: 'http://old-proxy:8080',
          key: 'ak|sk|us-east-1',
        }}
        onSave={() => undefined}
      />
    )
    await waitFor(() => expect(previewBedrockQuotas).toHaveBeenCalledTimes(1))
    const oldSignal = vi.mocked(previewBedrockQuotas).mock.calls[0][1]
    first.unmount()
    expect(oldSignal?.aborted).toBe(true)
    vi.mocked(previewBedrockQuotas).mockResolvedValue({
      ...quotaResponse,
      data: {
        ...quotaResponse.data,
        models: [{ ...quotaResponse.data.models[0], rpm: 60 }],
      },
    })
    render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          proxy: 'http://new-proxy:8080',
          key: 'ak|sk|us-east-1',
        }}
        onSave={() => undefined}
      />
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add Row' }))
    await user.click(screen.getByRole('combobox', { name: 'Model' }))
    await user.click(screen.getByRole('option', { name: 'claude-opus-4-6' }))
    await waitFor(() =>
      expect(
        screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
      ).toHaveValue(60)
    )
    await act(async () => {
      resolveOld?.(quotaResponse)
    })
    expect(
      screen.getByRole('spinbutton', { name: 'RPM claude-opus-4-6' })
    ).toHaveValue(60)
  })
  test.each(['zero_quota', 'unmatched', 'ambiguous'])(
    'does not import %s quotas and allows manual values',
    async (status) => {
      vi.mocked(previewBedrockQuotas).mockResolvedValue({
        ...quotaResponse,
        data: {
          ...quotaResponse.data,
          models: [{ ...quotaResponse.data.models[0], status, rpm: 0 }],
        },
      })
      const user = userEvent.setup()
      const saved: string[] = []
      render(
        <SettingsForm
          defaults={{
            bedrock_model_rate_limit_enabled: true,
            proxy: 'http://proxy:8080',
            key: 'ak|sk|us-east-1',
          }}
          onSave={(value) => saved.push(value)}
        />
      )
      await user.click(screen.getByRole('button', { name: 'Add Row' }))
      await user.click(screen.getByRole('combobox', { name: 'Model' }))
      await user.click(screen.getByRole('option', { name: 'claude-opus-4-6' }))
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
      )
      const rpm = screen.getByRole('spinbutton', {
        name: 'RPM claude-opus-4-6',
      })
      expect(rpm).toHaveValue(null)
      expect(
        screen.getByRole('spinbutton', { name: 'TPM claude-opus-4-6' })
      ).toHaveValue(null)
      expect(
        screen.queryByRole('button', { name: 'Restore AWS quotas' })
      ).not.toBeInTheDocument()
      await user.type(rpm, '25')
      await user.click(screen.getByRole('button', { name: 'Save' }))
      expect(
        JSON.parse(saved[0]).bedrock_protection.model_limits['claude-opus-4-6']
      ).toEqual({ rpm: 25 })
    }
  )

  test('disabling model limits cancels an active query and ignores its response', async () => {
    let finish: ((response: typeof quotaResponse) => void) | undefined
    vi.mocked(previewBedrockQuotas).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    const user = userEvent.setup()
    const saved: string[] = []
    render(
      <SettingsForm
        defaults={{
          bedrock_model_rate_limit_enabled: true,
          proxy: 'http://proxy:8080',
          key: 'ak|sk|us-east-1',
        }}
        onSave={(value) => saved.push(value)}
      />
    )
    await waitFor(() => expect(previewBedrockQuotas).toHaveBeenCalledTimes(1))
    const signal = vi.mocked(previewBedrockQuotas).mock.calls[0][1]
    await user.click(
      screen.getByRole('switch', { name: 'Bedrock model rate limits' })
    )
    expect(signal?.aborted).toBe(true)
    await act(async () => {
      finish?.(quotaResponse)
    })
    expect(
      screen.queryByRole('button', { name: 'Restore AWS quotas' })
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      JSON.parse(saved[0]).bedrock_protection.model_rate_limit_enabled
    ).toBe(false)
    expect(JSON.parse(saved[0]).bedrock_protection.model_limits).toEqual({})
  })
})
