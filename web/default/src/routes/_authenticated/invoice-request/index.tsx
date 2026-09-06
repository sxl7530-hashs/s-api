import { createFileRoute } from '@tanstack/react-router'
import { InvoiceRequestPage } from '@/features/invoice-request'

export const Route = createFileRoute('/_authenticated/invoice-request/')({
  component: RouteComponent,
})

function RouteComponent() {
  return <InvoiceRequestPage />
}
