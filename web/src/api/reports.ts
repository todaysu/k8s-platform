export type ReportPeriod = 'weekly' | 'monthly' | 'quarterly'

export type ReportRequest = {
  period: ReportPeriod
  date: string
  environment: string
  cluster_type: string
  use_ai: boolean
}

export type ReportItem = {
  id: string
  title: string
  period: ReportPeriod
  environment: string
  cluster_type: string
  generated_at: string
  ai_status: string
  url: string
}

export type OperationalReport = {
  id: string
  title: string
  request: ReportRequest
  window: { start: string; end: string; label: string }
  generated_at: string
  ai_status: string
  ai_message: string
  model: string
  url: string
}

export type ReportJob = {
  id: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  stage: string
  message: string
  error_message: string
  report_id: string
  request: ReportRequest
  created_at: string
  started_at?: string
  finished_at?: string
}

export type ReportModelConfig = {
  enabled: boolean
  endpoint: string
  model: string
  api_key?: string
  has_api_key: boolean
  clear_api_key?: boolean
}

type Envelope<T> = { data?: T; error?: { message?: string } | null }

async function request<T>(url: string, fallback: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(url, init)
  } catch {
    throw new Error(`${fallback}请检查平台连接后重试。`)
  }
  let payload: Envelope<T>
  try {
    payload = await response.json() as Envelope<T>
  } catch {
    throw new Error(`${fallback}平台返回的响应格式异常。`)
  }
  if (!response.ok || payload.error || payload.data == null) {
    throw new Error(payload.error?.message || fallback)
  }
  return payload.data
}

export async function listReports(): Promise<ReportItem[]> {
  const result = await request<{ items: ReportItem[] | null }>('/api/v1/reports', '读取报告列表失败。')
  return Array.isArray(result.items) ? result.items : []
}

export function createReport(value: ReportRequest): Promise<ReportJob> {
  return request('/api/v1/reports', '生成报告失败。', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value),
  })
}

export function deleteReport(id: string): Promise<{ id: string }> {
  return request(`/api/v1/reports/${encodeURIComponent(id)}`, '删除报告失败。', { method: 'DELETE' })
}

export async function listReportJobs(): Promise<ReportJob[]> {
  const result = await request<{ items: ReportJob[] | null }>('/api/v1/report-jobs', '读取后台报告任务失败。')
  return Array.isArray(result.items) ? result.items : []
}

export async function getReportModelConfig(): Promise<ReportModelConfig> {
  const value = await request<Omit<ReportModelConfig, 'enabled'>>('/api/v1/reports/config', '读取模型配置失败。')
  return { ...value, enabled: false }
}

export type ReportModelProbe = { ok: boolean; message: string }

export async function saveReportModelConfig(value: ReportModelConfig): Promise<ReportModelConfig> {
  const { enabled, ...payload } = value
  const saved = await request<Omit<ReportModelConfig, 'enabled'>>('/api/v1/reports/config', '保存模型配置失败。', {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
  })
  return { ...saved, enabled }
}

export async function testReportModelConfig(value: ReportModelConfig): Promise<ReportModelProbe> {
  const { enabled: _enabled, ...payload } = value
  return request<ReportModelProbe>('/api/v1/reports/config/test', '测试模型连接失败。', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
  })
}

// Report links always stay on the platform, including when old records contain
// a full URL from a previous deployment address.
export function reportPath(item: Pick<ReportItem, 'id'>): string {
  return `/api/v1/reports/${encodeURIComponent(item.id)}/html`
}
