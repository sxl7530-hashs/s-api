/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

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
import { describe, expect, test, vi } from 'vitest'

import { LegalConsent } from '../legal-consent'

describe('LegalConsent', () => {
  test('exposes a prominent consent control that toggles its checked state', async () => {
    const user = userEvent.setup()
    const onCheckedChange = vi.fn()

    const { rerender } = render(
      <LegalConsent
        status={{ user_agreement_enabled: true }}
        checked={false}
        onCheckedChange={onCheckedChange}
      />
    )

    const card = document.querySelector('[data-consent-checked]')
    const checkbox = screen.getByRole('checkbox')

    expect(card).toHaveClass('legal-consent-card')
    expect(card).toHaveAttribute('data-consent-checked', 'false')
    expect(checkbox).toHaveAccessibleName(/I have read and agree to the/i)
    expect(screen.getByRole('link', { name: 'User Agreement' })).toBeVisible()

    await user.click(checkbox)
    expect(onCheckedChange).toHaveBeenCalledWith(true)

    rerender(
      <LegalConsent
        status={{ user_agreement_enabled: true }}
        checked
        onCheckedChange={onCheckedChange}
      />
    )

    expect(card).toHaveAttribute('data-consent-checked', 'true')
  })
})
