import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import PlatformSettingsPage from './PlatformSettingsPage'

const defaults = {
  runtime: { query_concurrency: 64, realtime_concurrency: 20, monitoring_concurrency: 8, session_concurrency: 10, per_cluster_concurrency: 2, auto_refresh_seconds: 30 },
  collection: { health_interval_seconds: 60, health_timeout_seconds: 20, health_workers: 5, capacity_interval_seconds: 300, capacity_timeout_seconds: 45, capacity_workers: 3, resource_interval_seconds: 600, resource_timeout_seconds: 30, resource_workers: 2 },
  events: { normal_retention_days: 7, warning_retention_days: 30, rollup_retention_days: 365, daily_retention_days: 31, cleanup_batch_size: 5000 },
  security: { kube_bench_image: 'repo/kube-bench:v1', kube_bench_image_amd64: '', kube_bench_image_arm64: '', image_pull_secret: '', namespace: 'kube-system', scan_workers: 2, host_workers: 5, scan_timeout_minutes: 30, pod_startup_timeout_minutes: 5 },
  java: { arthas_url: '', arthas_version: '4.3.5', arthas_sha256: '', download_timeout_seconds: 60, diagnostic_timeout_minutes: 30, max_concurrency: 2, heap_dump_enabled: false, heap_dump_max_gib: 4, temporary_retention_hours: 24 },
  database: { backup_enabled: false, backup_time: '02:00', backup_retention_days: 30, backup_target_node: '', backup_target_path: '', space_warning_percent: 80, space_critical_percent: 90, connection_check_seconds: 30 },
  updated_at: {},
}

const modelConfig = { endpoint: 'https://model.internal/v1', model: 'deepseek-chat', has_api_key: true }

function mockSettingsFetch() {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/api/v1/reports/config') {
      const value = init?.method === 'PUT' ? { ...JSON.parse(String(init.body)), api_key: undefined, clear_api_key: undefined, has_api_key: true } : modelConfig
      return { ok: true, json: async () => ({ data: value, error: null }) }
    }
    return { ok: true, json: async () => ({ data: defaults, error: null }) }
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
})

describe('platform settings page', () => {
  it('uses the built-in Arthas version by default and allows an offline update package', async () => {
    window.history.replaceState({}, '', '/settings/access?tab=parameters&section=java')
    vi.stubGlobal('fetch', mockSettingsFetch())
    render(<PlatformSettingsPage />)
    expect(await screen.findByRole('textbox', { name: /^Arthas 更新离线包地址/ })).toHaveValue('')
    expect(screen.getByText(/默认使用平台内置 Arthas 4.3.5/)).toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: /^更新包下载超时/ })).toHaveValue(60)
  })

  it('loads categories and saves the active section only', async () => {
    window.history.replaceState({}, '', '/settings/runtime')
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (String(_input) === '/api/v1/reports/config') return { ok: true, json: async () => ({ data: modelConfig, error: null }) }
      const saved = init?.method === 'PUT'
      const settings = { ...defaults, runtime: saved ? JSON.parse(String(init?.body)) : defaults.runtime, updated_at: saved ? { runtime: '2026-10-22T02:00:00Z' } : {} }
      return { ok: true, json: async () => ({ data: saved ? { settings, apply: { status: 'restarting', targets: ['ftc-platform'], message: '配置已保存，正在滚动重启 ftc-platform。' } } : settings, error: null }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<PlatformSettingsPage />)
    const input = await screen.findByRole('spinbutton', { name: /^普通查询并发/ })
    await user.clear(input)
    await user.type(input, '80')
    await user.click(screen.getByRole('button', { name: '保存配置' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/settings/runtime', expect.objectContaining({ method: 'PUT' })))
    expect(await screen.findByText('配置已保存，正在滚动重启 ftc-platform。')).toBeInTheDocument()
  })

  it('shows an inline reminder and blocks saving when a parameter is invalid', async () => {
    vi.stubGlobal('fetch', mockSettingsFetch())
    const user = userEvent.setup()
    render(<PlatformSettingsPage />)
    const input = await screen.findByRole('spinbutton', { name: /^普通查询并发/ })
    await user.clear(input)
    await user.type(input, '0')
    expect(await screen.findByText('有 1 项参数需要修正')).toBeInTheDocument()
    expect(screen.getByText('请输入 1–1000 之间的整数。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
  })

  it('switches to the security image configuration without leaving the page shell', async () => {
    vi.stubGlobal('fetch', mockSettingsFetch())
    const user = userEvent.setup()
    render(<PlatformSettingsPage />)
    await screen.findByRole('heading', { name: '功能参数' })
    expect(screen.getByText('共 6 项参数')).toBeInTheDocument()
    expect(screen.getByText('控制 API、实时会话和单集群访问的并发上限。')).toBeInTheDocument()
    expect(screen.getByText('集群、工作负载与事件查询共享的上限。')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^安全扫描/ }))
    expect(screen.getByRole('textbox', { name: /^通用 kube-bench 镜像/ })).toHaveValue('repo/kube-bench:v1')
    expect(screen.getByText('共 9 项参数')).toBeInTheDocument()
    expect(window.location.pathname).toBe('/settings/access')
    expect(window.location.search).toBe('?tab=parameters&section=security')
  })

  it('keeps report model configuration in administrator platform settings', async () => {
    window.history.replaceState({}, '', '/settings/access?tab=parameters&section=reports')
    const fetchMock = mockSettingsFetch()
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<PlatformSettingsPage />)
    expect(await screen.findByRole('textbox', { name: /^模型接口地址/ })).toHaveValue('https://model.internal/v1')
    expect(screen.getByRole('textbox', { name: /^模型名称/ })).toHaveValue('deepseek-chat')
    expect(screen.getByPlaceholderText('已保存，留空沿用')).toHaveAttribute('type', 'password')
    expect(screen.getByText('认证密钥已加密保存；留空表示继续使用原密钥。')).toBeInTheDocument()
    await user.clear(screen.getByRole('textbox', { name: /^模型名称/ }))
    await user.type(screen.getByRole('textbox', { name: /^模型名称/ }), 'deepseek-v3')
    await user.click(screen.getByRole('button', { name: '保存配置' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/reports/config', expect.objectContaining({ method: 'PUT' })))
    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringMatching(/^\/api\/v1\/settings\//), expect.objectContaining({ method: 'PUT' }))
    expect(await screen.findByText('模型配置已保存，下一次生成运行报告时生效。')).toBeInTheDocument()
  })
})
