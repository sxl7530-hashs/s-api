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
})
