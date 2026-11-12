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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

type Entry = Record<string, unknown>
type Props = {
  kind: 'beta' | 'fingerprint'
  value: unknown
  onChange: (value: Entry[]) => void
}

// The shared static table supplies the table, overflow and empty-state behavior;
// this feature component only composes the provider's editable business fields.
export function Claude5CapabilitySettings(props: Props) {
  const { t } = useTranslation()
  const entries: Entry[] = Array.isArray(props.value)
    ? props.value.map((entry: unknown) =>
        entry && typeof entry === 'object' && !Array.isArray(entry)
          ? (entry as Entry)
          : {}
      )
    : []
  const isBeta = props.kind === 'beta'
  const update = (index: number, field: string, value: string) =>
    props.onChange(
      entries.map((entry, i) =>
        i === index ? { ...entry, [field]: value } : entry
      )
    )
  const statuses = isBeta
    ? ['allow', 'deny']
    : [
        'unknown',
        'preserve_verified',
        'preserve_rejected',
        'remove_verified',
        'unavailable',
      ]
  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-xs'>
        {isBeta
          ? t(
              'Only add capabilities verified for this exact Bedrock model and region. Unknown capabilities do not inject beta.'
            )
          : t(
              'Record administrator-verified fingerprint results. These records do not automatically change request policies.'
            )}
      </p>
      {!isBeta &&
        entries.some((entry) => entry.status === 'preserve_rejected') && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Fingerprint preservation was rejected for a listed model. Review its forge policy before enabling compatibility.'
              )}
            </AlertDescription>
          </Alert>
        )}
      <StaticDataTable
        data={entries}
        columns={[
          ...(['model', 'region'] as const).map((field) => ({
            id: field,
            header: field === 'model' ? t('Model') : t('Region'),
            cell: (entry: Entry, index: number) => (
              <Input
                aria-label={`${field === 'model' ? t('Model') : t('Region')} ${index + 1}`}
                className='min-w-44'
                value={typeof entry[field] === 'string' ? entry[field] : ''}
                onChange={(event) => update(index, field, event.target.value)}
              />
            ),
          })),
          {
            id: 'status',
            header: t('Status'),
            cell: (entry, index) => (
              <NativeSelect
                aria-label={`${t('Status')} ${index + 1}`}
                value={
                  typeof entry.status === 'string' ? entry.status : statuses[0]
                }
                onChange={(event) =>
                  update(index, 'status', event.target.value)
                }
              >
                {statuses.map((status) => (
                  <NativeSelectOption key={status} value={status}>
                    {status}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            ),
          },
          ...(isBeta
            ? [
                {
                  id: 'source',
                  header: t('Source'),
                  cell: (entry: Entry, index: number) => (
                    <NativeSelect
                      aria-label={`${t('Source')} ${index + 1}`}
                      value={
                        typeof entry.source === 'string'
                          ? entry.source
                          : 'admin'
                      }
                      onChange={(event) =>
                        update(index, 'source', event.target.value)
                      }
                    >
                      <NativeSelectOption value='admin'>
                        admin
                      </NativeSelectOption>
                      <NativeSelectOption value='static'>
                        static
                      </NativeSelectOption>
                    </NativeSelect>
                  ),
                },
              ]
            : []),
          {
            id: 'actions',
            header: t('Actions'),
            cell: (_, index) => (
              <Button
                type='button'
                variant='ghost'
                onClick={() =>
                  props.onChange(entries.filter((_, i) => i !== index))
                }
              >
                {t('Delete')}
              </Button>
            ),
          },
        ]}
      />
      <Button
        type='button'
        variant='outline'
        disabled={entries.length >= 1000}
        onClick={() =>
          props.onChange([
            ...entries,
            isBeta
              ? {
                  model: '',
                  region: '',
                  beta: 'thinking-binding-controls-2026-08-01',
                  status: 'allow',
                  source: 'admin',
                }
              : { model: '', region: '', status: 'unknown' },
          ])
        }
      >
        {isBeta ? t('Add beta capability') : t('Add fingerprint review')}
      </Button>
    </div>
  )
}
