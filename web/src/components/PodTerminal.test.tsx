import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import PodTerminal from './PodTerminal'

const mocks = vi.hoisted(() => ({ opened: null as null | HTMLElement }))
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    loadAddon() {}
    open(element: HTMLElement) { mocks.opened = element }
    clear() {}
    write() {}
    writeln() {}
    focus() {}
    onData() { return { dispose() {} } }
    onResize() { return { dispose() {} } }
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('../api/clusters', () => ({
  fetchPlatformCapabilities: vi.fn(() => Promise.resolve({ pod_exec_enabled: true })),
}))

afterEach(() => { vi.unstubAllGlobals() })

it('mounts xterm into a padding-free inner view so the bottom row is not clipped', () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  render(<PodTerminal clusterId="a" containers={['app']} namespace="ops" podName="web-0" running />)
  // FitAddon 只按宿主的 border-box 高度换算行数，宿主带 padding 会多算行数，导致终端底部被裁掉
  expect(mocks.opened?.className).toContain('ks-pod-terminal-view')
  expect(mocks.opened?.parentElement?.className).toContain('ks-pod-terminal')
})

it('toggles fullscreen while keeping the terminal mounted', async () => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const user = userEvent.setup()
  render(<PodTerminal clusterId="a" containers={['app']} namespace="ops" podName="web-0" running />)
  const terminal = screen.getByLabelText('Pod 交互式终端')
  expect(screen.getByText('ops')).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'web-0' })).not.toBeInTheDocument()
  expect(screen.queryByText('高权限操作')).not.toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: '容器终端' })).not.toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: '全屏' }))
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveAttribute('aria-label', '全屏容器终端 · ops / web-0')
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(document.body.style.overflow).toBe('hidden')
  expect(document.body).toHaveClass('ks-terminal-fullscreen')
  expect(screen.getByLabelText('Pod 交互式终端')).toBe(terminal)
  expect(screen.getByRole('button', { name: '退出全屏' })).toBeInTheDocument()

  await user.click(screen.getByRole('button', { name: '退出全屏' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Pod 交互式终端')).toBe(terminal)
  await user.click(screen.getByRole('button', { name: '全屏' }))
  expect(screen.getByRole('dialog')).toBeInTheDocument()

  const textarea = document.createElement('textarea')
  document.body.appendChild(textarea)
  textarea.focus()
  await user.keyboard('{Escape}')
  textarea.remove()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(document.body.style.overflow).toBe('')
  expect(document.body).not.toHaveClass('ks-terminal-fullscreen')
  expect(screen.getByLabelText('Pod 交互式终端')).toBe(terminal)
})
