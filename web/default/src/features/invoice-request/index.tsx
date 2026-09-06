import { useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { SectionPageLayout } from '@/components/layout'
import { InvoiceRequestForm } from './components/invoice-request-form'
import { InvoiceRequestList } from './components/invoice-request-list'

export function InvoiceRequestPage() {
  const { t } = useTranslation()
  const [refreshKey, setRefreshKey] = useState(0)

  const handleSuccess = useCallback(() => {
    setRefreshKey((k) => k + 1)
  }, [])

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Invoice Request')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-6'>
          <InvoiceRequestForm onSuccess={handleSuccess} />
          <InvoiceRequestList key={refreshKey} />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export { AdminInvoiceList } from './components/admin-invoice-list'
