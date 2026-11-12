import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { ChannelNameDisplaySettings } from '../lib/channel-name-display'

const EMPTY_SETTINGS: ChannelNameDisplaySettings = { enabled: false, mappings: {} }
const QUERY_KEY = ['channels', 'name-display'] as const
export function useChannelNameDisplay() {
  const role = useAuthStore((state) => state.auth.user?.role ?? 0)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: QUERY_KEY,
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data: ChannelNameDisplaySettings
      }>('/api/channel/name-display')
      return requireServerSuccess(response.data).data
    },
    enabled: role >= ROLE.ADMIN,
    staleTime: 60_000,
  })
  const mutation = useMutation({
    mutationFn: async (settings: ChannelNameDisplaySettings) => {
      requireServerSuccess(
        (
          await api.put('/api/option/', {
            key: 'global.channel_name_display',
            value: JSON.stringify(settings),
          })
        ).data
      )
      return settings
    },
    onSuccess: (settings) => {
      queryClient.setQueryData(QUERY_KEY, settings)
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
    },
  })
  return {
    settings: role >= ROLE.ADMIN ? query.data ?? EMPTY_SETTINGS : EMPTY_SETTINGS,
    updateSettings: mutation.mutateAsync,
    isLoading: query.isPending,
    isError: query.isError,
    retryLoad: query.refetch,
    isSaving: mutation.isPending,
    canEdit: role === ROLE.SUPER_ADMIN,
  }
}
