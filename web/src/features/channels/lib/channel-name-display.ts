export const CHANNEL_NAME_DISPLAY_STORAGE_KEY = 'channels:name-display'

export type ChannelNameDisplaySettings = {
  enabled: boolean
  mappings: Record<string, string>
}

const EMPTY_SETTINGS: ChannelNameDisplaySettings = {
  enabled: false,
  mappings: {},
}

export function readChannelNameDisplaySettings(): ChannelNameDisplaySettings {
  try {
    const stored = window.localStorage.getItem(CHANNEL_NAME_DISPLAY_STORAGE_KEY)
    if (!stored) return EMPTY_SETTINGS
    const value: unknown = JSON.parse(stored)
    if (!value || typeof value !== 'object') return EMPTY_SETTINGS
    const settings = value as Partial<ChannelNameDisplaySettings>
    const mappings = settings.mappings
    return {
      enabled: settings.enabled === true,
      mappings:
        mappings && typeof mappings === 'object'
          ? Object.fromEntries(
              Object.entries(mappings).filter(
                ([source, display]) =>
                  source.length > 0 && typeof display === 'string'
              )
            )
          : {},
    }
  } catch {
    return EMPTY_SETTINGS
  }
}

export function writeChannelNameDisplaySettings(
  settings: ChannelNameDisplaySettings
): void {
  try {
    window.localStorage.setItem(
      CHANNEL_NAME_DISPLAY_STORAGE_KEY,
      JSON.stringify(settings)
    )
  } catch {
    // Display preferences are optional and must not affect channel operations.
  }
  window.dispatchEvent(new Event('channel-name-display-change'))
}

export function getDisplayedChannelName(
  name: string | null | undefined,
  settings: ChannelNameDisplaySettings
): string {
  if (!name || !settings.enabled) return name ?? ''

  const sources = Object.keys(settings.mappings)
    .filter((source) => source.length > 0)
    .sort((left, right) => right.length - left.length)
  if (sources.length === 0) return name

  const pattern = new RegExp(
    sources
      .map((source) => source.replaceAll(/[.*+?^${}()|[\]\\]/g, '\\$&'))
      .join('|'),
    'g'
  )
  return name.replaceAll(pattern, (match) => settings.mappings[match] ?? match)
}
