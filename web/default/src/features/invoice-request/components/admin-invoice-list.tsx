import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getAllInvoiceRequests, updateInvoiceRequestStatus } from '../api'
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

const STATUS_FILTERS = [
  { value: 0, label: 'All' },
  { value: INVOICE_STATUS.PENDING, label: 'Pending' },
  { value: INVOICE_STATUS.APPROVED, label: 'Invoiced' },
  { value: INVOICE_STATUS.REJECTED, label: 'Rejected' },
] as const

export function AdminInvoiceList() {
  const { t } = useTranslation()
  const [items, setItems] = useState<InvoiceRequest[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [statusFilter, setStatusFilter] = useState(0)
  const [loading, setLoading] = useState(false)
  const [selectedItem, setSelectedItem] = useState<InvoiceRequest | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [adminRemark, setAdminRemark] = useState('')
  const [updating, setUpdating] = useState(false)
  const pageSize = 20

  const fetchData = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getAllInvoiceRequests(page, pageSize, statusFilter)
      if (res.success && res.data) {
        setItems(res.data.items || [])
        setTotal(res.data.total)
      }
    } finally {
      setLoading(false)
    }
  }, [page, statusFilter])

  useEffect(() => {
    fetchData()
  }, [fetchData])

  const handleAction = (item: InvoiceRequest) => {
    setSelectedItem(item)
    setAdminRemark(item.admin_remark || '')
    setDialogOpen(true)
  }

  const handleUpdateStatus = async (status: number) => {
    if (!selectedItem) return
    setUpdating(true)
    try {
      const res = await updateInvoiceRequestStatus(selectedItem.id, {
        status,
        admin_remark: adminRemark,
      })
      if (res.success) {
        toast.success(t('Updated successfully'))
        setDialogOpen(false)
        fetchData()
      } else {
        toast.error(res.message)
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setUpdating(false)
    }
  }

  const totalPages = Math.ceil(total / pageSize)

  return (
    <>
      <Card>
        <CardHeader>
          <div className='flex items-center justify-between'>
            <CardTitle>{t('Invoice Request Management')}</CardTitle>
            <div className='flex gap-2'>
              {STATUS_FILTERS.map((f) => (
                <Button
                  key={f.value}
                  variant={statusFilter === f.value ? 'default' : 'outline'}
                  size='sm'
                  onClick={() => {
                    setStatusFilter(f.value)
                    setPage(1)
                  }}
                >
                  {t(f.label)}
                </Button>
              ))}
            </div>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className='py-8 text-center text-muted-foreground'>
              {t('Loading...')}
            </div>
          ) : items.length === 0 ? (
            <div className='py-8 text-center text-muted-foreground'>
              {t('No invoice requests')}
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>{t('User')}</TableHead>
                    <TableHead>{t('Company Name')}</TableHead>
                    <TableHead>{t('Tax ID')}</TableHead>
                    <TableHead>{t('Amount')}</TableHead>
                    <TableHead>{t('Email')}</TableHead>
                    <TableHead>{t('Remark')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead>{t('Created At')}</TableHead>
                    <TableHead>{t('Actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((item) => (
                    <TableRow key={item.id}>
                      <TableCell>{item.id}</TableCell>
                      <TableCell>{item.username}</TableCell>
                      <TableCell>{item.company_name}</TableCell>
                      <TableCell>{item.tax_id || '-'}</TableCell>
                      <TableCell>{item.amount}</TableCell>
                      <TableCell>{item.email}</TableCell>
                      <TableCell className='max-w-32 truncate'>
                        {item.remark || '-'}
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={item.status} />
                      </TableCell>
                      <TableCell>{formatTime(item.created_at)}</TableCell>
                      <TableCell>
                        {item.status === INVOICE_STATUS.PENDING && (
                          <Button
                            variant='outline'
                            size='sm'
                            onClick={() => handleAction(item)}
                          >
                            {t('Process')}
                          </Button>
                        )}
                      </TableCell>
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

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('Process Invoice Request')}</DialogTitle>
          </DialogHeader>
          {selectedItem && (
            <div className='space-y-4'>
              <div className='grid grid-cols-2 gap-2 text-sm'>
                <div>
                  <span className='text-muted-foreground'>
                    {t('Company Name')}:
                  </span>{' '}
                  {selectedItem.company_name}
                </div>
                <div>
                  <span className='text-muted-foreground'>{t('Tax ID')}:</span>{' '}
                  {selectedItem.tax_id || '-'}
                </div>
                <div>
                  <span className='text-muted-foreground'>{t('Amount')}:</span>{' '}
                  {selectedItem.amount}
                </div>
                <div>
                  <span className='text-muted-foreground'>{t('Email')}:</span>{' '}
                  {selectedItem.email}
                </div>
              </div>
              {selectedItem.remark && (
                <div className='text-sm'>
                  <span className='text-muted-foreground'>{t('Remark')}:</span>{' '}
                  {selectedItem.remark}
                </div>
              )}
              <div className='space-y-2'>
                <Label>{t('Admin Remark')}</Label>
                <Textarea
                  value={adminRemark}
                  onChange={(e) => setAdminRemark(e.target.value)}
                  placeholder={t('Optional admin remark')}
                  rows={3}
                />
              </div>
            </div>
          )}
          <DialogFooter>
            <Button
              variant='destructive'
              disabled={updating}
              onClick={() => handleUpdateStatus(INVOICE_STATUS.REJECTED)}
            >
              {t('Reject')}
            </Button>
            <Button
              disabled={updating}
              onClick={() => handleUpdateStatus(INVOICE_STATUS.APPROVED)}
            >
              {t('Approve & Invoice')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
