import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { CHANNEL_NAME_DISPLAY_STORAGE_KEY } from '../../lib/channel-name-display'
import { ChannelNameDisplayDialog } from '../channel-name-display-dialog'

afterEach(() => localStorage.clear())

it('saves multiple mappings entered as table rows', async () => {
  const user = userEvent.setup()
  const onOpenChange = vi.fn()
  render(<ChannelNameDisplayDialog open onOpenChange={onOpenChange} />)

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

  expect(
    JSON.parse(localStorage.getItem(CHANNEL_NAME_DISPLAY_STORAGE_KEY) || '{}')
  ).toEqual({
    enabled: true,
    mappings: { sara: 'Supplier 1', west: 'Supplier 2' },
  })
  expect(onOpenChange).toHaveBeenCalledWith(false)
})

it('blocks saving a partially completed mapping row', async () => {
  const user = userEvent.setup()
  render(<ChannelNameDisplayDialog open onOpenChange={vi.fn()} />)

  await user.type(screen.getByRole('textbox', { name: 'Name 1' }), 'sara')

  expect(screen.getByRole('alert')).toHaveTextContent(
    'Complete both names or remove the incomplete row.'
  )
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
})
