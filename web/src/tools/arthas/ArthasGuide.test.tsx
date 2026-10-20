import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import ArthasGuide from './ArthasGuide'
import { buildMethodCommand } from './commands'

describe('Arthas troubleshooting guide', () => {
  it('does not execute on scenario selection and gates commands before attach', () => {
    const send = vi.fn()
    render(<ArthasGuide ready={false} onSend={send} />)
    fireEvent.click(screen.getByRole('button', { name: /应用卡住/ }))
    expect(screen.getByText(/此命令不能覆盖所有锁类型/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '执行这一步' })).toBeDisabled()
    expect(send).not.toHaveBeenCalled()
  })
  it('generates a bounded trace for an exact method and resets steps when changing scenario', () => {
    const send = vi.fn()
    render(<ArthasGuide ready onSend={send} />)
    fireEvent.click(screen.getByRole('button', { name: /接口响应慢/ }))
    fireEvent.click(screen.getByRole('button', { name: '看下一步' }))
    expect(screen.getByRole('button', { name: '执行这一步' })).toBeDisabled()
    fireEvent.change(screen.getByRole('textbox', { name: '完整类名' }), { target: { value: 'com.example.OrderService' } })
    fireEvent.change(screen.getByRole('textbox', { name: '方法名' }), { target: { value: 'submit' } })
    fireEvent.click(screen.getByRole('button', { name: '执行这一步' }))
    expect(send).toHaveBeenCalledWith("trace com.example.OrderService submit '#cost > 100' -n 5")
    fireEvent.click(screen.getByRole('button', { name: /内存上涨/ }))
    expect(screen.getByRole('heading', { name: '1. 查看内存分布' })).toBeInTheDocument()
  })
  it('supports release verification and bounded method monitoring', () => {
    expect(buildMethodCommand('sc', 'com.example.OrderService', '', '')).toBe('sc -d com.example.OrderService')
    expect(buildMethodCommand('jad', 'com.example.OrderService', '', '')).toBe('jad com.example.OrderService')
    expect(buildMethodCommand('sm', 'com.example.OrderService', 'submit', '')).toBe('sm -d com.example.OrderService submit')
    expect(buildMethodCommand('monitor', 'com.example.OrderService', 'submit', '')).toBe('monitor -c 5 -n 3 com.example.OrderService submit')
    expect(buildMethodCommand('sc', 'com.example.*', '', '')).toBeNull()
    expect(buildMethodCommand('unknown', 'com.example.Foo', 'run', '100')).toBeNull()
  })
  it('shows community cases with methods and original author links', () => {
    render(<ArthasGuide ready={false} onSend={vi.fn()} />)
    fireEvent.click(screen.getByRole('button',{name:/接口响应慢/}))
    const cases = screen.getByRole('region',{name:'用户排障案例'})
    expect(within(cases).getByText('没有慢 SQL，接口却累计耗时约 2 秒')).toBeInTheDocument()
    expect(within(cases).getAllByRole('link',{name:'阅读用户原文'}).some(link=>link.getAttribute('href')?.includes('qiaoyihang'))).toBe(true)
    expect(within(cases).queryByRole('link',{name:/官方/})).not.toBeInTheDocument()
  })
  it('rejects wildcard, expression and command injection inputs', () => {
    for (const value of ['com.Foo;stop', 'com.*', 'com.Foo\nstop']) expect(buildMethodCommand('watch', value, 'run', '100')).toBeNull()
    expect(buildMethodCommand('trace', 'com.Foo', '*', '100')).toBeNull()
    expect(buildMethodCommand('trace', 'com.Foo', 'run', '0')).toBeNull()
    expect(buildMethodCommand('trace', 'com.Foo', 'run', '1;stop')).toBeNull()
    expect(buildMethodCommand('watch', 'com.Foo$Inner', 'run', '100')).toBe('watch com.Foo$Inner run throwExp -e -n 5 -x 1')
  })
})
