import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import FleetEventsPage from './FleetEventsPage'

const now = new Date().toISOString()
const object = { cluster_id: 'c1', cluster_name: 'test01', namespace: 'myapp', kind: 'Pod', name: 'web-1', uid: 'old-pod-uid', scope: 'business', event_count: 3, occurrence_count: 25, message: 'Error: ImagePullBackOff', last_observed_at: now }
const event = { cluster_id: 'c1', cluster_name: 'test01', environment: 'test', technology_stack: 'TCS', scope: 'business', regarding_kind: 'Pod', regarding_namespace: 'myapp', regarding_name: 'web-1', regarding_uid: 'old-pod-uid', event_uid: 'event-uid', reason: 'Failed', type: 'Warning', message: 'Failed to pull image: proxyconnect tcp: connection refused', occurrence_count: 25, first_observed_at: now, last_observed_at: now, reporting_controller: 'kubelet', reporting_instance: 'node1' }
const issue = { fingerprint: 'image-pull', title: '镜像拉取异常', severity: 'warning', reason: 'Failed', message: event.message, regarding_kind: 'Pod', affected_clusters: 1, affected_objects: 1, event_count: 3, occurrence_count: 25, last_observed_at: now }
const base = { business_configured: true, items: [], objects: [], issues: [issue], collectors: [], failures: [], facets: { kinds: ['Pod'], namespaces: ['myapp'], types: ['Warning'] }, summary: { event_records: 3, problem_groups: 1, warning_groups: 1, affected_clusters: 1, affected_objects: 1, collectors_healthy: 1, collectors_failed: 0, total_clusters: 1 }, storage: { normal_retention_days: 7, warning_retention_days: 30 }, total: 3, pagination: { page: 1, page_size: 25, total_items: 26, total_pages: 2 } }
function setup(status = 'abnormal', failedLive = false, workload = false, context?: object) {
 window.history.replaceState(null, '', context ? `/events?view=events&include_normal=true&object=${encodeURIComponent(JSON.stringify(context))}` : '/events')
 const user = userEvent.setup()
 const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), 'http://localhost')
  let data: unknown = base
  if (url.pathname.endsWith('/events/resource')) {
   if (failedLive) throw new Error('API timeout')
   data = { status, detail: status === 'normal' ? '原资源当前正常；保留历史异常证据' : status === 'replaced' ? '同名资源 UID 已变化' : '独立 Pod，无副本控制器', checked_at: now, uid: status === 'replaced' ? 'new-pod-uid' : object.uid, owners: [], containers: [{ name: 'web', state: 'waiting', reason: 'ImagePullBackOff', restarts: 0, has_logs: false, has_previous_logs: false }], node_name: 'node1' }
  } else if (url.searchParams.get('view') === 'objects' || url.searchParams.get('view') === 'resources') data = { ...base, issues: [], objects: workload ? [{ ...object, kind: 'Deployment', name: 'web', uid: 'deployment-uid', event_count: 6, members: [object, { ...object, name: 'web-2', uid: 'second-pod-uid' }] }] : [object], pagination: { ...base.pagination, total_items: 1, total_pages: 1 } }
  else if (url.searchParams.get('view') === 'events') data = workload && url.searchParams.get('kind') === 'Deployment' ? { ...base, items: null, pagination: { ...base.pagination, total_items: 0, total_pages: 1 } } : { ...base, issues: [], items: [event], pagination: { ...base.pagination, total_items: 1, total_pages: 1 } }
  return { ok: true, json: async () => ({ data, error: null }) }
 })
 vi.stubGlobal('fetch', fetchMock)
 render(<FleetEventsPage clusters={[{ id: 'c1', name: 'test01', environment: 'test', technologyStack: 'TCS' }]} />)
 return { user, fetchMock }
}
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it('defaults to anomaly investigation, paginates on the server, and applies scope to the query', async () => {
 const { user, fetchMock } = setup()
 expect(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' })).toBeInTheDocument()
 expect(screen.queryByRole('grid', { name: '跨集群事件检索' })).not.toBeInTheDocument()
 await user.click(screen.getByRole('button', { name: '下一页' }))
 await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('page=2'))).toBe(true))
 await user.selectOptions(screen.getByLabelText('事件资源归属'), 'business')
 await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('scope=business') && String(url).includes('page=1'))).toBe(true))
})

it('drills into exact objects, exposes full evidence and explains missing logs before container startup', async () => {
 const { user, fetchMock } = setup()
 await user.click(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' }))
 await user.click(await screen.findByRole('button', { name: '核验并排查 web-1' }))
 const drawer = within(screen.getByLabelText('异常排查详情'))
 expect(await drawer.findByText('当前异常')).toBeInTheDocument()
 expect(drawer.getByText('所属：独立 Pod，无副本控制器')).toBeInTheDocument()
 expect(drawer.getByText('容器尚无可用日志')).toBeInTheDocument()
 expect(drawer.queryByRole('button', { name: '查看容器日志' })).not.toBeInTheDocument()
 await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('object_uid=old-pod-uid') && String(url).includes('include_normal=true'))).toBe(true))
 const summary = await drawer.findByText(`Failed · ${event.message}`)
 await user.click(summary)
 expect(drawer.getByText('Event UID')).toBeInTheDocument()
 expect(drawer.getByText('event-uid')).toBeInTheDocument()
 expect(drawer.getByText('25（非所选时间内新增次数）')).toBeInTheDocument()
 await user.selectOptions(drawer.getByLabelText('关联证据时间范围'), '60')
 await user.click(drawer.getByRole('button', { name: '在事件检索中查看此对象' }))
 expect(await screen.findByRole('grid', { name: '跨集群事件检索' })).toBeInTheDocument()
 expect(screen.getByText(/对象上下文：test01/)).toBeInTheDocument()
 expect(screen.getByLabelText('包含 Normal 前后文')).toBeChecked()
 expect(new URLSearchParams(window.location.search).get('object')).toContain('old-pod-uid')
})

