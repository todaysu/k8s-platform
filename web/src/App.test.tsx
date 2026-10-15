import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import App from './App'

describe('cluster management UI', () => {
  it('shows the cluster management overview', () => {
    render(<App />)

    expect(
      screen.getByRole('heading', { name: '集群管理' }),
    ).toBeInTheDocument()
    expect(screen.getByText('prd-shanghai-01')).toBeInTheDocument()
    expect(screen.getByText('dev-beijing-03')).toBeInTheDocument()
  })

  it('opens the add cluster dialog', async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(screen.getByRole('button', { name: '添加集群' }))

    expect(
      screen.getByRole('heading', { name: '纳管新集群' }),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('集群名称')).toBeInTheDocument()
    expect(screen.getByText('上传文件')).toBeInTheDocument()
    expect(screen.getByText('粘贴内容')).toBeInTheDocument()
  })

  it('filters clusters by name', async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('搜索集群'), 'prd-shanghai')

    expect(screen.getByText('prd-shanghai-01')).toBeInTheDocument()
    expect(screen.queryByText('dev-beijing-03')).not.toBeInTheDocument()
  })
})
