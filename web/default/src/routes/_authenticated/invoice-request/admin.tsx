import { createFileRoute } from '@tanstack/react-router'
import { AdminInvoiceList } from '@/features/invoice-request'

export const Route = createFileRoute('/_authenticated/invoice-request/admin')({
  component: RouteComponent,
})

function RouteComponent() {
  return <AdminInvoiceList />
}
