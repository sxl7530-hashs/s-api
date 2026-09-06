import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { createInvoiceRequest } from '../api'

interface InvoiceRequestFormProps {
  onSuccess?: () => void
}

export function InvoiceRequestForm({ onSuccess }: InvoiceRequestFormProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [form, setForm] = useState({
    company_name: '',
    tax_id: '',
    amount: '',
    email: '',
    remark: '',
  })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.company_name || !form.amount || !form.email) {
      toast.error(t('Please fill in required fields'))
      return
    }
    setLoading(true)
    try {
      const res = await createInvoiceRequest(form)
      if (res.success) {
        toast.success(t('Invoice request submitted'))
        setForm({ company_name: '', tax_id: '', amount: '', email: '', remark: '' })
        onSuccess?.()
      } else {
        toast.error(res.message)
      }
    } catch {
      toast.error(t('Request failed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Submit Invoice Request')}</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className='space-y-4'>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('Company Name')} *</Label>
              <Input
                value={form.company_name}
                onChange={(e) =>
                  setForm({ ...form, company_name: e.target.value })
                }
                placeholder={t('Company Name')}
                required
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Tax ID')}</Label>
              <Input
                value={form.tax_id}
                onChange={(e) => setForm({ ...form, tax_id: e.target.value })}
                placeholder={t('Tax ID')}
              />
            </div>
          </div>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label>{t('Amount')} *</Label>
              <Input
                value={form.amount}
                onChange={(e) => setForm({ ...form, amount: e.target.value })}
                placeholder={t('Invoice Amount')}
                required
              />
            </div>
            <div className='space-y-2'>
              <Label>{t('Email')} *</Label>
              <Input
                type='email'
                value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })}
                placeholder={t('Email for receiving invoice')}
                required
              />
            </div>
          </div>
          <div className='space-y-2'>
            <Label>{t('Remark')}</Label>
            <Textarea
              value={form.remark}
              onChange={(e) => setForm({ ...form, remark: e.target.value })}
              placeholder={t('Optional remark')}
              rows={3}
            />
          </div>
          <Button type='submit' disabled={loading}>
            {loading ? t('Submitting...') : t('Submit')}
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}
