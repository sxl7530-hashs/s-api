export interface InvoiceRequest {
  id: number
  user_id: number
  username: string
  company_name: string
  tax_id: string
  amount: string
  email: string
  remark: string
  status: number
  admin_remark: string
  created_at: number
  updated_at: number
}

export interface CreateInvoiceRequestInput {
  company_name: string
  tax_id: string
  amount: string
  email: string
  remark: string
}

export interface UpdateInvoiceStatusInput {
  status: number
  admin_remark: string
}

export interface InvoiceListResponse {
  items: InvoiceRequest[]
  total: number
}

export const INVOICE_STATUS = {
  PENDING: 1,
  APPROVED: 2,
  REJECTED: 3,
} as const
