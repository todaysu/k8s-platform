import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import FleetAlertsPage from './FleetAlertsPage'

afterEach(() => { vi.unstubAllGlobals(); history.replaceState({}, '', '/') })

it('shows current alerts, coverage, history and resource drill-down', async () => {
  history.replaceState({}, '', '/alerts')
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const historyView = String(input).includes('view=history')
    return { ok: true, json: async () => ({ data: {
      total: 1,
      summary: { critical: 0, warning: 1, info: 0, affected_clusters: 1, affected_resources: 1 },
      retention_days: 90,
      collected_at: '2026-10-24T10:00:00Z',
      coverage: [{ cluster_id: 'c1', status: 'error', collected_at: '2026-10-24T10:00:00Z', message: 'Service 代理不可用' }],
      items: [{ id: 'a1', cluster_id: 'c1', fingerprint: 'fp1', name: 'PodCrashLooping', severity: 'warning', namespace: 'myapp', resource_kind: 'Pod', resource_name: 'web-1', summary: 'Pod 重启', description: '容器反复退出', labels: { alertname: 'PodCrashLooping' }, owner_kind: 'Deployment', owner_name: 'web', source_type: 'business', occurrence_count: 3, runbook_url: 'https://runbooks.example/pod', rule_url: '', state: historyView ? 'resolved' : 'firing', source: 'monitoring/alertmanager', starts_at: '2026-10-24T09:00:00Z', last_seen_at: '2026-10-24T10:00:00Z', ends_at: historyView ? '2026-10-24T10:05:00Z' : undefined }],
    } }) }
  })
  vi.stubGlobal('fetch', fetcher)
  render(<FleetAlertsPage clusters={[{ id: 'c1', name: 'test01', environment: 'prod' }]} />)
  expect(await screen.findByText('PodCrashLooping')).toBeInTheDocument()
  expect(screen.getByText(/当前保留 90 天/)).toBeInTheDocument()
  const warningCard = screen.getByRole('button', { name: /^Warning/ })
  fireEvent.click(warningCard)
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes('severity=warning'))).toBe(true))
  expect(warningCard).toHaveAttribute('aria-pressed', 'true')
  fireEvent.click(screen.getByRole('button', { name: /^受影响资源/ }))
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes('resource=linked'))).toBe(true))
  fireEvent.click(screen.getByRole('button', { name: /^采集覆盖/ }))
  expect(screen.getByLabelText('告警采集覆盖明细')).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: '告警时间范围' })).toHaveValue('')
  fireEvent.change(screen.getByRole('combobox', { name: '告警时间范围' }), { target: { value: 'gt24h' } })
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes('duration=gt24h'))).toBe(true))
  expect(screen.getByText(/test01：Service 代理不可用/)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '查看详情' }))
  expect(screen.getByRole('dialog', { name: '告警详情' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '打开资源' }))
  expect(location.pathname).toBe('/clusters/c1/pods/myapp/web-1')
  fireEvent.click(screen.getByRole('tab', { name: '已恢复历史' }))
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes('view=history'))).toBe(true))
  fireEvent.change(screen.getByRole('combobox', { name: '告警时间范围' }), { target: { value: '24h' } })
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes('range=24h'))).toBe(true))
  await waitFor(() => expect(screen.getAllByText('已恢复').length).toBeGreaterThan(0))
})
