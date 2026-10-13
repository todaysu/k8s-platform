import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from './App'

describe('App', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows the platform title', () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
    render(<App />)

    expect(
      screen.getByRole('heading', { name: 'Kubernetes 多集群管理平台' }),
    ).toBeInTheDocument()
  })

  it('shows that the backend is connected', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        json: async () => ({ data: { status: 'ok' }, error: null }),
        ok: true,
      }),
    )

    render(<App />)

    expect(await screen.findByText('后端连接正常')).toBeInTheDocument()
  })

  it('shows that the backend is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))

    render(<App />)

    expect(await screen.findByText('后端连接失败')).toBeInTheDocument()
  })
})
