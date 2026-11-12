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
import { Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { BedrockModelQuota } from '../types'

type ModelLimit = {
  rpm?: number
  tpm?: number
  resolved_model?: string
  quota_region?: string
}

type ModelLimitRow = {
  id: number
  model: string
  rpm: string
  tpm: string
  rpmEdited?: boolean
  tpmEdited?: boolean
  metadata?: Pick<ModelLimit, 'resolved_model' | 'quota_region'>
}

type BedrockModelLimitsEditorProps = {
  value: string
  models: string[]
  onChange: (value: string) => void
  disabled: boolean
  quotas?: BedrockModelQuota[]
  quotaRegion?: string
  loadingQuotas?: boolean
}

function parseModelLimitRows(value: string): ModelLimitRow[] {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return []
    }

    return Object.entries(parsed).flatMap(([model, limit], id) => {
      if (!limit || typeof limit !== 'object' || Array.isArray(limit)) {
        return []
      }
      const values = limit as ModelLimit
      return [
        {
          id,
          model,
          rpmEdited: true,
          tpmEdited: true,
          metadata: {
            resolved_model: values.resolved_model,
            quota_region: values.quota_region,
          },
          rpm: values.rpm === undefined ? '' : String(values.rpm),
          tpm: values.tpm === undefined ? '' : String(values.tpm),
        },
      ]
    })
  } catch {
    return []
  }
}

function serializeModelLimitRows(rows: ModelLimitRow[]): string {
  const limits: Record<string, ModelLimit> = {}
  for (const row of rows) {
    const model = row.model.trim()
    if (!model) continue

    const limit: ModelLimit = { ...row.metadata }
    if (row.rpm !== '') limit.rpm = Number(row.rpm)
    if (row.tpm !== '') limit.tpm = Number(row.tpm)
    limits[model] = limit
  }
  return JSON.stringify(limits, null, 2)
}

