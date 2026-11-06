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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { getClaude5CompatStats, type Claude5CompatStats } from '../../api'
import { Claude5CompatSettings } from '../claude5-compat-settings'
import { Claude5CompatStatistics } from '../claude5-compat-statistics'

vi.mock('../../api', () => ({ getClaude5CompatStats: vi.fn() }))
afterEach(() => vi.mocked(getClaude5CompatStats).mockReset())

const empty: Claude5CompatStats = {
  start: 1000,
  end: 4600,
  scanned: 0,
  partial: false,
  requests: 0,
  rows: [],
  buckets: [{ start: 1000, requests: 0 }],
}
function renderStatistics(settings = false) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      {settings ? (
        <Claude5CompatSettings value='{}' onChange={() => {}} />
      ) : (
        <Claude5CompatStatistics />
      )}
    </QueryClientProvider>
  )
}

describe('Claude 5 compatibility statistics', () => {
  test('keeps statistics collapsed and fetches only on manual refresh', async () => {
    renderStatistics(true)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: /AWS Claude 5 Compatibility/ })
    )
    const trigger = screen.getByRole('button', {
      name: 'Compatibility rule-hit statistics',
    })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await user.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(
      screen.getByRole('button', { name: 'Load rule-hit statistics' })
    ).toBeVisible()
    expect(getClaude5CompatStats).not.toHaveBeenCalled()
  })
  test('shows server counts, trend buckets and an explicit partial warning', async () => {
    vi.mocked(getClaude5CompatStats).mockResolvedValue({
      success: true,
      data: {
        ...empty,
        scanned: 10000,
        partial: true,
        requests: 2,
        rows: [
          { model: 'target-model', rule: 'temperature_dropped', requests: 2 },
        ],
        buckets: [
          { start: 1000, requests: 1 },
          { start: 1300, requests: 1 },
        ],
      },
    })
    renderStatistics()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Load rule-hit statistics' }))
    expect(await screen.findByText('target-model')).toBeVisible()
    expect(screen.getByText('temperature_dropped')).toBeVisible()
    expect(
      screen.getByText('Scanned logs: 10,000; unique rule-hit requests: 2')
    ).toBeVisible()
    expect(screen.getByRole('status')).toHaveTextContent('Scan limit reached')
    expect(screen.getByText('Five-minute interval')).toBeVisible()
  })
  test('shows an empty completed query without a partial warning', async () => {
    vi.mocked(getClaude5CompatStats).mockResolvedValue({
      success: true,
      data: empty,
    })
    renderStatistics()
    expect(getClaude5CompatStats).not.toHaveBeenCalled()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Load rule-hit statistics' }))
    expect(await screen.findByText('No rule hits in this window')).toBeVisible()
    expect(screen.queryByText(/Scan limit reached/)).not.toBeInTheDocument()
    expect(getClaude5CompatStats).toHaveBeenCalledTimes(1)
    expect(vi.mocked(getClaude5CompatStats).mock.calls[0]?.[0]).toBeInstanceOf(
      AbortSignal
    )
  })
  test('disables duplicate loads and handles failure with explicit retry', async () => {
    let resolve!: (
      value: Awaited<ReturnType<typeof getClaude5CompatStats>>
    ) => void
    vi.mocked(getClaude5CompatStats).mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done
        })
    )
    renderStatistics()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Load rule-hit statistics' })
    )
    expect(
      screen.getByRole('button', { name: 'Load rule-hit statistics' })
    ).toBeDisabled()
    resolve({ success: false, message: 'Denied' })
    expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
    expect(getClaude5CompatStats).toHaveBeenCalledTimes(1)
    vi.mocked(getClaude5CompatStats).mockResolvedValueOnce({
      success: true,
      data: empty,
    })
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() =>
      expect(screen.getByText('No rule hits in this window')).toBeVisible()
    )
    expect(getClaude5CompatStats).toHaveBeenCalledTimes(2)
  })
  test('aborts a pending statistics request when the panel unmounts', async () => {
    vi.mocked(getClaude5CompatStats).mockImplementationOnce(
      () => new Promise(() => {})
    )
    const view = renderStatistics()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: 'Load rule-hit statistics' }))
    const signal = vi.mocked(getClaude5CompatStats).mock.calls[0]?.[0]
    expect(signal?.aborted).toBe(false)
    view.unmount()
    expect(signal?.aborted).toBe(true)
  })
})
