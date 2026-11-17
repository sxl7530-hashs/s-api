import { describe, expect, it } from 'vitest'

import { getDisplayedChannelName } from '../channel-name-display'

describe('channel name display mappings', () => {
  it('uses aliases only while the display switch is enabled', () => {
    const settings = { enabled: true, mappings: { sara: 'Supplier 1' } }
    expect(getDisplayedChannelName('sara', settings)).toBe('Supplier 1')
    expect(
      getDisplayedChannelName('AWS-sara-20261023-dfhiowehufej', settings)
    ).toBe('AWS-Supplier 1-20261023-dfhiowehufej')
    expect(
      getDisplayedChannelName('sara', { ...settings, enabled: false })
    ).toBe('sara')
    expect(getDisplayedChannelName('unknown', settings)).toBe('unknown')
    expect(getDisplayedChannelName('', settings)).toBe('')
  })

  it('replaces each matching alias once without cascading into replacement text', () => {
    expect(
      getDisplayedChannelName('sara-sara', {
        enabled: true,
        mappings: { sara: '$Supplier', Supplier: 'Changed' },
      })
    ).toBe('$Supplier-$Supplier')
  })
})
