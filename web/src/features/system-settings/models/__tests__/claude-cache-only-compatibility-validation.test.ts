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
import { describe, expect, test } from 'vitest'

import { isPositiveIdBooleanMap } from '../claude-cache-only-compatibility'

describe('Claude cache-only compatibility validation', () => {
  test('accepts positive user and API key IDs with boolean overrides', () => {
    expect(isPositiveIdBooleanMap({ 12: true, 34: false })).toBe(true)
    expect(isPositiveIdBooleanMap({})).toBe(true)
  })

  test('rejects non-canonical IDs and non-boolean values', () => {
    expect(isPositiveIdBooleanMap({ 0: true })).toBe(false)
    expect(isPositiveIdBooleanMap({ '01': true })).toBe(false)
    expect(isPositiveIdBooleanMap({ user: true })).toBe(false)
    expect(isPositiveIdBooleanMap({ 12: 'true' })).toBe(false)
    expect(isPositiveIdBooleanMap(null)).toBe(false)
    expect(isPositiveIdBooleanMap([])).toBe(false)
  })
})
