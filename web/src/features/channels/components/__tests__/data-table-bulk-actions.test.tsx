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
import type { Table } from '@tanstack/react-table'
import { fireEvent, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, describe, expect, test } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { useAuthStore } from '@/stores/auth-store'

import type { Channel } from '../../types'
import { DataTableBulkActions } from '../data-table-bulk-actions'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Test all models in selected channels':
          'Test all models in selected channels',
        'Test all selected channel models?':
          'Test all selected channel models?',
        'This tests every model in {{count}} selected channel(s). Models matching automatic disable rules will be removed; channels with no passing models will be disabled.':
          'This tests every model in {{count}} selected channel(s). Models matching automatic disable rules will be removed; channels with no passing models will be disabled.',
        Cancel: 'Cancel',
        'Start testing': 'Start testing',
      },
    },
  },
})

function makeTable(): Table<Channel> {
  return {
    getFilteredSelectedRowModel: () => ({
      rows: [{ original: { id: 11 } }, { original: { id: 22 } }],
    }),
    resetRowSelection: () => undefined,
  } as unknown as Table<Channel>
}

describe('channel bulk actions', () => {
  afterEach(() => useAuthStore.getState().auth.reset())

  test('opens a destructive-behavior confirmation before testing all selected models', () => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'root',
      role: 100,
    })
    const queryClient = new QueryClient()
    render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <TooltipProvider>
            <DataTableBulkActions table={makeTable()} />
          </TooltipProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )

    fireEvent.click(
      screen.getByRole('button', {
        name: 'Test all models in selected channels',
      })
    )

    expect(
      screen.getByRole('heading', {
        name: 'Test all selected channel models?',
      })
    ).toBeInTheDocument()
    expect(
      screen.getByText(/This tests every model in 2 selected channel/)
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Start testing' })).toBeEnabled()
  })
})
