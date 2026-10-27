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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { Channels } from '../../index'
import { CHANNEL_NAME_DISPLAY_STORAGE_KEY } from '../../lib/channel-name-display'
import type { Channel } from '../../types'
import { ChannelsProvider } from '../channels-provider'
import { ChannelsTable } from '../channels-table'

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})
const clients: QueryClient[] = []

function channel(name: string): Channel {
  return {
    id: 3,
    type: 1,
    key: '',
    status: 1,
    name,
    created_time: 0,
    test_time: 0,
    response_time: 0,
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'gpt-4o',
    group: 'vip',
    used_quota: 0,
    other_info: '',
    remark: '',
    max_input_tokens: 0,
    cost_ratio: 1,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '{}',
  }
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

async function renderChannelsPage(
  searchGate: () => Promise<void>,
  fullPage = false,
  usageFails: boolean | (() => boolean) = false,
  tpmUnavailable = false,
  expectedChannelName = 'prod'
) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/channel/test/models/tasks') {
      return { data: { success: true, data: [], total: 0 } }
    }
    if (url === '/api/channel/search') {
      await searchGate()
      return {
        data: {
          success: true,
          data: { items: [channel('prod')], total: 1, type_counts: {} },
        },
      }
    }
    if (url === '/api/channel/usage') {
      if (typeof usageFails === 'function' ? usageFails() : usageFails) {
        return { data: { success: false, message: 'metrics unavailable' } }
      }
      return {
        data: {
          success: true,
          data: {
            '3': {
              rpm: 12,
              tpm: 345,
              available: true,
              tpm_unavailable: tpmUnavailable,
            },
          },
        },
      }
    }
    return { data: { success: true, data: ['vip'] } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const channelsRoute = createRoute({
    getParentRoute: () => auth,
    path: 'channels/',
    component: () =>
      fullPage ? (
        <Channels />
      ) : (
        <ChannelsProvider>
          <ChannelsTable />
        </ChannelsProvider>
      ),
  })
  const initialEntry =
    '/channels/?filter=prod&group=%5B%22vip%22%5D&status=%5B%22enabled%22%5D&model=gpt-4o'
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([channelsRoute])]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  await router.load()
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  expect(
    (await screen.findAllByText(expectedChannelName)).length
  ).toBeGreaterThan(0)
  return get
}

it('refetches the channel list with the current filters and marks Refresh busy while it runs', async () => {
  let gate = Promise.resolve()
  const get = await renderChannelsPage(() => gate)
  const searchCalls = () =>
    get.mock.calls.filter(([url]) => url === '/api/channel/search')
  const refresh = screen.getByRole('button', { name: 'Refresh' })
  expect(searchCalls()).toHaveLength(1)
  await waitFor(() =>
    expect(get.mock.calls.some(([url]) => url === '/api/channel/usage')).toBe(
      true
    )
  )
  expect(refresh).toHaveAttribute('aria-busy', 'false')

  let release!: () => void
  gate = new Promise((resolve) => {
    release = resolve
  })
  refresh.focus()
  await userEvent.setup().keyboard('{Enter}')

  await waitFor(() => expect(searchCalls()).toHaveLength(2))
  expect(searchCalls()[1][1]?.params).toEqual(searchCalls()[0][1]?.params)
  expect(refresh).toHaveAttribute('aria-busy', 'true')
  release()
  await waitFor(() => expect(refresh).toHaveAttribute('aria-busy', 'false'))
  expect(get.mock.calls.some(([url]) => url === '/api/channel/usage')).toBe(
    true
  )
})

it('shows the configured alias in the channel list without changing channel data', async () => {
  localStorage.setItem('channels:view-mode', 'table')
  localStorage.setItem(
    CHANNEL_NAME_DISPLAY_STORAGE_KEY,
    JSON.stringify({ enabled: true, mappings: { prod: 'Supplier 1' } })
  )

  await renderChannelsPage(
    () => Promise.resolve(),
    false,
    false,
    false,
    'Supplier 1'
  )

  expect(await screen.findByText('Supplier 1')).toBeVisible()
  expect(screen.queryByText('prod')).not.toBeInTheDocument()
})

