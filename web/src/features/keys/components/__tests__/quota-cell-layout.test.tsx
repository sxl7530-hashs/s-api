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
import { describe, expect, test } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'

import { ApiKeyQuotaCell } from '../api-keys-cells'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  nsSeparator: false,
  resources: {
    en: {
      translation: {
        'Remaining:': 'Remaining:',
        'Total:': 'Total:',
        'Used:': 'Used:',
      },
    },
  },
})

describe('API key quota table cell layout', () => {
  test('keeps large quota labels inside the cell while preserving exact amounts for assistive technology', () => {
    render(
      <I18nextProvider i18n={i18n}>
        <TooltipProvider>
          <ApiKeyQuotaCell
            used={5_520_000}
            remaining={499_999_994_480_000}
            total={500_000_000_000_000}
          />
        </TooltipProvider>
      </I18nextProvider>
    )

    const cell = screen.getByTestId('api-key-quota-cell')
    expect(cell).toHaveClass('w-full', 'min-w-0', 'overflow-hidden')

    const labels = cell.querySelectorAll('[data-quota-compact-label]')
    expect(labels).toHaveLength(2)
    for (const label of labels) {
      expect(label).toHaveClass('min-w-0', 'truncate')
    }

    expect(cell).toHaveAccessibleName(
      'Remaining: $999,999,988.96; Total: $1,000,000,000; Used: $11.04'
    )
    expect(cell).toHaveTextContent('$1B')
    expect(cell).not.toHaveTextContent('$1,000,000,000')
  })
})
