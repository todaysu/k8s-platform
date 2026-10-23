import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SetupPage from './SetupPage'

const preflight = {
  namespace: 'ftc-platform',
  nodes: [
    { name: 'node-a', arch: 'amd64', os: 'linux', cpu_millis: 8000, memory_bytes: 16 * 1024 * 1024 * 1024, internal_ips: ['10.0.0.11'], schedulable: true },
    { name: 'node-b', arch: 'amd64', os: 'linux', cpu_millis: 8000, memory_bytes: 16 * 1024 * 1024 * 1024, internal_ips: ['10.0.0.12'], schedulable: true },
    { name: 'node-c', arch: 'arm64', os: 'linux', cpu_millis: 8000, memory_bytes: 16 * 1024 * 1024 * 1024, internal_ips: ['10.0.0.13'], schedulable: true },
  ],
  storage_classes: [],
  pxc_available: true,
  runtime_secret_exists: false,
  warnings: [],
  errors: [],
}

afterEach(() => { vi.unstubAllGlobals() })

describe('initialization wizard', () => {
  it('runs preflight and advances to external MySQL', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: string) => ({
      ok: true,
      json: async () => ({ data: input === '/api/v1/setup/install' ? { state: 'idle', steps: [] } : preflight, error: null }),
    })))
    const user = userEvent.setup()
    render(<SetupPage />)

    expect(await screen.findByRole('heading', { name: '准备部署 FTC 集群运维平台' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('初始化凭证'), 'setup-token-value')
    await user.click(screen.getByRole('button', { name: '检查环境' }))

    expect(await screen.findByText('node-a')).toBeInTheDocument()
		expect(screen.getByText(/平台 HTTPS \+ NodePort/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '下一步' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '下一步' }))
    expect(await screen.findByRole('heading', { name: '配置外部 MySQL' })).toBeInTheDocument()
  })

  it('blocks the next step when preflight reports errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: string) => ({
      ok: true,
      json: async () => ({ data: input === '/api/v1/setup/install' ? { state: 'idle', steps: [] } : { ...preflight, errors: ['没有可调度节点。'] }, error: null }),
    })))
    const user = userEvent.setup()
    render(<SetupPage />)
    await user.type(screen.getByLabelText('初始化凭证'), 'setup-token-value')
    await user.click(screen.getByRole('button', { name: '检查环境' }))
    expect(await screen.findByText('没有可调度节点。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
  })
})
