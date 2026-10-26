import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import OperationalReportsPage from './OperationalReportsPage'

const clusters = [{ id: 'c1', name: 'cluster-a', environment: 'stp', cluster_type: 'G01' }]

const report = {
  id: 'report-1', title: '2026.10.13 — 10.19 集群运行周报', period: 'weekly', environment: '', cluster_type: '',
  generated_at: '2026-10-20T02:00:00Z', ai_status: 'failed', url: '/api/v1/reports/report-1/html',
}

const aiFailure = 'AI 分析未完成：无法连接模型服务，请检查地址、DNS、TLS 与网络策略。已保留事实图表与规则建议。'

const baseJob = { status: 'running', stage: 'collecting', message: '正在汇总数据', error_message: '', report_id: '', request: { period: 'weekly', date: '2026-10-19', environment: '', cluster_type: '', use_ai: true }, created_at: '2026-10-20T02:00:00Z' }

function mockReports(jobs: Record<string, unknown>[], reports: Record<string, unknown>[] = [report]) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.startsWith('/api/v1/report-jobs')) return { ok: true, json: async () => ({ data: { items: jobs }, error: null }) }
    if (url === '/api/v1/reports') return { ok: true, json: async () => ({ data: { items: reports }, error: null }) }
    if (url === '/api/v1/reports/config') return { ok: true, json: async () => ({ data: { endpoint: 'https://model.internal/v1', model: 'deepseek-chat', has_api_key: true }, error: null }) }
    return { ok: true, json: async () => ({ data: null, error: null }) }
  })
}

vi.mock('../auth/context', () => ({ usePermission: () => true, useAuth: () => ({ user: { username: 'admin' } }) }))

afterEach(() => { vi.unstubAllGlobals() })

describe('operational reports page', () => {
  it('hides a cleanly finished job, because the report list already shows it', async () => {
    vi.stubGlobal('fetch', mockReports([{ ...baseJob, id: 'job-done', status: 'completed', stage: 'completed', message: '报告已生成', report_id: 'report-1' }]))
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText(report.title)).toBeInTheDocument()
    expect(screen.queryByText('后台任务')).not.toBeInTheDocument()
    expect(screen.queryByText('报告已生成')).not.toBeInTheDocument()
  })

  it('keeps a running job visible while it is in flight', async () => {
    vi.stubGlobal('fetch', mockReports([{ ...baseJob, id: 'job-run' }]))
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText('后台任务')).toBeInTheDocument()
    expect(screen.getByText('正在汇总数据')).toBeInTheDocument()
    expect(screen.getByText('执行中')).toBeInTheDocument()
  })

  it('still surfaces a completed job whose AI analysis was dropped', async () => {
    vi.stubGlobal('fetch', mockReports([{ ...baseJob, id: 'job-ai', status: 'completed', stage: 'completed', message: '事实报告已生成，AI 分析未完成', error_message: aiFailure, report_id: 'report-1' }]))
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText(aiFailure)).toBeInTheDocument()
    expect(screen.getByText('AI 未完成')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '前往模型配置' })).toHaveAttribute('href', '/settings/access?tab=parameters&section=reports')
  })

  it('does not keep an older AI warning after a newer report succeeds', async () => {
    vi.stubGlobal('fetch', mockReports([
      { ...baseJob, id: 'job-new', status: 'completed', stage: 'completed', message: '报告已生成', report_id: 'report-2', created_at: '2026-10-20T03:00:00Z' },
      { ...baseJob, id: 'job-old', status: 'completed', stage: 'completed', message: '事实报告已生成，AI 分析未完成', error_message: aiFailure, report_id: 'report-1' },
    ]))
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText(report.title)).toBeInTheDocument()
    expect(screen.queryByText(aiFailure)).not.toBeInTheDocument()
    expect(screen.queryByText('后台任务')).not.toBeInTheDocument()
  })

  it('keeps a failed job, which never produces a report row', async () => {
    vi.stubGlobal('fetch', mockReports([{ ...baseJob, id: 'job-fail', status: 'failed', stage: 'failed', message: '报告生成失败', error_message: '数据库连接中断' }]))
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText('数据库连接中断')).toBeInTheDocument()
    expect(screen.getByText('失败')).toBeInTheDocument()
  })

  it('opens the report directly from both the job card and the report row', async () => {
    vi.stubGlobal('fetch', mockReports([{ ...baseJob, id: 'job-ai', status: 'completed', stage: 'completed', message: '事实报告已生成，AI 分析未完成', error_message: aiFailure, report_id: 'report-1' }]))
    render(<OperationalReportsPage clusters={clusters} />)
    // The job card action opens a new tab instead of previewing in place.
    expect(await screen.findByRole('link', { name: /查看报告/ })).toHaveAttribute('href', '/api/v1/reports/report-1/html')
    // The report title is a direct link too, and the in-page preview is gone.
    const title = screen.getByRole('link', { name: report.title })
    expect(title).toHaveAttribute('href', '/api/v1/reports/report-1/html')
    expect(title).toHaveAttribute('target', '_blank')
    expect(screen.queryByRole('button', { name: '预览' })).not.toBeInTheDocument()
  })

  it('deletes a report record after confirming', async () => {
    const user = userEvent.setup()
    let deleted = false
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/v1/reports/report-1' && init?.method === 'DELETE') {
        deleted = true
        return { ok: true, json: async () => ({ data: { id: 'report-1' }, error: null }) }
      }
      if (url.startsWith('/api/v1/report-jobs')) return { ok: true, json: async () => ({ data: { items: [] }, error: null }) }
      if (url === '/api/v1/reports') return { ok: true, json: async () => ({ data: { items: [report] }, error: null }) }
      if (url === '/api/v1/reports/config') return { ok: true, json: async () => ({ data: { endpoint: 'https://model.internal/v1', model: 'deepseek-chat', has_api_key: true }, error: null }) }
      return { ok: true, json: async () => ({ data: null, error: null }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<OperationalReportsPage clusters={clusters} />)
    expect(await screen.findByText(report.title)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '删除' }))
    expect(await screen.findByText('确认删除报告')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '确认删除' }))
    expect(deleted).toBe(true)
    expect(await screen.findByText('还没有运行报告')).toBeInTheDocument()
    expect(screen.queryByText(report.title)).not.toBeInTheDocument()
  })
})
