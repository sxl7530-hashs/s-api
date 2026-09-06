import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getUserInvoiceRequests } from '../api'
import { INVOICE_STATUS, type InvoiceRequest } from '../types'

function StatusBadge({ status }: { status: number }) {
  const { t } = useTranslation()
  switch (status) {
    case INVOICE_STATUS.PENDING:
      return <Badge variant='outline'>{t('Pending')}</Badge>
    case INVOICE_STATUS.APPROVED:
      return <Badge variant='default'>{t('Invoiced')}</Badge>
    case INVOICE_STATUS.REJECTED:
      return <Badge variant='destructive'>{t('Rejected')}</Badge>
    default:
      return <Badge variant='secondary'>{t('Unknown')}</Badge>
  }
}

function formatTime(ts: number) {
  if (!ts) return '-'
  return new Date(ts * 1000).toLocaleString()
}

export function InvoiceRequestList() {
  const { t } = useTranslation()
  const [items, setItems] = useState<InvoiceRequest[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const pageSize = 10

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getUserInvoiceRequests(page, pageSize)
      if (res.success && res.data) {
        setItems(res.data.items || [])
        setTotal(res.data.total)
      }
    } finally {
      setLoading(false)
    }
  }, [page])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const totalPages = Math.ceil(total / pageSize)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('My Invoice Requests')}</CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <div className='py-8 text-center text-muted-foreground'>
            {t('Loading...')}
          </div>
        ) : items.length === 0 ? (
          <div className='py-8 text-center text-muted-foreground'>
            {t('No invoice requests yet')}
          </div>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Company Name')}</TableHead>
                  <TableHead>{t('Amount')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead>{t('Admin Remark')}</TableHead>
                  <TableHead>{t('Created At')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell>{item.company_name}</TableCell>
                    <TableCell>{item.amount}</TableCell>
                    <TableCell>
                      <StatusBadge status={item.status} />
                    </TableCell>
                    <TableCell>{item.admin_remark || '-'}</TableCell>
                    <TableCell>{formatTime(item.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {totalPages > 1 && (
              <div className='mt-4 flex items-center justify-center gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={page <= 1}
                  onClick={() => setPage((p) => p - 1)}
                >
                  {t('Previous')}
                </Button>
                <span className='text-sm text-muted-foreground'>
                  {page} / {totalPages}
                </span>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={page >= totalPages}
                  onClick={() => setPage((p) => p + 1)}
                >
                  {t('Next')}
                </Button>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