export function BedrockModelLimitsEditor(props: BedrockModelLimitsEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const nextId = useRef(parseModelLimitRows(props.value).length)
  const lastEmittedValue = useRef(props.value)
  const [rows, setRows] = useState(() => parseModelLimitRows(props.value))

  useEffect(() => {
    if (props.value === lastEmittedValue.current) return
    lastEmittedValue.current = props.value
    const nextRows = parseModelLimitRows(props.value)
    nextId.current = Math.max(0, ...nextRows.map((row) => row.id + 1))
    setRows(nextRows)
  }, [props.value])

  const usedModels = new Set(rows.map((row) => row.model).filter(Boolean))
  const availableModels = [...new Set([...props.models, ...usedModels])]

  const onChange = props.onChange
  const updateRows = useCallback(
    (nextRows: ModelLimitRow[]) => {
      setRows(nextRows)
      const value = serializeModelLimitRows(nextRows)
      lastEmittedValue.current = value
      onChange(value)
    },
    [onChange]
  )

  useEffect(() => {
    if (!props.quotas?.length) return
    let changed = false
    const next = rows.map((row) => {
      const quota = props.quotas?.find(
        (item) => item.model === row.model && item.status === 'matched'
      )
      if (!quota) return row
      if (
        row.metadata?.resolved_model &&
        (row.metadata.resolved_model !== quota.target ||
          row.metadata.quota_region !== props.quotaRegion)
      ) {
        return row
      }
      const rpm =
        !row.rpmEdited && row.rpm === '' && quota.rpm && quota.rpm > 0
          ? String(quota.rpm)
          : row.rpm
      const tpm =
        !row.tpmEdited && row.tpm === '' && quota.tpm && quota.tpm > 0
          ? String(quota.tpm)
          : row.tpm
      if (rpm === row.rpm && tpm === row.tpm) return row
      changed = true
      return {
        ...row,
        rpm,
        tpm,
        metadata: {
          resolved_model: quota.target,
          quota_region: props.quotaRegion,
        },
      }
    })
    if (changed) updateRows(next)
  }, [props.quotas, props.quotaRegion, rows, updateRows])

  const addRow = () => {
    updateRows([...rows, { id: nextId.current++, model: '', rpm: '', tpm: '' }])
  }

  return (
    <div className='space-y-2'>
      <div className='grid grid-cols-[minmax(0,1.3fr)_minmax(4rem,0.7fr)_minmax(4rem,0.7fr)_2rem] gap-2 text-sm font-medium'>
        <div>{t('Model')}</div>
        <div>{t('RPM')}</div>
        <div>{t('TPM')}</div>
        <div />
      </div>
      {rows.map((row) => {
        const quota = props.quotas?.find((item) => item.model === row.model)
        const rowModels = availableModels.filter(
          (model) => model === row.model || !usedModels.has(model)
        )
        return (
          <div
            key={row.id}
            className='grid grid-cols-[minmax(0,1.3fr)_minmax(4rem,0.7fr)_minmax(4rem,0.7fr)_2rem] items-center gap-2'
          >
            <Select
              disabled={props.disabled}
              value={row.model}
              onValueChange={(model) =>
                updateRows(
                  rows.map((item) =>
                    item.id === row.id
                      ? {
                          ...item,
                          model: model ?? '',
                          rpm: '',
                          tpm: '',
                          metadata: undefined,
                          rpmEdited: false,
                          tpmEdited: false,
                        }
                      : item
                  )
                )
              }
            >
              <SelectTrigger aria-label={t('Model')} className='w-full'>
                <SelectValue placeholder={t('Select model')} />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {rowModels.map((model) => (
                    <SelectItem key={model} value={model}>
                      {model}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Input
              aria-label={`${t('RPM')} ${row.model || row.id + 1}`}
              type='number'
              required={
                !!row.model && !(Number(row.rpm) > 0 || Number(row.tpm) > 0)
              }
              min={0}
              step={1}
              value={row.rpm}
              disabled={props.disabled || !row.model}
              onChange={(event) =>
                updateRows(
                  rows.map((item) =>
                    item.id === row.id
                      ? { ...item, rpm: event.target.value, rpmEdited: true }
                      : item
                  )
                )
              }
            />
            <Input
              aria-label={`${t('TPM')} ${row.model || row.id + 1}`}
              type='number'
              required={
                !!row.model && !(Number(row.rpm) > 0 || Number(row.tpm) > 0)
              }
              min={0}
              step={1}
              value={row.tpm}
              disabled={props.disabled || !row.model}
              onChange={(event) =>
                updateRows(
                  rows.map((item) =>
                    item.id === row.id
                      ? { ...item, tpm: event.target.value, tpmEdited: true }
                      : item
                  )
                )
              }
            />
            <Button
              type='button'
              variant='ghost'
              size='icon'
              aria-label={t('Delete')}
              title={t('Delete')}
              disabled={props.disabled}
              onClick={() =>
                updateRows(rows.filter((item) => item.id !== row.id))
              }
            >
              <Trash2 aria-hidden='true' />
            </Button>
            <div className='text-muted-foreground col-span-4 space-y-1 text-xs'>
              {props.loadingQuotas && (
                <span>{t('Fetching AWS quotas...')}</span>
              )}
              {quota && (
                <div className='flex flex-wrap items-center gap-2'>
                  <span className='break-all'>
                    {t('Upstream model')}: {quota.target || '—'}
                  </span>
                  <span>
                    AWS RPM:{' '}
                    {quota.rpm === undefined
                      ? '—'
                      : formatNumber(quota.rpm, locale)}{' '}
                    · TPM:{' '}
                    {quota.tpm === undefined
                      ? '—'
                      : formatNumber(quota.tpm, locale)}
                  </span>
                  {quota.status === 'matched' ? (
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      disabled={props.disabled}
                      onClick={() =>
                        updateRows(
                          rows.map((item) =>
                            item.id === row.id
                              ? {
                                  ...item,
                                  rpm:
                                    quota.rpm && quota.rpm > 0
                                      ? String(quota.rpm)
                                      : '',
                                  tpm:
                                    quota.tpm && quota.tpm > 0
                                      ? String(quota.tpm)
                                      : '',
                                  rpmEdited: false,
                                  tpmEdited: false,
                                  metadata: {
                                    resolved_model: quota.target,
                                    quota_region: props.quotaRegion,
                                  },
                                }
                              : item
                          )
                        )
                      }
                    >
                      {t('Restore AWS quotas')}
                    </Button>
                  ) : (
                    <span className='text-destructive'>
                      {quota.status === 'zero_quota'
                        ? t(
                            'AWS reports a zero quota. Automatic import is disabled; verify access before setting limits manually.'
                          )
                        : t(
                            'No unique AWS quota match. Enter limits manually.'
                          )}
                    </span>
                  )}
                  {row.metadata?.resolved_model &&
                    (row.metadata.resolved_model !== quota.target ||
                      row.metadata.quota_region !== props.quotaRegion) && (
                      <span className='text-destructive'>
                        {t(
                          'Model target changed. Restore quotas or reselect the model.'
                        )}
                      </span>
                    )}
                </div>
              )}
              {row.model &&
                row.rpm === '' &&
                row.tpm === '' &&
                !props.loadingQuotas && (
                  <span className='text-destructive'>
                    {t(
                      'Enter at least one positive RPM or TPM limit before saving.'
                    )}
                  </span>
                )}
            </div>
          </div>
        )
      })}
      <Button
        type='button'
        variant='outline'
        size='sm'
        className='w-full'
        disabled={
          props.disabled || props.models.every((model) => usedModels.has(model))
        }
        onClick={addRow}
      >
        <Plus aria-hidden='true' />
        {t('Add Row')}
      </Button>
    </div>
  )
}
