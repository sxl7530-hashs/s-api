import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, test } from 'vitest'

import { Claude5CompatSettings } from '../claude5-compat-settings'

function SettingsFixture(props: { initial?: string }) {
  const [value, setValue] = useState(props.initial ?? '{}')
  return (
    <>
      <Claude5CompatSettings value={value} onChange={setValue} />
      <output data-testid='saved-settings'>{value}</output>
    </>
  )
}
function savedSettings(): Record<string, unknown> {
  return JSON.parse(screen.getByTestId('saved-settings').textContent || '{}')
}

describe('Bedrock compatibility settings', () => {
  test('starts disabled and collapsed and preserves unrelated options when toggling', async () => {
    render(<SettingsFixture initial='{"future_option":"preserve"}' />)
    const user = userEvent.setup()
    expect(
      screen.getByRole('switch', { name: 'Enable Claude 5 compatibility' })
    ).not.toBeChecked()
    const trigger = screen.getByRole('button', {
      name: /AWS Claude 5 Compatibility/,
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await user.click(
      screen.getByRole('switch', { name: 'Enable Claude 5 compatibility' })
    )
    expect(savedSettings()).toEqual({
      future_option: 'preserve',
      enabled: true,
    })
    await user.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(
      screen.getByRole('button', { name: 'Thinking compatibility' })
    ).toHaveAttribute('aria-expanded', 'false')
  })
  test('edits scope without enabling direct passthrough compatibility', async () => {
    render(<SettingsFixture />)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
    )
    await user.click(
      screen.getByRole('button', { name: 'Scope and direct passthrough' })
    )
    fireEvent.change(screen.getByLabelText('Target Bedrock model patterns'), {
      target: { value: '*claude-opus-5*,target' },
    })
    expect(savedSettings().target_models).toEqual(['*claude-opus-5*', 'target'])
    expect(
      screen.getByRole('switch', {
        name: 'Apply to AWS direct passthrough users',
      })
    ).not.toBeChecked()
    await user.click(
      screen.getByRole('switch', {
        name: 'Apply to AWS direct passthrough users',
      })
    )
    expect(savedSettings().apply_to_passthrough_users).toBe(true)
  })
  test('exposes version warnings and independently configures thinking budget mappings', async () => {
    render(<SettingsFixture />)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
    )
    await user.click(
      screen.getByRole('button', { name: 'Version attribution' })
    )
    await user.selectOptions(screen.getByLabelText('Attribution mode'), 'forge')
    expect(
      screen.getByText(/Declares a different client version/)
    ).toBeVisible()
    expect(savedSettings().attribution_mode).toBe('forge')
    await user.click(
      screen.getByRole('button', { name: 'Thinking compatibility' })
    )
    await user.click(screen.getByRole('button', { name: 'Add budget mapping' }))
    expect(savedSettings().thinking_budget_effort_table).toEqual([
      { max_budget: 8000, effort: 'medium' },
    ])
    await user.click(
      screen.getByRole('switch', { name: 'Recover thinking block bindings' })
    )
    expect(savedSettings().thinking_binding_recover).toBe(true)
    expect(
      screen.getByRole('switch', { name: 'Map enabled thinking to adaptive' })
    ).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    expect(savedSettings().thinking_budget_effort_table).toEqual([])
  })
  test('keeps experimental binding writes off and invalidates temporary denials only through saved settings', async () => {
    render(
      <SettingsFixture initial='{"beta_capabilities":[{"model":"target","region":"us-east-1","beta":"thinking-binding-controls-2026-08-01","status":"allow"}]}' />
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
    )
    await user.click(
      screen.getByRole('button', { name: 'Thinking compatibility' })
    )
    expect(
      screen.getByRole('switch', {
        name: 'Write thinking block binding control',
      })
    ).not.toBeChecked()
    await user.click(
      screen.getByRole('button', {
        name: 'Clear temporary beta denials on save',
      })
    )
    expect(savedSettings().capability_table_version).toEqual(expect.any(String))
    expect(savedSettings().beta_capabilities).toEqual([
      {
        model: 'target',
        region: 'us-east-1',
        beta: 'thinking-binding-controls-2026-08-01',
        status: 'allow',
      },
    ])
    expect(savedSettings().enabled).toBeUndefined()
    expect(savedSettings().block_binding_write).toBeUndefined()
  })
  test('edits output policy independently without silently enabling thinking conversion', async () => {
    render(<SettingsFixture />)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
    )
    await user.click(
      screen.getByRole('button', { name: 'Request field compatibility' })
    )
    await user.selectOptions(
      screen.getByLabelText('Output config policy'),
      'keep_effort_only'
    )
    expect(savedSettings().output_config_policy).toBe('keep_effort_only')
    expect(savedSettings().thinking_enabled_map).toBeUndefined()
  })
})

test('edits rollout sampling and verified capabilities without enabling compatibility', async () => {
  render(
    <SettingsFixture initial='{"beta_capabilities":[{"model":"target","region":"us-east-1","status":"allow","beta":"thinking-binding-controls-2026-08-01","future":"kept"}]}' />
  )
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
  )
  await user.click(
    screen.getByRole('button', { name: 'Rollout and audit sampling' })
  )
  expect(screen.getByLabelText('Unmodified control group (%)')).toHaveValue(0)
  expect(screen.getByLabelText('Audit sample (%)')).toHaveValue(100)
  fireEvent.change(screen.getByLabelText('Unmodified control group (%)'), {
    target: { value: '20' },
  })
  fireEvent.change(screen.getByLabelText('Audit sample (%)'), {
    target: { value: '25' },
  })
  expect(savedSettings().control_percent).toBe(20)
  expect(savedSettings().audit_sample_percent).toBe(25)
  await user.click(
    screen.getByRole('button', { name: 'Verified beta capabilities' })
  )
  fireEvent.change(screen.getByLabelText('Model 1'), {
    target: { value: 'updated-model' },
  })
  await user.selectOptions(screen.getByLabelText('Status 1'), 'deny')
  expect(savedSettings().beta_capabilities).toEqual([
    expect.objectContaining({
      model: 'updated-model',
      status: 'deny',
      future: 'kept',
    }),
  ])
  await user.click(screen.getByRole('button', { name: 'Add beta capability' }))
  expect(savedSettings().beta_capabilities).toHaveLength(2)
  await user.click(
    screen.getAllByRole('button', { name: 'Delete' }).at(1) as HTMLElement
  )
  expect(savedSettings().beta_capabilities).toHaveLength(1)
  expect(savedSettings().enabled).toBeUndefined()
})

test('records fingerprint rejection with a warning without changing forge policy', async () => {
  render(<SettingsFixture />)
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
  )
  await user.click(
    screen.getByRole('button', { name: 'Fingerprint review results' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Add fingerprint review' })
  )
  fireEvent.change(screen.getByLabelText('Model 1'), {
    target: { value: 'target' },
  })
  fireEvent.change(screen.getByLabelText('Region 1'), {
    target: { value: 'us-east-1' },
  })
  await user.selectOptions(
    screen.getByLabelText('Status 1'),
    'preserve_rejected'
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Fingerprint preservation was rejected'
  )
  expect(savedSettings().fingerprint_statuses).toEqual([
    { model: 'target', region: 'us-east-1', status: 'preserve_rejected' },
  ])
  expect(savedSettings().fingerprint_policy).toBeUndefined()
  expect(savedSettings().enabled).toBeUndefined()
})
