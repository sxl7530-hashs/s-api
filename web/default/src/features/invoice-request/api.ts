import { api } from '@/lib/api'
import type {
  CreateInvoiceRequestInput,
  UpdateInvoiceStatusInput,
  InvoiceListResponse,
  InvoiceRequest,
} from './types'

interface ApiResponse<T = unknown> {
  success: boolean
  message: string
  data: T
}

export async function createInvoiceRequest(
  input: CreateInvoiceRequestInput
): Promise<ApiResponse<InvoiceRequest>> {
  const res = await api.post('/api/user/invoice_request', input)
  return res.data
}

export async function getUserInvoiceRequests(
  page: number,
  pageSize: number
): Promise<ApiResponse<InvoiceListResponse>> {
  const res = await api.get(
    `/api/user/invoice_request?p=${page}&page_size=${pageSize}`
  )
  return res.data
}

export async function getAllInvoiceRequests(
  page: number,
  pageSize: number,
  status?: number
): Promise<ApiResponse<InvoiceListResponse>> {
  let url = `/api/user/invoice_request/all?p=${page}&page_size=${pageSize}`
  if (status && status > 0) {
    url += `&status=${status}`
  }
  const res = await api.get(url)
  return res.data
}

export async function updateInvoiceRequestStatus(
  id: number,
  input: UpdateInvoiceStatusInput
): Promise<ApiResponse<null>> {
  const res = await api.put(`/api/user/invoice_request/${id}`, input)
  return res.data
}
