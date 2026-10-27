import { useCallback, useEffect, useState } from 'react'

import {
  readChannelNameDisplaySettings,
  writeChannelNameDisplaySettings,
  type ChannelNameDisplaySettings,
} from '../lib/channel-name-display'

export function useChannelNameDisplay() {
  const [settings, setSettings] = useState(readChannelNameDisplaySettings)

  useEffect(() => {
    const refresh = () => setSettings(readChannelNameDisplaySettings())
    window.addEventListener('storage', refresh)
    window.addEventListener('channel-name-display-change', refresh)
    return () => {
      window.removeEventListener('storage', refresh)
      window.removeEventListener('channel-name-display-change', refresh)
    }
  }, [])

  const updateSettings = useCallback((next: ChannelNameDisplaySettings) => {
    writeChannelNameDisplaySettings(next)
    setSettings(next)
  }, [])

  return { settings, updateSettings }
}