it('closes the investigation drawer when clicking outside it and keeps it open on inner clicks', async () => {
  const { user } = setup()
  await user.click(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' }))
  expect(screen.getByLabelText('异常排查详情')).toBeInTheDocument()
  await user.click(within(screen.getByLabelText('异常排查详情')).getByText('查看问题证据样例'))
  expect(screen.getByLabelText('异常排查详情')).toBeInTheDocument()
  await user.click(screen.getByRole('heading', { name: '异常线索' }))
  expect(screen.queryByLabelText('异常排查详情')).not.toBeInTheDocument()
})

it('labels a normal live resource as currently normal while keeping historical evidence', async () => {
 const { user } = setup('normal')
 await user.click(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' }))
 await user.click(await screen.findByRole('button', { name: '核验并排查 web-1' }))
 expect(await screen.findByText('当前正常')).toBeInTheDocument()
 expect(screen.getByText('原资源当前正常；保留历史异常证据')).toBeInTheDocument()
 expect(await screen.findByText(`Failed · ${event.message}`)).toBeInTheDocument()
})

it('does not offer replacement logs or claim recovery when the historical UID has changed', async () => {
 const { user } = setup('replaced')
 await user.click(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' }))
 await user.click(await screen.findByRole('button', { name: '核验并排查 web-1' }))
 expect(await screen.findByText('同名资源已替换')).toBeInTheDocument()
 expect(screen.queryByRole('button', { name: '查看资源详情' })).not.toBeInTheDocument()
 expect(screen.queryByText('当前正常')).not.toBeInTheDocument()
})

it('treats API failure as unverified and still loads persisted evidence', async () => {
 const { user } = setup('unverified', true)
 await user.click(await screen.findByRole('button', { name: '查看影响对象 镜像拉取异常' }))
 await user.click(await screen.findByRole('button', { name: '核验并排查 web-1' }))
 expect(await screen.findByText('当前状态未核验')).toBeInTheDocument()
 expect(await screen.findByText(`Failed · ${event.message}`)).toBeInTheDocument()
 expect(screen.queryByText('当前正常')).not.toBeInTheDocument()
})

it('groups retrieval by resource and expands evidence within the full selected window', async () => {
 const { user, fetchMock } = setup()
 await user.click(screen.getByRole('tab', { name: '事件检索' }))
 const grid = within(await screen.findByRole('grid', { name: '跨集群事件检索' }))
 await grid.findByText('Pod/web-1')
 expect(grid.getAllByText('Pod/web-1')).toHaveLength(1)
 expect(grid.getByText('3 条事件')).toBeInTheDocument()
 expect(grid.queryByText(`Failed · ${event.message}`)).not.toBeInTheDocument()
 await user.click(grid.getByRole('button', { name: '展开事件 web-1' }))
 expect(await grid.findByText(`Failed · ${event.message}`)).toBeInTheDocument()
 const calls = fetchMock.mock.calls.map(([input]) => new URL(String(input), 'http://localhost'))
 const grouped = calls.find(url => url.searchParams.get('view') === 'resources')!
 const raw = calls.find(url => url.searchParams.get('view') === 'events')!
 expect(raw.searchParams.get('object_uid')).toBe('old-pod-uid')
 expect(raw.searchParams.get('since')).toBe(grouped.searchParams.get('since'))
 expect(raw.searchParams.get('until')).toBe(grouped.searchParams.get('until'))
})

it('keeps a resource row expanded while an object context is active', async () => {
  // 对象上下文的时间窗以页面查询截止时间为上限，早先用 Date.now() 会让查询串每次渲染都变，
  // 而查询串曾进入列表行的 key，导致行被重新挂载、展开状态瞬间丢失（点击看起来没反应）。
  const { user } = setup('abnormal', false, true, { ...object, message: '' })
  const grid = within(await screen.findByRole('grid', { name: '跨集群事件检索' }))
  await grid.findByText('Deployment/web')
  await user.click(grid.getByRole('button', { name: '展开事件 web' }))
  const row = grid.getByText('Deployment/web').closest('tr') as HTMLElement
  expect(within(row).getByText('收起事件')).toBeInTheDocument()
  expect(document.querySelectorAll('.ks-event-group-evidence').length).toBeGreaterThan(0)
  await waitFor(() => expect(within(row).getByText('收起事件')).toBeInTheDocument())
})

it('merges Deployment pods and exposes individual evidence and investigation actions', async () => {
 const { user, fetchMock } = setup('abnormal', false, true)
 await user.click(screen.getByRole('tab', { name: '事件检索' }))
 expect(await screen.findByText('Deployment/web')).toBeInTheDocument()
 expect(screen.getByText('涉及 2 个 Pod')).toBeInTheDocument()
 expect(screen.queryByText('Pod/web-1')).not.toBeInTheDocument()
 await user.click(screen.getByRole('button', { name: '展开事件 web' }))
 expect(await screen.findByText('Pod/web-1')).toBeInTheDocument()
 expect(await screen.findByText('Pod/web-2')).toBeInTheDocument()
 await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).includes('object_uid=second-pod-uid'))).toBe(true))
 await user.click(screen.getByRole('button', { name: '排查 web-2' }))
 expect(await screen.findByText('当前异常')).toBeInTheDocument()
})
