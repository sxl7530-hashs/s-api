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
          <p className='text-muted-foreground text-xs'>
            {t(
              'Observed logs only. Cohorts may differ in traffic mix; results do not prove causation. Missing upstream, stream or timing observations remain unknown.'
            )}
          </p>
          {(query.data.cohorts?.length ?? 0) > 0 && (
            <StaticDataTable
              data={query.data.cohorts ?? []}
              getRowKey={(row) =>
                JSON.stringify([row.model, row.region, row.cohort])
              }
              columns={[
                { id: 'model', header: t('Model'), cell: (row) => row.model },
                {
                  id: 'region',
                  header: t('Region'),
                  cell: (row) => row.region,
                },
                {
                  id: 'cohort',
                  header: t('Cohort'),
                  cell: (row) =>
                    row.cohort === 'control'
                      ? t('Unmodified control')
                      : t('Compatibility enabled'),
                },
                {
                  id: 'requests',
                  header: t('Requests'),
                  cell: (row) => formatNumber(row.requests, locale),
                },
                {
                  id: 'success',
                  header: t('Success rate'),
                  cell: (row) =>
                    row.requests > 0
                      ? `${formatNumber((row.successes * 100) / row.requests, locale)}%`
                      : '—',
                },
                {
                  id: 'failures',
                  header: t('Failures'),
                  cell: (row) => formatNumber(row.failures, locale),
                },
                {
                  id: 'errors',
                  header: t('Upstream 4xx / 5xx'),
                  cell: (row) =>
                    `${formatNumber(row.upstream_4xx, locale)} / ${formatNumber(row.upstream_5xx, locale)}`,
                },
                {
                  id: 'known',
                  header: t('Measured upstream requests'),
                  cell: (row) => formatNumber(row.upstream_known, locale),
                },
                {
                  id: 'retry',
                  header: t('Retried requests / retries'),
                  cell: (row) =>
                    `${formatNumber(row.retried, locale)} / ${formatNumber(row.retries, locale)}`,
                },
                {
                  id: 'ttfb',
                  header: t('TTFB P50 / P95 (ms)'),
                  cell: (row) =>
                    row.ttfb_p50_ms !== null && row.ttfb_p95_ms !== null
                      ? `${formatNumber(row.ttfb_p50_ms, locale)} / ${formatNumber(row.ttfb_p95_ms, locale)}`
                      : '—',
                },
                {
                  id: 'timings',
                  header: t('Timing samples'),
                  cell: (row) => formatNumber(row.ttfb_samples, locale),
                },
                {
                  id: 'streams',
                  header: t('Streams'),
                  cell: (row) => formatNumber(row.streams, locale),
                },
                {
                  id: 'interrupted',
                  header: t('Interrupted / measured streams'),
                  cell: (row) =>
                    `${formatNumber(row.interrupted, locale)} / ${formatNumber(row.stream_known, locale)}`,
                },
                {
                  id: 'sample',
                  header: t('Audit sample (%)'),
                  cell: (row) =>
                    `${formatNumber(row.sample_min, locale)}–${formatNumber(row.sample_max, locale)}`,
                },
              ]}
            />
          )}
          {(query.data.rule_states?.length ?? 0) > 0 && (
            <StaticDataTable
              data={query.data.rule_states ?? []}
              getRowKey={(row) => JSON.stringify([row.model, row.rule])}
              columns={[
                { id: 'model', header: t('Model'), cell: (row) => row.model },
                {
                  id: 'rule',
                  header: t('Compatibility rule'),
                  cell: (row) => (
                    <code className='text-xs break-all'>{row.rule}</code>
                  ),
                },
                {
                  id: 'evaluated',
                  header: t('Evaluated'),
                  cell: (row) => formatNumber(row.evaluated, locale),
                },
                {
                  id: 'matched',
                  header: t('Matched'),
                  cell: (row) => formatNumber(row.matched, locale),
                },
                {
                  id: 'changed',
                  header: t('Body changed'),
                  cell: (row) => formatNumber(row.changed, locale),
                },
                {
                  id: 'skipped',
                  header: t('Skipped'),
                  cell: (row) => formatNumber(row.skipped, locale),
                },
              ]}
            />
          )}
          {(query.data.combinations?.length ?? 0) > 0 && (
            <StaticDataTable
              data={query.data.combinations ?? []}
              getRowKey={(row) =>
                JSON.stringify([
                  row.model,
                  row.output_config_policy,
                  row.thinking_enabled_map,
                ])
              }
              columns={[
                { id: 'model', header: t('Model'), cell: (row) => row.model },
                {
                  id: 'policy',
                  header: t('Output config policy'),
                  cell: (row) => row.output_config_policy,
                },
                {
                  id: 'thinking',
                  header: t('Map enabled thinking to adaptive'),
                  cell: (row) =>
                    row.thinking_enabled_map ? t('On') : t('Off'),
                },
                {
                  id: 'requests',
                  header: t('Requests'),
                  cell: (row) => formatNumber(row.requests, locale),
                },
              ]}
            />
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
