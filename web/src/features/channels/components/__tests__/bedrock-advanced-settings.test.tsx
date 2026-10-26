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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test } from 'vitest'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  buildSettingJSON,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { BedrockAdvancedSettings } from '../bedrock-advanced-settings'

function SettingsForm(props: {
  disabled?: boolean
  onSave: (value: string) => void
}) {
  const form = useForm<ChannelFormValues>({
    defaultValues: {
      ...CHANNEL_FORM_DEFAULT_VALUES,
      type: 33,
      models: 'claude-opus-4-6,claude-sonnet-4-5',
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
  test('switches default off and independently reveal their configuration', async () => {
    const user = userEvent.setup()
    const saved: string[] = []
    render(<SettingsForm onSave={(value) => saved.push(value)} />)
    expect(screen.getAllByRole('switch')).toHaveLength(3)
    for (const toggle of screen.getAllByRole('switch')) {
      expect(toggle).not.toBeChecked()
    }
    expect(screen.queryByRole('spinbutton')).not.toBeInTheDocument()

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

  test('keeps the custom API URL collapsed until expanded', async () => {
    const user = userEvent.setup()
    render(<SettingsForm onSave={() => undefined} />)

    const trigger = screen.getByRole('button', { name: 'API URL' })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('textbox', { name: 'API URL' })).toBeNull()

    await user.click(trigger)
    const input = screen.getByRole('textbox', { name: 'API URL' })
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    await user.type(input, 'https://bedrock-proxy.example')

    await user.click(trigger)
    expect(screen.queryByRole('textbox', { name: 'API URL' })).toBeNull()
    await user.click(trigger)
    expect(screen.getByRole('textbox', { name: 'API URL' })).toHaveValue(
      'https://bedrock-proxy.example'
    )
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
})
