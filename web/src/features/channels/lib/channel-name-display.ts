export type ChannelNameDisplaySettings = {
  enabled: boolean
  mappings: Record<string, string>
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
