import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SecurityBaselinePage from './SecurityBaselinePage'

const completed = {
  cluster_id: 'cluster-b', cluster_name: 'uat-ocp-01', environment: 'uat', technology_stack: 'OCP',
  scan: {
    id: 2, cluster_id: 'cluster-b', status: 'completed', scanner: 'kube-bench', framework: 'cis',
    started_at: '2026-10-20T08:00:00Z', finished_at: '2026-10-20T08:02:00Z', error: '',
    report: {
      mode: 'job-kube-bench', scanned_at: '2026-10-20T08:02:00Z', namespaces: 0, pods: 0, truncated_samples: false,
      summary: { controls: 11, passed: 8, failed: 1, warnings: 2, score: 72 },
      coverage: { included: ['CIS 节点基线'], not_covered: [] },
      findings: [{ control_id: 'CIS-4.1.1', status: 'FAIL', severity: 'high', category: 'CIS 节点基线', title: 'Kubelet file permissions', affected: 1, resources: ['Node/worker-1'], evidence: 'kube-bench 返回 FAIL。', remediation: '修正文件权限。' }],
    },
  },
}
const unscanned = { cluster_id: 'cluster-a', cluster_name: 'stp-g01-01', environment: 'stp', technology_stack: 'TCS' }

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
})

describe('security baseline page', () => {
  it('prompts for kube-bench image configuration and blocks scans when the image is missing', async () => {
    const user = userEvent.setup()
    const payload = (data: unknown) => ({ ok: true, json: async () => ({ data, error: null }) })
    const settings = {
      security: { kube_bench_image: '', kube_bench_image_amd64: '', kube_bench_image_arm64: '', image_pull_secret: '', namespace: 'kube-system', scan_workers: 2, host_workers: 5, scan_timeout_minutes: 30, pod_startup_timeout_minutes: 5 },
      java: {},
      updated_at: {},
    }
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/settings')) return payload(settings)
      if (url.endsWith('/security-baselines')) return payload({ clusters: [unscanned] })
      return payload({})
    }))
    render(<SecurityBaselinePage />)

    expect(await screen.findByText('安全扫描缺少 kube-bench 镜像配置')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '手动执行扫描' })).toBeDisabled()
    expect(screen.getAllByRole('button', { name: '开始扫描' }).every(button => button.hasAttribute('disabled'))).toBe(true)
    await user.click(screen.getByText('前往参数配置'))
    expect(window.location.pathname).toBe('/settings/access')
    expect(window.location.search).toBe('?tab=parameters&section=security')
  })

  it('shows saved results and calls out newly onboarded unscanned clusters', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: { clusters: [unscanned, completed] }, error: null }) }))
    render(<SecurityBaselinePage />)
    expect(await screen.findByText('stp-g01-01')).toBeInTheDocument()
    expect(screen.getByText('uat-ocp-01')).toBeInTheDocument()
    expect(screen.queryByText('1 套集群尚未进行安全扫描')).not.toBeInTheDocument()
    expect(screen.getAllByText('未扫描').length).toBeGreaterThan(0)
    expect(screen.getAllByText('72%').length).toBeGreaterThan(0)
    expect(screen.queryByText(/离线补充/)).not.toBeInTheDocument()
  })

  it('submits a scan with the selected benchmark and reloads the saved result', async () => {
    const user = userEvent.setup()
    let scanned = false
    const payload = (data: unknown) => ({ ok: true, json: async () => ({ data, error: null }) })
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/security-baseline/benchmarks')) return payload([{ id: 'cis-1.24', name: 'CIS Benchmark v1.24.0', cis_version: '1.24.0', group: 'CIS Benchmark' }])
      if (url.endsWith('/security-baseline/history')) return payload({ records: scanned ? [{ ...completed.scan, cluster_id: 'cluster-a' }] : [], comparison: { new_risks: [], resolved: [], changed: [] } })
      if (url.endsWith('/security-baseline/scan')) { scanned = true; return payload({ ...completed.scan, status: 'queued' }) }
      if (url.endsWith('/security-baselines')) return payload({ clusters: scanned ? [{ ...unscanned, scan: { ...completed.scan, cluster_id: 'cluster-a' } }] : [unscanned] })
      if (url.includes('/security-baseline/scan')) return payload({ ...completed.scan, status: 'queued' })
      return payload({})
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<SecurityBaselinePage />)
    await screen.findByText('stp-g01-01')
    await user.click(screen.getAllByRole('button', { name: '开始扫描' })[0])
    expect(screen.queryByRole('dialog', { name: 'stp-g01-01 安全扫描结果' })).not.toBeInTheDocument()
    const scanCallsBefore = fetchMock.mock.calls.filter(call => String(call[0]).endsWith('/security-baseline/scan')).length
    await user.click(screen.getByRole('button', { name: '开始扫描（1）' }))
    await waitFor(() => expect(fetchMock.mock.calls.filter(call => String(call[0]).endsWith('/security-baseline/scan')).length).toBe(scanCallsBefore + 1))
    const scanCall = fetchMock.mock.calls.find(call => String(call[0]).endsWith('/security-baseline/scan'))!
    expect(JSON.parse(String((scanCall[1] as RequestInit | undefined)?.body))).toEqual({ mode: 'full', benchmark: '' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '手动执行扫描' })).not.toBeInTheDocument())
    expect(await screen.findByRole('dialog', { name: 'stp-g01-01 安全扫描结果' })).toBeInTheDocument()
    expect((await screen.findAllByText('72%')).length).toBeGreaterThan(0)
  })

  it('opens the cluster scan detail from the row action', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const data = url.endsWith('/security-baseline/history')
        ? { records: [], comparison: { new_risks: [], resolved: [], changed: [] } }
        : url.endsWith('/security-baseline/benchmarks')
          ? []
          : { clusters: [unscanned] }
      return { ok: true, json: async () => ({ data, error: null }) }
    }))
    render(<SecurityBaselinePage />)
    await screen.findByText('stp-g01-01')
    await user.click(screen.getByRole('button', { name: '扫描详情' }))
    expect(screen.getByRole('dialog', { name: 'stp-g01-01 安全扫描结果' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '全屏' }))
    expect(screen.getByRole('button', { name: '退出全屏' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '扫描详情' }))
    expect(screen.getByRole('dialog', { name: 'stp-g01-01 安全扫描结果' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '关闭集群扫描详情' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('adds a finding to the cluster whitelist with a reason', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const data = url.endsWith('/security-baseline/history')
        ? { records: [{ ...completed.scan, cluster_id: 'cluster-b' }], comparison: { new_risks: [], resolved: [], changed: [], remediation: [] } }
        : url.endsWith('/security-baseline/benchmarks')
          ? []
          : url.endsWith('/security-baseline/whitelist')
            ? (init?.method === 'POST' ? { cluster_id: 'cluster-b', control_id: 'CIS-4.1.1', reason: '测试环境白名单', created_at: '2026-10-21T12:00:00Z' } : [])
            : { clusters: [completed] }
      return { ok: true, json: async () => ({ data, error: null }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<SecurityBaselinePage />)
    await screen.findByText('uat-ocp-01')
    await user.click(screen.getByRole('button', { name: '扫描详情' }))
    await user.click(screen.getByRole('button', { name: /查看并处理失败项/ }))
    await user.click(screen.getByRole('button', { name: /CIS-4.1.1/ }))
    await user.click(screen.getByRole('button', { name: '加入白名单' }))
    await user.type(screen.getByLabelText('白名单原因'), '测试环境白名单')
    await user.click(screen.getByRole('button', { name: '确认加入' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(call => String(call[0]).endsWith('/security-baseline/whitelist') && (call[1] as RequestInit | undefined)?.method === 'POST')).toBe(true))
  })

  it('marks a previously failing control as remediated in the comparison', async () => {
    const user = userEvent.setup()
    const previous = { ...completed.scan, id: 1, finished_at: '2026-10-19T08:00:00Z' }
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const data = url.endsWith('/security-baseline/history')
        ? { records: [{ ...completed.scan, id: 2 }, previous], comparison: { previous_scan_id: 1, current_scan_id: 2, new_risks: [], resolved: [], changed: [], remediation: [{ control_id: 'CIS-4.1.1', title: 'Kubelet file permissions', previous_status: 'FAIL', current_status: 'PASS', result: 'remediated' }] } }
        : url.endsWith('/security-baseline/benchmarks') || url.endsWith('/security-baseline/whitelist')
          ? []
          : { clusters: [completed] }
      return { ok: true, json: async () => ({ data, error: null }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<SecurityBaselinePage />)
    await screen.findByText('uat-ocp-01')
    await user.click(screen.getByRole('button', { name: '扫描详情' }))
    expect(await screen.findByText('整改成功 1')).toBeInTheDocument()
    expect(screen.getAllByText('CIS-4.1.1').length).toBeGreaterThan(0)
  })

  it('opens the failed-item picker directly when there is no whitelist', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const data = url.endsWith('/security-baseline/history')
        ? { records: [{ ...completed.scan, cluster_id: 'cluster-b' }], comparison: { new_risks: [], resolved: [], changed: [], remediation: [] } }
        : url.endsWith('/security-baseline/benchmarks') || url.endsWith('/security-baseline/whitelist')
          ? []
          : { clusters: [completed] }
      return { ok: true, json: async () => ({ data, error: null }) }
    }))
    render(<SecurityBaselinePage />)
    await screen.findByText('uat-ocp-01')
    await user.click(screen.getByRole('button', { name: '扫描详情' }))
    await user.click(await screen.findByRole('button', { name: /点击选择失败项/ }))
    expect(await screen.findByRole('heading', { name: '选择不合规项加入白名单' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '加入白名单' }))
    expect(screen.getByRole('dialog', { name: /加入白名单：CIS-4.1.1/ })).toBeInTheDocument()
  })
})
