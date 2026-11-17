import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { ChannelNameDisplayDialog } from '../channel-name-display-dialog'

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: { enabled: false, mappings: {} } } })
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})
afterEach(() => {
  cleanup()
  client.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  vi.restoreAllMocks()
})

function renderDialog(onOpenChange = vi.fn()) {
  return render(
    <QueryClientProvider client={client}>
      <ChannelNameDisplayDialog open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )
}

it('saves multiple mappings entered as table rows', async () => {
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  renderDialog(onOpenChange)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled())

  await user.click(
    screen.getByRole('switch', { name: 'Enable display mapping' })
  )
  await user.type(screen.getByRole('textbox', { name: 'Name 1' }), 'sara')
  await user.type(
    screen.getByRole('textbox', { name: 'Display name 1' }),
    'Supplier 1'
  )
  await user.click(screen.getByRole('button', { name: 'Add' }))
  await user.type(screen.getByRole('textbox', { name: 'Name 2' }), 'west')
  await user.type(
    screen.getByRole('textbox', { name: 'Display name 2' }),
    'Supplier 2'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'global.channel_name_display',
    value: JSON.stringify({ enabled: true, mappings: { sara: 'Supplier 1', west: 'Supplier 2' } }),
  }))
  expect(onOpenChange).toHaveBeenCalledWith(false)
})

it('blocks saving a partially completed mapping row', async () => {
  const user = userEvent.setup()
  renderDialog()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled())

  await user.type(screen.getByRole('textbox', { name: 'Name 1' }), 'sara')

  expect(screen.getByRole('alert')).toHaveTextContent(
    'Complete both names or remove the incomplete row.'
  )
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
})

it('preserves unsaved mappings during background refresh and loads the latest settings on reopen', async () => {
  const user = userEvent.setup()
  const view = renderDialog()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled())
  await user.type(screen.getByRole('textbox', { name: 'Name 1' }), 'draft-source')
  await user.type(screen.getByRole('textbox', { name: 'Display name 1' }), 'Draft supplier')
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: { enabled: true, mappings: { remote: 'Remote supplier' } } },
  })
  await act(async () => {
    await client.refetchQueries({ queryKey: ['channels', 'name-display'] })
  })
  expect(client.getQueryData(['channels', 'name-display'])).toEqual({
    enabled: true, mappings: { remote: 'Remote supplier' },
  })
  view.rerender(
    <QueryClientProvider client={client}>
      <ChannelNameDisplayDialog open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  expect(screen.getByRole('textbox', { name: 'Name 1' })).toHaveValue('draft-source')
  expect(screen.getByRole('textbox', { name: 'Display name 1' })).toHaveValue('Draft supplier')
  view.rerender(
    <QueryClientProvider client={client}>
      <ChannelNameDisplayDialog open={false} onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  view.rerender(
    <QueryClientProvider client={client}>
      <ChannelNameDisplayDialog open onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
  expect(await screen.findByRole('textbox', { name: 'Name 1' })).toHaveValue('remote')
  expect(screen.getByRole('textbox', { name: 'Display name 1' })).toHaveValue('Remote supplier')
})

it('keeps the dialog open when the server rejects the mapping save', async () => {
  vi.mocked(api.put).mockResolvedValue({ data: { success: false, message: 'Save failed' } })
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  renderDialog(onOpenChange)
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(api.put).toHaveBeenCalled())
  expect(onOpenChange).not.toHaveBeenCalled()
})

it('keeps shared mapping settings read-only for non-root administrators', async () => {
  useAuthStore.getState().auth.setUser({ id: 2, username: 'admin', role: 10 })
  renderDialog()

  expect(await screen.findByRole('switch', { name: 'Enable display mapping' })).toHaveAttribute('data-disabled', '')
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
})
