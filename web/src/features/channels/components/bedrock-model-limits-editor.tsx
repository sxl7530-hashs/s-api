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
import { useEffect, useRef, useState } from 'react'
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

type ModelLimit = {
  rpm?: number
  tpm?: number
}

type ModelLimitRow = {
  id: number
  model: string
  rpm: string
  tpm: string
}

type BedrockModelLimitsEditorProps = {
  value: string
  models: string[]
  onChange: (value: string) => void
  disabled: boolean
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

    const limit: ModelLimit = {}
    if (row.rpm !== '') limit.rpm = Number(row.rpm)
    if (row.tpm !== '') limit.tpm = Number(row.tpm)
    if (Object.keys(limit).length > 0) limits[model] = limit
  }
  return JSON.stringify(limits, null, 2)
}

export function BedrockModelLimitsEditor(props: BedrockModelLimitsEditorProps) {
  const { t } = useTranslation()
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

  const updateRows = (nextRows: ModelLimitRow[]) => {
    setRows(nextRows)
    const value = serializeModelLimitRows(nextRows)
    lastEmittedValue.current = value
    props.onChange(value)
  }

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
                    item.id === row.id ? { ...item, model: model ?? '' } : item
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
              min={0}
              step={1}
              value={row.rpm}
              disabled={props.disabled || !row.model}
              onChange={(event) =>
                updateRows(
                  rows.map((item) =>
                    item.id === row.id
                      ? { ...item, rpm: event.target.value }
                      : item
                  )
                )
              }
            />
            <Input
              aria-label={`${t('TPM')} ${row.model || row.id + 1}`}
              type='number'
              min={0}
              step={1}
              value={row.tpm}
              disabled={props.disabled || !row.model}
              onChange={(event) =>
                updateRows(
                  rows.map((item) =>
                    item.id === row.id
                      ? { ...item, tpm: event.target.value }
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
