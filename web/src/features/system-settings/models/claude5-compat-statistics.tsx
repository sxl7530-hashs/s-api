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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getClaude5CompatStats, type Claude5CompatStats } from '../api'

type RuleHit = Claude5CompatStats['rows'][number]

export function Claude5CompatStatistics() {
  const { t, i18n } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['claude5-compat-statistics', userId],
    queryFn: async ({ signal }) => {
      const response = requireServerSuccess(await getClaude5CompatStats(signal))
      if (!response.data) throw new Error('Failed to query statistics')
      return response.data
    },
    enabled: false,
    retry: false,
    gcTime: 0,
  })
  const columns = useMemo(
    () => [
      { id: 'model', header: t('Model'), cell: (row: RuleHit) => row.model },
      {
        id: 'rule',
        header: t('Compatibility rule'),
        cell: (row: RuleHit) => (
          <code className='text-xs break-all'>{row.rule}</code>
        ),
      },
      {
        id: 'requests',
        header: t('Unique rule-hit requests'),
        cell: (row: RuleHit) => formatNumber(row.requests, locale),
      },
    ],
    [t, locale]
  )

  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Manual statistics from shared logs for the last hour, capped at 10,000 logs and 8 MB. Counts use unique request IDs. Missing logs are excluded; rule hits do not indicate repair success.'
        )}
      </p>
      <Button
        type='button'
        variant='outline'
        disabled={query.isFetching}
        onClick={() => void query.refetch()}
      >
        {t('Load rule-hit statistics')}
      </Button>
      {query.isFetching && <LoadingState size='sm' />}
      {!query.isFetching && query.isError && (
        <ErrorState onRetry={() => void query.refetch()} />
      )}
      {!query.isFetching && !query.isError && query.data && (
        <>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Scanned logs: {{logs}}; unique rule-hit requests: {{requests}}',
              {
                logs: formatNumber(query.data.scanned, locale),
                requests: formatNumber(query.data.requests, locale),
              }
            )}
          </p>
          {query.data.partial && (
            <p role='status' className='text-destructive text-sm'>
              {t(
                'Scan limit reached. These counts cover only the scanned logs.'
              )}
            </p>
          )}
          <StaticDataTable
            data={query.data.rows}
            columns={columns}
            getRowKey={(row) => JSON.stringify([row.model, row.rule])}
            emptyContent={
              <EmptyState
                title={t('No rule hits in this window')}
                className='min-h-24'
              />
            }
          />
          <StaticDataTable
            data={query.data.buckets}
            getRowKey={(row) => row.start}
            columns={[
              {
                id: 'start',
                header: t('Five-minute interval'),
                cell: (row) =>
                  new Date(row.start * 1000).toLocaleTimeString(locale),
              },
              {
                id: 'requests',
                header: t('Unique rule-hit requests'),
                cell: (row) => formatNumber(row.requests, locale),
              },
            ]}
          />
        </>
      )}
    </div>
  )
}
