import { fireEvent, render, screen, within, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ArthasPage from './ArthasPage'

afterEach(() => { vi.unstubAllGlobals() })

describe('Arthas guided session page', () => {
  it('shows the troubleshooting guide next to the main workspace, not below it', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/capabilities')) return { ok: true, json: async () => ({ data: { pod_exec_enabled: true }, error: null }) }
      if (url.endsWith('/clusters/c1/pods')) return { ok: true, json: async () => ({ data: [], error: null }) }
      return { ok: true, json: async () => ({ data: [], error: null }) }
    }))
    render(<ArthasPage clusters={[{ id: 'c1', name: 'cluster-a' }]} onBack={vi.fn()} />)
    expect(screen.getByRole('heading', { name: 'Arthas 诊断' })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '终端会在启动后显示在这里' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '你遇到了什么问题？' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '启动 Arthas 会话' })).toBeDisabled()
    const target = screen.getByRole('heading', { name: '选择目标' })
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
    render(<ArthasPage clusters={[{id:'c1',name:'one'},{id:'c2',name:'two'}]} onBack={vi.fn()} />)
    const pods = screen.getByRole('listbox',{name:'选择 Arthas Pod'})
    expect(pods).toBeDisabled()
    const ns = screen.getByRole('combobox',{name:'选择 Arthas 命名空间'})
    await within(ns).findByRole('option',{name:'myapp'})
    fireEvent.change(ns,{target:{value:'myapp'}})
    expect(within(pods).getByRole('option',{name:/java-a/})).toBeInTheDocument()
    expect(within(pods).queryByRole('option',{name:/java-b/})).not.toBeInTheDocument()
    fireEvent.change(pods,{target:{value:'myapp/java-a'}})
    await waitFor(()=>expect(screen.getByRole('combobox',{name:'选择 Arthas 容器'})).toHaveValue('java'))
    fireEvent.change(ns,{target:{value:'other'}})
    expect(pods).toHaveValue('')
    expect(within(pods).queryByRole('option',{name:/java-a/})).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox',{name:'选择 Arthas 集群'}),{target:{value:'c2'}})
    expect(ns).toHaveValue('')
    expect(pods).toBeDisabled()
  })

})
