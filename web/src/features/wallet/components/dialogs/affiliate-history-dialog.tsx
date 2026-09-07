import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'

import { getAffiliateTransferLogs } from '../../api'
import type { AffiliateTransferLog } from '../../types'

export function AffiliateHistoryDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t } = useTranslation()
  const [items, setItems] = useState<AffiliateTransferLog[]>([])
  const [loading, setLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)

  useEffect(() => {
    if (!open) return
    setLoading(true)
    getAffiliateTransferLogs(page)
      .then((res) => {
        if (res.success && res.data) {
          setItems(res.data.items)
          setTotal(res.data.total)
        }
      })
      .finally(() => setLoading(false))
  }, [open, page])

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={t('Referral History')}>
      {loading ? <Skeleton className='h-32 w-full' /> : items.length === 0 ? <p className='text-muted-foreground py-8 text-center text-sm'>{t('No referral history')}</p> : <div className='space-y-2'>{items.map((item) => <div key={item.id} className='flex justify-between rounded border p-3 text-sm'><span>{new Date(item.created_at * 1000).toLocaleString()}</span><span className='font-medium'>+{formatQuota(item.quota)}</span></div>)}</div>}
      {total > 20 && <div className='mt-4 flex justify-end gap-2'><Button size='sm' variant='outline' disabled={page === 1} onClick={() => setPage((p) => p - 1)}>{t('Previous')}</Button><Button size='sm' variant='outline' disabled={page * 20 >= total} onClick={() => setPage((p) => p + 1)}>{t('Next')}</Button></div>}
    </Dialog>
  )
}