it('migrates legacy RPM visibility to one combined column and refreshes both metrics', async () => {
  localStorage.setItem('channels:view-mode', 'table')
  localStorage.setItem(
    'channels:column-visibility',
    JSON.stringify({ rpm: true, tpm: false })
  )
  const get = await renderChannelsPage(() => Promise.resolve())
  const usageCalls = () =>
    get.mock.calls.filter(([url]) => url === '/api/channel/usage')

  await waitFor(() => expect(usageCalls()).toHaveLength(1))
  expect(usageCalls()[0][1]?.params).toEqual({ ids: '3' })
  expect(await screen.findByText('12')).toBeVisible()
  expect(await screen.findByText('345')).toBeVisible()
  expect(
    screen.getAllByRole('columnheader', { name: /RPM \/ TPM/ })
  ).toHaveLength(1)
  expect((await screen.findAllByText('prod')).length).toBeGreaterThan(0)

  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(usageCalls()).toHaveLength(2))
})

it('an explicitly hidden combined column overrides legacy visibility', async () => {
  localStorage.setItem(
    'channels:column-visibility',
    JSON.stringify({ throughput: false, rpm: true, tpm: true })
  )
  const get = await renderChannelsPage(() => Promise.resolve())
  expect((await screen.findAllByText('prod')).length).toBeGreaterThan(0)
  expect(get.mock.calls.some(([url]) => url === '/api/channel/usage')).toBe(
    false
  )
})

it.each(['null', '[]', '"invalid"', '{broken', '{"throughput":"true"}'])(
  'opens the channel list safely with invalid column preferences: %s',
  async (stored) => {
    localStorage.setItem('channels:column-visibility', stored)
    const get = await renderChannelsPage(() => Promise.resolve())
    expect(get.mock.calls.some(([url]) => url === '/api/channel/usage')).toBe(
      true
    )
  }
)

it('preserves RPM and shows unavailable TPM when token estimation failed', async () => {
  localStorage.setItem('channels:view-mode', 'table')
  localStorage.setItem(
    'channels:column-visibility',
    JSON.stringify({ throughput: true })
  )
  await renderChannelsPage(() => Promise.resolve(), false, false, true)
  expect(await screen.findByText('12')).toBeVisible()
  expect(screen.queryByText('345')).not.toBeInTheDocument()
  expect(screen.getAllByText('—').length).toBeGreaterThan(0)
})

it('keeps the channel list visible when optional usage metrics fail', async () => {
  localStorage.setItem(
    'channels:column-visibility',
    JSON.stringify({ rpm: true })
  )
  const get = await renderChannelsPage(() => Promise.resolve(), false, true)

  await waitFor(() =>
    expect(get.mock.calls.some(([url]) => url === '/api/channel/usage')).toBe(
      true
    )
  )
  expect((await screen.findAllByText('prod')).length).toBeGreaterThan(0)
})

it.each(['business', 'network'])(
  'clears stale metrics after a %s failure and restores them after recovery',
  async (failure) => {
    localStorage.setItem('channels:view-mode', 'table')
    localStorage.setItem(
      'channels:column-visibility',
      JSON.stringify({ throughput: true })
    )
    let unavailable = false
    await renderChannelsPage(
      () => Promise.resolve(),
      false,
      () => {
        if (unavailable && failure === 'network') {
          throw new Error('network unavailable')
        }
        return unavailable
      }
    )
    expect(await screen.findByText('345')).toBeVisible()

    unavailable = true
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(screen.queryByText('345')).not.toBeInTheDocument()
    )
    expect(screen.queryByText('12')).not.toBeInTheDocument()
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(2)
    expect(screen.getAllByText('prod').length).toBeGreaterThan(0)

    unavailable = false
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('345')).toBeVisible()
  }
)

it('opens model test history from the channel toolbar without taking space from the channel table', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const get = await renderChannelsPage(() => Promise.resolve(), true)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    get.mock.calls.some(([url]) => url === '/api/channel/test/models/tasks')
  ).toBe(false)
  await userEvent.click(
    screen.getByRole('button', { name: 'Selected channel model test' })
  )
  expect(await screen.findByRole('dialog')).toBeVisible()
  expect(await screen.findByText('No historical system tasks.')).toBeVisible()
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('button', { name: 'Selected channel model test' })
  ).toHaveFocus()
})
