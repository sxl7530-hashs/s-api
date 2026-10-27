import { Plus, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { useChannelNameDisplay } from '../hooks/use-channel-name-display'

type MappingRow = {
  id: number
  source: string
  display: string
}

type ChannelNameDisplayDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function ChannelNameDisplayDialog(props: ChannelNameDisplayDialogProps) {
  const { t } = useTranslation()
  const { settings, updateSettings } = useChannelNameDisplay()
  const [enabled, setEnabled] = useState(settings.enabled)
  const [rows, setRows] = useState<MappingRow[]>(() => {
    const entries = Object.entries(settings.mappings)
    return entries.length > 0
      ? entries.map(([source, display], id) => ({ id, source, display }))
      : [{ id: 0, source: '', display: '' }]
  })
  const nextRowId = useRef(rows.length)
  const hasIncompleteRow = rows.some(
    (row) => Boolean(row.source.trim()) !== Boolean(row.display.trim())
  )

  useEffect(() => {
    if (!props.open) return
    setEnabled(settings.enabled)
    const entries = Object.entries(settings.mappings)
    const nextRows = entries.map(([source, display], id) => ({
      id,
      source,
      display,
    }))
    nextRowId.current = nextRows.length
    setRows(
      nextRows.length > 0
        ? nextRows
        : [{ id: nextRowId.current++, source: '', display: '' }]
    )
  }, [props.open, settings])

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Channel name display mapping')}
      description={t(
        'Change channel names only in the channel list and usage logs. Stored channel names and forms remain unchanged.'
      )}
      contentClassName='sm:max-w-xl'
      contentHeight='min(55dvh, 480px)'
      footer={
        <>
          <Button variant='outline' onClick={() => props.onOpenChange(false)}>
            {t('Cancel')}
          </Button>
          <Button
            onClick={() => {
              if (hasIncompleteRow) return
              const mappings = Object.fromEntries(
                rows
                  .filter((row) => row.source.trim() && row.display.trim())
                  .map((row) => [row.source.trim(), row.display.trim()])
              )
              updateSettings({
                enabled,
                mappings,
              })
              props.onOpenChange(false)
            }}
            disabled={hasIncompleteRow}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <div className='flex items-center justify-between gap-4 rounded-md border p-3'>
          <Label
            htmlFor='channel-name-display-enabled'
            className='cursor-pointer'
          >
            {t('Enable display mapping')}
          </Label>
          <Switch
            id='channel-name-display-enabled'
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </div>
        <div className='space-y-2'>
          <div className='flex items-center justify-between gap-3'>
            <Label>{t('Batch mappings')}</Label>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() =>
                setRows((current) => [
                  ...current,
                  { id: nextRowId.current++, source: '', display: '' },
                ])
              }
            >
              <Plus aria-hidden='true' />
              {t('Add')}
            </Button>
          </div>
          <div className='overflow-hidden rounded-md border'>
            <Table>
              <TableHeader>
                <TableRow className='bg-muted/40 hover:bg-muted/40'>
                  <TableHead className='h-9'>{t('Name')}</TableHead>
                  <TableHead className='h-9'>{t('Display name')}</TableHead>
                  <TableHead className='h-9 w-12' />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => (
                  <TableRow key={row.id}>
                    <TableCell className='py-1.5'>
                      <Input
                        aria-label={`${t('Name')} ${index + 1}`}
                        value={row.source}
                        onChange={(event) =>
                          setRows((current) =>
                            current.map((item) =>
                              item.id === row.id
                                ? { ...item, source: event.target.value }
                                : item
                            )
                          )
                        }
                      />
                    </TableCell>
                    <TableCell className='py-1.5'>
                      <Input
                        aria-label={`${t('Display name')} ${index + 1}`}
                        value={row.display}
                        onChange={(event) =>
                          setRows((current) =>
                            current.map((item) =>
                              item.id === row.id
                                ? { ...item, display: event.target.value }
                                : item
                            )
                          )
                        }
                      />
                    </TableCell>
                    <TableCell className='py-1.5'>
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={t('Delete')}
                        title={t('Delete')}
                        onClick={() =>
                          setRows((current) =>
                            current.filter((item) => item.id !== row.id)
                          )
                        }
                      >
                        <Trash2 aria-hidden='true' />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {hasIncompleteRow && (
            <p role='alert' className='text-destructive text-xs'>
              {t('Complete both names or remove the incomplete row.')}
            </p>
          )}
        </div>
      </div>
    </Dialog>
  )
}
