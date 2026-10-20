import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SetupPage from './SetupPage'

afterEach(() => { vi.unstubAllGlobals() })

describe('database setup page', () => {
  it('loads in-cluster defaults and renders the setup form', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      json: async () => ({
        data: {
          setup_required: true,
          default_host: 'platform-mysql.ftc-platform.svc',
          default_database: 'ftc_platform',
          default_port: '3306',
          default_user: 'ftc',
        },
        error: null,
      }),
    })))
    render(<SetupPage />)
    expect(await screen.findByRole('heading', { name: '初始化平台数据库' })).toBeInTheDocument()
    expect(await screen.findByDisplayValue('platform-mysql.ftc-platform.svc')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '测试连接' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '初始化数据库' })).toBeInTheDocument()
  })
})
