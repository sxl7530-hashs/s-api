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
import i18next from 'i18next'
import { beforeAll, describe, expect, test, vi } from 'vitest'

import { ChannelSelectorDialog } from '../channel-selector-dialog'

describe('channel selector sync timeout', () => {
  beforeAll(() => {
    i18next.addResourceBundle('en', 'translation', {
      'Sync timeout': 'Sync timeout',
    })
  })

  test('allows the administrator to select a longer upstream timeout', async () => {
    const onSyncTimeoutChange = vi.fn()
    const user = userEvent.setup()

    render(
      <ChannelSelectorDialog
        open
        onOpenChange={vi.fn()}
        channels={[]}
        selectedChannelIds={[]}
        onSelectedChannelIdsChange={vi.fn()}
        channelEndpoints={{}}
        onChannelEndpointsChange={vi.fn()}
        syncTimeout={30}
        onSyncTimeoutChange={onSyncTimeoutChange}
        onConfirm={vi.fn()}
      />
    )

    await user.click(screen.getByRole('combobox', { name: 'Sync timeout' }))
    await user.click(screen.getByRole('option', { name: 'Sync timeout: 45s' }))

    expect(onSyncTimeoutChange).toHaveBeenCalledWith(45)
  })
})

