import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import CapacityClassificationDialog from './CapacityClassificationDialog'

const data = {
  classification: { id: 1, business_namespaces: ['myapp', 'future-app'], common_platform_namespaces: ['kube-system'], stack_platform_namespaces: { TCS: ['tcs-system'] }, note: '' },
  discovered: [
    { namespace: 'myapp', technology_stack: 'TCS', class: 'business', cluster_count: 1, pods: 0, cpu_requests_milli: 0, memory_requests_bytes: 0 },
    { namespace: 'addon', technology_stack: 'TCS', class: 'platform', cluster_count: 1, pods: 1, cpu_requests_milli: 10, memory_requests_bytes: 0 },
    { namespace: 'addon', technology_stack: 'OCP', class: 'platform', cluster_count: 2, pods: 2, cpu_requests_milli: 20, memory_requests_bytes: 0 },
  ],
}
afterEach(() => { cleanup(); vi.unstubAllGlobals() })
function setup() {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data, error: null }) })
  vi.stubGlobal('fetch', fetchMock)
  const onSaved = vi.fn()
  render(<CapacityClassificationDialog onClose={vi.fn()} onSaved={onSaved} open />)
  return { fetchMock, onSaved, user: userEvent.setup() }
}

it('preserves saved and future business names, merges discovered stacks, and saves only the business list', async () => {
  const { fetchMock, onSaved, user } = setup()
  expect(await screen.findByLabelText('业务 Namespace')).toHaveValue('myapp\nfuture-app')
  expect(screen.getByText('future-app（尚未发现）')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: '加入业务 addon' })).toHaveLength(1)
  expect(screen.getByText('30 m')).toBeInTheDocument()
  expect(screen.queryByLabelText('按技术栈查看 Namespace')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '加入业务 addon' }))
  expect(screen.getByText('没有可加入的 Namespace')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '移出业务 myapp' }))
  expect(screen.getByRole('button', { name: '加入业务 myapp' })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '保存业务清单' }))
  await waitFor(() => expect(onSaved).toHaveBeenCalledOnce())
  const call = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(call![1].body)).toEqual({ business_namespaces: ['addon', 'future-app'], note: '' })
})

it('validates namespace names, normalizes separators and duplicates, and supports an empty business list', async () => {
  const { fetchMock, user } = setup()
  const input = await screen.findByLabelText('业务 Namespace')
  await user.clear(input)
  await user.type(input, 'Bad_Name')
  expect(screen.getByText('Namespace 名称无效')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '保存业务清单' })).toBeDisabled()
  await user.clear(input)
  await user.type(input, 'myapp, orders，myapp\nfuture-app')
  expect(screen.queryByText('Namespace 名称无效')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '保存业务清单' }))
  await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true))
  const call = fetchMock.mock.calls.find(([, init]) => init?.method === 'PUT')
  expect(JSON.parse(call![1].body).business_namespaces).toEqual(['future-app', 'myapp', 'orders'])
  await user.clear(input)
  expect(screen.getByText('尚未设置业务 Namespace')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '保存业务清单' })).toBeEnabled()
})
