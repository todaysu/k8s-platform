import { fireEvent, render, screen, within, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ArthasPage from './ArthasPage'

const terminalCalls = vi.hoisted(() => ({ stop: vi.fn(), mounted: vi.fn() }))
vi.mock('../components/ArthasTerminal', async () => {
  const { forwardRef, useImperativeHandle, useEffect } = await import('react')
  return { default: forwardRef(function TerminalStub(_props, ref) {
    useImperativeHandle(ref, () => ({ stop: terminalCalls.stop, clear: vi.fn(), send: vi.fn(), interrupt: vi.fn() }))
    useEffect(() => { terminalCalls.mounted() }, [])
    return <div aria-label="测试终端">会话输出</div>
  }) }
})

beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} }) })

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
})

describe('Arthas guided session page', () => {
  it('keeps the troubleshooting guide optional and opens it without changing the workspace', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/capabilities')) return { ok: true, json: async () => ({ data: { pod_exec_enabled: true }, error: null }) }
      if (url.endsWith('/clusters/c1/pods')) return { ok: true, json: async () => ({ data: [], error: null }) }
      return { ok: true, json: async () => ({ data: [], error: null }) }
    }))
    render(<ArthasPage clusters={[{ id: 'c1', name: 'cluster-a' }]} />)
    expect(screen.queryByRole('heading', { name: 'Arthas 诊断' })).not.toBeInTheDocument()
    expect(screen.queryByText('终端会在启动后显示在这里')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '你遇到了什么问题？' })).not.toBeInTheDocument()
    const guideToggle = screen.getByRole('button', { name: /^故障排查向导$/ })
    expect(guideToggle).toHaveAttribute('aria-expanded','false')
    fireEvent.click(guideToggle)
    expect(guideToggle).toHaveAttribute('aria-expanded','true')
    expect(screen.getByRole('heading', { name: '你遇到了什么问题？' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '启动终端' })).toBeDisabled()
    const target = screen.getByLabelText('诊断目标')
    const workspace = screen.getByLabelText('Arthas 工作区')
    const guide = screen.getByLabelText('Arthas 故障排查向导')
    expect(target.compareDocumentPosition(workspace) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(workspace.compareDocumentPosition(guide) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.queryByText('故障排查向导', { selector: 'summary' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /发布后代码未生效/ }))
    expect(screen.getByRole('heading', { name: '1. 确认类来自哪个包' })).toBeInTheDocument()
  })
  it('cascades cluster, namespace and Pod without retaining a stale target', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const data = url.endsWith('/capabilities') ? { pod_exec_enabled:true } : url.endsWith('/pods') ? [
        {namespace:'myapp',name:'java-a',phase:'Running',ready_containers:1,total_containers:1},
        {namespace:'other',name:'java-b',phase:'Running',ready_containers:1,total_containers:1},
      ] : { containers:[{name:'java'}] }
      return {ok:true,json:async()=>({data,error:null})}
    }))
    render(<ArthasPage clusters={[{id:'c1',name:'one'},{id:'c2',name:'two'}]} />)
    const trigger = screen.getByRole('combobox',{name:'选择 Arthas Pod'})
    expect(trigger).toBeDisabled()
    expect(trigger).toHaveTextContent('先选择命名空间')
    const ns = screen.getByRole('combobox',{name:'选择 Arthas 命名空间'})
    await within(ns).findByRole('option',{name:'myapp'})
    fireEvent.change(ns,{target:{value:'myapp'}})
    expect(trigger).not.toBeDisabled()
    fireEvent.click(trigger)
    const listbox = await screen.findByRole('listbox',{name:'选择 Arthas Pod'})
    expect(within(listbox).getByRole('option',{name:/java-a/})).toBeInTheDocument()
    expect(within(listbox).queryByRole('option',{name:/java-b/})).not.toBeInTheDocument()
    const wheel = new WheelEvent('wheel',{deltaY:120,bubbles:true,cancelable:true})
    screen.getByRole('textbox',{name:'搜索 Pod'}).dispatchEvent(wheel)
    expect(wheel.defaultPrevented).toBe(true)
    expect(listbox.scrollTop).toBe(120)
    fireEvent.click(within(listbox).getByRole('option',{name:/java-a/}))
    await waitFor(()=>expect(screen.getByRole('combobox',{name:'选择 Arthas 容器'})).toHaveValue('java'))
    expect(trigger).toHaveTextContent('java-a')
    fireEvent.change(ns,{target:{value:'other'}})
    expect(trigger).toHaveTextContent('选择 Pod')
    expect(screen.queryByRole('listbox',{name:'选择 Arthas Pod'})).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox',{name:'选择 Arthas 集群'}),{target:{value:'c2'}})
    expect(trigger).toBeDisabled()
    expect(trigger).toHaveTextContent('先选择命名空间')
  })
  it('keeps the target locked until switching is confirmed, without restarting the terminal on cancel', async () => {
    terminalCalls.stop.mockClear(); terminalCalls.mounted.mockClear()
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      const data = url.endsWith('/capabilities') ? {pod_exec_enabled:true} : url.endsWith('/pods') ? [{namespace:'myapp',name:'java-a',phase:'Running'}] : {containers:[{name:'java'}]}
      return {ok:true,json:async()=>({data,error:null})}
    }))
    render(<ArthasPage clusters={[{id:'c1',name:'one'}]} />)
    const ns = screen.getByRole('combobox',{name:'选择 Arthas 命名空间'})
    await within(ns).findByRole('option',{name:'myapp'})
    fireEvent.change(ns,{target:{value:'myapp'}})
    fireEvent.click(screen.getByRole('combobox',{name:'选择 Arthas Pod'}))
    fireEvent.click(await screen.findByRole('option',{name:/java-a/}))
    await waitFor(()=>expect(screen.getByRole('combobox',{name:'选择 Arthas 容器'})).toHaveValue('java'))
    fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.click(screen.getByRole('button',{name:'启动终端'}))
    expect(ns).toBeDisabled()
    expect(screen.getByRole('combobox',{name:'选择 Arthas Pod'})).toHaveTextContent('java-a')
    expect(screen.getByLabelText('测试终端')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button',{name:'切换目标'}))
    fireEvent.click(screen.getByRole('button',{name:'取消'}))
    expect(terminalCalls.stop).not.toHaveBeenCalled()
    expect(terminalCalls.mounted).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button',{name:'切换目标'}))
    fireEvent.click(screen.getByRole('button',{name:'结束并切换'}))
    expect(terminalCalls.stop).toHaveBeenCalledTimes(1)
    expect(ns).toBeDisabled()
    await waitFor(()=>expect(ns).not.toBeDisabled(),{timeout:2000})
    expect(screen.queryByLabelText('测试终端')).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox')).not.toBeChecked()
  })

  it('prompts to configure the Arthas offline package when it is missing', async () => {
    const payload = (data: unknown) => ({ ok: true, json: async () => ({ data, error: null }) })
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/settings')) return payload({ security: {}, java: { arthas_url: '' }, updated_at: {} })
      if (url.endsWith('/capabilities')) return payload({ pod_exec_enabled: true })
      return payload([])
    }))
    render(<ArthasPage clusters={[{ id: 'c1', name: 'cluster-a' }]} />)

    expect(await screen.findByText('Arthas 离线包地址尚未配置')).toBeInTheDocument()
    fireEvent.click(screen.getByText('前往参数配置'))
    expect(window.location.pathname).toBe('/settings/access')
    expect(window.location.search).toBe('?tab=parameters&section=java')
  })

})
