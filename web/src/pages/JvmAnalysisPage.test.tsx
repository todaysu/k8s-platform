import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import JvmAnalysisPage from './JvmAnalysisPage'

describe('JVM analysis page', () => {
  it('analyzes the built-in sample and shows findings', async () => {
    const user = userEvent.setup()
    render(<JvmAnalysisPage onBack={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'JVM 内存分析' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看示例' }))
    expect(await screen.findByText(/发现 \d+ 个/)).toBeInTheDocument()
    expect(screen.getAllByText(/Full GC/).length).toBeGreaterThan(0)
  })
})
