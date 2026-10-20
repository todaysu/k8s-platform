import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from './App'

const emptyClusterResponse = {
  json: async () => ({ data: [], error: null }),
  ok: true,
}

const realClusters = [
  {
    api_latency_ms: 18, api_server: 'https://cluster-a.example.test', authentication_type: 'Bearer Token',
    connection_status: 'connected', context_name: 'default', created_at: '2026-10-16T01:00:00Z',
    core_components_ready: 3, core_components_total: 3, cpu_utilization: 23.4,
    environment: '生产环境', id: 'cluster-a', instances_ready: 12, instances_total: 12,
    kubernetes_version: 'v1.32.4', last_checked_at: '2026-10-16T01:00:00Z',
    memory_utilization: 51.2, metrics_available: true, name: 'cluster-a', nodes_ready: 3,
    nodes_total: 3, technology_stack: 'TCS', worker_cores: 16, worker_nodes: 2,
  },
  {
    api_latency_ms: 25, api_server: 'https://cluster-b.example.test', authentication_type: 'Bearer Token',
    connection_status: 'connected', context_name: 'default', created_at: '2026-10-16T01:00:00Z',
    core_components_ready: 2, core_components_total: 2, cpu_utilization: null,
    environment: '测试环境', id: 'cluster-b', instances_ready: 8, instances_total: 9,
    kubernetes_version: 'v1.31.7', last_checked_at: '2026-10-16T01:00:00Z',
    memory_utilization: null, metrics_available: false, name: 'cluster-b', nodes_ready: 2,
    nodes_total: 2, technology_stack: 'CMBK8S', worker_cores: 8, worker_nodes: 2,
  },
]

const clusterNodes = [
  {
    cpu_cores: 8,
    created_at: '2026-10-01T00:00:00Z',
    instance_type: 'physical',
    kubernetes_version: 'v1.32.4',
    memory_bytes: 17179869184,
    name: 'worker-1',
    region: 'cn-east',
    roles: ['worker'],
    schedulable: true,
    status: 'Ready',
    zone: 'cn-east-1a',
  },
]

const clusterCapacity = {
  baseline: null,
  baseline_review: { status: 'not_required' },
  business: { cpu_limits_milli: 12000, cpu_requests_milli: 8000, cpu_usage_milli: null, memory_limits_bytes: 42949672960, memory_requests_bytes: 32212254720, memory_usage_bytes: null, pods_without_cpu_requests: 2, pods_without_memory_requests: 1 },
  classification_version_id: 1,
  collected_at: '2026-10-17T10:00:00Z',
  comparison: { cpu_allocatable_change_percent: 0, memory_allocatable_change_percent: 0, platform_cpu_request_change_percent: 0, platform_memory_request_change_percent: 0, status: 'not_set' },
  cpu_allocatable_milli: 16000,
  memory_allocatable_bytes: 68719476736,
  metrics_available: false,
  namespaces: [
    { class: 'business', cpu_limits_milli: 12000, cpu_requests_milli: 8000, cpu_usage_milli: null, memory_limits_bytes: 42949672960, memory_requests_bytes: 32212254720, memory_usage_bytes: null, namespace: 'app', pods: 12, pods_without_cpu_requests: 2, pods_without_memory_requests: 1 },
    { class: 'platform', cpu_limits_milli: 4000, cpu_requests_milli: 2000, cpu_usage_milli: null, memory_limits_bytes: 8589934592, memory_requests_bytes: 4294967296, memory_usage_bytes: null, namespace: 'kube-system', pods: 8 },
  ],
  pending: { cpu_limits_milli: 0, cpu_requests_milli: 1000, cpu_usage_milli: null, memory_limits_bytes: 0, memory_requests_bytes: 1073741824, memory_usage_bytes: null },
  platform: { cpu_limits_milli: 4000, cpu_requests_milli: 2000, cpu_usage_milli: null, memory_limits_bytes: 8589934592, memory_requests_bytes: 4294967296, memory_usage_bytes: null },
  safety_reserve_percent: 15,
  system_namespaces: ['kube-system', 'cmb-monitoring'],
  unclassified: { cpu_limits_milli: 0, cpu_requests_milli: 0, cpu_usage_milli: null, memory_limits_bytes: 0, memory_requests_bytes: 0, memory_usage_bytes: null, pods_without_cpu_requests: 0, pods_without_memory_requests: 0 },
  worker_nodes: 2,
}

const fleetCapacity = {
  collected_at: '2026-10-17T10:00:00Z',
  failures: [{ cluster_id: 'cluster-b', cluster_name: 'cluster-b', code: 'CONNECTION_FAILED', environment: '测试环境', technology_stack: 'CMBK8S' }],
  items: [{ cluster_id: 'cluster-a', cluster_name: 'cluster-a', environment: '生产环境', technology_stack: 'TCS', capacity: clusterCapacity }],
}

const fleetCapacityByPool = {
  collected_at: '2026-10-17T10:00:00Z',
  failures: [],
  items: [
    fleetCapacity.items[0],
    {
      cluster_id: 'cluster-cmb', cluster_name: 'cluster-cmb', environment: '生产环境', technology_stack: 'CMBK8S',
      capacity: {
        ...clusterCapacity,
        business: { ...clusterCapacity.business, cpu_requests_milli: 2000, memory_requests_bytes: 4294967296 },
        cpu_allocatable_milli: 12000,
        memory_allocatable_bytes: 51539607552,
        pending: { ...clusterCapacity.pending, cpu_requests_milli: 0, memory_requests_bytes: 0 },
        platform: { ...clusterCapacity.platform, cpu_requests_milli: 1500, memory_requests_bytes: 3221225472 },
      },
    },
    {
      cluster_id: 'cluster-ocp', cluster_name: 'cluster-ocp', environment: '生产环境', technology_stack: 'OCP',
      capacity: {
        ...clusterCapacity,
        business: { ...clusterCapacity.business, cpu_requests_milli: 1000, memory_requests_bytes: 2147483648 },
        cpu_allocatable_milli: 8000,
        memory_allocatable_bytes: 34359738368,
        pending: { ...clusterCapacity.pending, cpu_requests_milli: 0, memory_requests_bytes: 0 },
        platform: { ...clusterCapacity.platform, cpu_requests_milli: 1000, memory_requests_bytes: 2147483648 },
      },
    },
    {
      cluster_id: 'cluster-uat', cluster_name: 'cluster-uat', environment: 'UAT', technology_stack: 'TCS',
      capacity: { ...clusterCapacity, cpu_allocatable_milli: 6000, memory_allocatable_bytes: 25769803776 },
    },
  ],
}

const capacityClassification = {
  classification: {
    business_namespaces: ['app'], common_platform_namespaces: ['kube-system'], created_at: '2026-10-17T10:00:00Z', id: 1, note: '初始规则',
    stack_platform_namespaces: { CMBK8S: ['cmb-monitoring'], OCP: [], TCS: ['tcs-system'] },
  },
  discovered: [
    { class: 'business', cluster_count: 1, cpu_requests_milli: 8000, first_seen_at: '2026-10-17T10:00:00Z', memory_requests_bytes: 32212254720, namespace: 'app', pods: 12, technology_stack: 'TCS' },
    { class: 'unclassified', cluster_count: 1, cpu_requests_milli: 500, first_seen_at: '2026-10-18T10:00:00Z', memory_requests_bytes: 536870912, namespace: 'new-system', pods: 2, technology_stack: 'TCS' },
  ],
}

const clusterWorkloads = [
  {
    available: 2, created_at: '2026-10-01T00:00:00Z', desired: 3,
    images: ['registry.example/web:v2'], kind: 'Deployment', last_updated_at: '2026-10-16T01:00:00Z',
    name: 'web', namespace: 'app', pods_ready: 2, pods_total: 3, ready: 2,
    restart_count: 4, status: 'progressing', status_reason: '正在发布或等待实例就绪', updated: 3,
  },
  {
    available: 2, created_at: '2026-10-02T00:00:00Z', desired: 2,
    images: ['redis:7'], kind: 'StatefulSet', last_updated_at: '2026-10-02T00:00:00Z',
    name: 'cache', namespace: 'data', pods_ready: 2, pods_total: 2, ready: 2,
    restart_count: 0, status: 'healthy', status_reason: '副本均已就绪', updated: 2,
  },
]

const clusterPods = [
  {
    created_at: '2026-10-16T00:30:00Z', images: ['registry.example/web:v2'], name: 'web-abc-1',
    namespace: 'app', node_name: 'worker-1', owner_kind: 'Deployment', owner_name: 'web', phase: 'Running',
    pod_ip: '10.0.0.10', ready_containers: 0, restart_count: 4, started_at: '2026-10-16T00:31:00Z',
    status_reason: 'CrashLoopBackOff', total_containers: 1,
  },
  {
    created_at: '2026-10-16T00:40:00Z', images: ['busybox:1.36'], name: 'manual-pod', namespace: 'app',
    node_name: 'worker-1', owner_kind: '', owner_name: '', phase: 'Running', pod_ip: '10.0.0.11',
    ready_containers: 1, restart_count: 0, started_at: '2026-10-16T00:41:00Z', status_reason: 'Running', total_containers: 1,
  },
]

const webWorkloadDetail = {
  ...clusterWorkloads[0],
  labels: { app: 'web' },
  pods: [{
    created_at: '2026-10-16T00:30:00Z', name: 'web-abc-1', node_name: 'worker-1', phase: 'Running',
    pod_ip: '10.0.0.10', ready_containers: 0, restart_count: 4, started_at: '2026-10-16T00:31:00Z',
    status_reason: 'CrashLoopBackOff', total_containers: 1,
  }],
}

const webPodDetail = {
  ...clusterPods[0],
  conditions: [{ last_transition_time: '2026-10-16T00:31:00Z', message: 'container is waiting', reason: 'ContainersNotReady', status: 'False', type: 'Ready' }],
  containers: [{ image: 'registry.example/web:v2', name: 'web', ready: false, restart_count: 4, state: 'Waiting', status_reason: 'CrashLoopBackOff' }],
  host_ip: '192.168.1.10', labels: { app: 'web' }, qos_class: 'Burstable', service_account: 'web-sa',
}

describe('cluster management UI', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    window.history.replaceState(null, '', '/')
  })

  it('shows the cluster management overview without mock clusters', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(emptyClusterResponse))
    render(<App />)

    expect(screen.getByRole('heading', { name: '多集群健康概览' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '资源健康' })).toBeInTheDocument()
    expect(screen.getByText('核心组件正常')).toBeInTheDocument()
    expect(screen.getByText('Kubernetes 版本')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /所有集群/ })).not.toBeInTheDocument()
    expect(screen.queryByText('业务全景')).not.toBeInTheDocument()
    expect(screen.queryByRole('grid', { name: '集群列表' })).not.toBeInTheDocument()
  })

  it('navigates between overview and the cluster list from the sidebar', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      json: async () => ({ data: realClusters, error: null }), ok: true,
    }))
    render(<App />)

    await screen.findByRole('heading', { name: '多集群健康概览' })

    const clustersNavigation = screen.getByText('集群', { selector: '.ks-nav-label' })
    const overviewNavigation = screen.getByText('概览', { selector: '.ks-nav-label' })

    await user.click(clustersNavigation)
    expect(window.location.pathname).toBe('/clusters')
    expect(clustersNavigation.closest('.pf-m-current')).not.toBeNull()
    expect(await screen.findByRole('heading', { name: '集群' })).toBeInTheDocument()
    expect(screen.getByText('cluster-a')).toBeInTheDocument()

    await user.click(overviewNavigation)
    expect(window.location.pathname).toBe('/')
    expect(overviewNavigation.closest('.pf-m-current')).not.toBeNull()
    expect(screen.queryByRole('grid', { name: '集群列表' })).not.toBeInTheDocument()
  })

  it('opens the unified inspection center from the sidebar and overview attention card', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      if (String(input).endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacity, error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: '需要关注' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /查看全部问题/ }))

    expect(window.location.pathname).toBe('/inspections')
    expect(await screen.findByRole('heading', { name: '主动巡检' })).toBeInTheDocument()
    expect(screen.getByText('当前问题')).toBeInTheDocument()
    expect(screen.getByRole('grid', { name: '巡检发现列表' })).toBeInTheDocument()
    expect(screen.getByText('实例未全部就绪')).toBeInTheDocument()

    await user.click(screen.getByText('概览', { selector: '.ks-nav-label' }))
    await user.click(screen.getByText('巡检分析', { selector: '.ks-nav-label' }))
    expect(window.location.pathname).toBe('/inspections')
  })

  it('shows fleet capacity conclusions on the overview and opens capacity details', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      if (String(input).endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacity, error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: '资源池容量' })).toBeInTheDocument()
    expect(screen.getByText('容量资源池')).toBeInTheDocument()
    expect(screen.getByText('最低 CPU 余量')).toBeInTheDocument()
    expect(screen.getByText('最低内存余量')).toBeInTheDocument()
    expect(screen.getByText('需关注资源池')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看容量详情' }))
    expect(window.location.pathname).toBe('/capacity')
  })

  it('opens the fleet capacity category and drills into one cluster', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacity, error: null }), ok: true }
      if (url.endsWith('/clusters/cluster-a/capacity')) return { json: async () => ({ data: clusterCapacity, error: null }), ok: true }
      if (url.endsWith('/clusters/cluster-a')) return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    const capacityNavigation = screen.getByText('容量', { selector: '.ks-nav-label' })
    await user.click(capacityNavigation)

    expect(window.location.pathname).toBe('/capacity')
    expect(await screen.findByRole('heading', { name: '资源池容量' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '添加集群' })).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '生产环境 / G01 CPU' })).toBeInTheDocument()
    expect(screen.getByText('1 套集群未纳入本次容量汇总')).toBeInTheDocument()
    const table = screen.getByRole('grid', { name: '集群容量列表' })
    await user.click(within(table).getByRole('button', { name: 'cluster-a' }))

    expect(window.location.pathname).toBe('/clusters/cluster-a/capacity')
    expect(await screen.findByRole('heading', { name: 'CPU 容量' })).toBeInTheDocument()
  })

  it('builds one G01 pool per environment and keeps TCS and CMBK8S as pool composition', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/capacity')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      if (String(input).endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacityByPool, error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    const { container } = render(<App />)

    const poolTable = await screen.findByRole('grid', { name: '容量资源池列表' })
    expect(within(poolTable).getByRole('button', { name: '生产环境 / OCP' })).toBeInTheDocument()
    const productionG01 = within(poolTable).getByRole('button', { name: '生产环境 / G01' })
    expect(productionG01).toBeInTheDocument()
    expect(within(poolTable).getByRole('button', { name: 'UAT / G01' })).toBeInTheDocument()
    const productionG01Row = productionG01.closest('tr')!
    expect(within(productionG01Row).getByText('TCS 1 套')).toBeInTheDocument()
    expect(within(productionG01Row).getByText('CMBK8S 1 套')).toBeInTheDocument()
    await user.click(productionG01)

    expect(container.querySelector('.ks-active-pool-heading')).toHaveTextContent('生产环境 / G01')
    expect(screen.getByRole('heading', { name: '生产环境 / G01 CPU' })).toBeInTheDocument()
    const clusterTable = screen.getByRole('grid', { name: '集群容量列表' })
    expect(within(clusterTable).getByText('cluster-a')).toBeInTheDocument()
    expect(within(clusterTable).getByText('cluster-cmb')).toBeInTheDocument()
    expect(within(clusterTable).queryByText('cluster-ocp')).not.toBeInTheDocument()
    expect(within(clusterTable).queryByText('cluster-uat')).not.toBeInTheDocument()
  })

  it('opens the fleet capacity deep link after reload', async () => {
    window.history.replaceState(null, '', '/capacity')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      if (String(input).endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacity, error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: '资源池容量' })).toBeInTheDocument()
    expect(screen.getByText('容量', { selector: '.ks-nav-label' }).closest('.pf-m-current')).not.toBeNull()
  })

  it('edits one global business namespace list without technology-stack classifications', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/capacity')
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/v1/capacity/classification') && init?.method === 'PUT') return { json: async () => ({ data: { ...capacityClassification.classification, id: 2 }, error: null }), ok: true }
      if (url.endsWith('/api/v1/capacity/classification')) return { json: async () => ({ data: capacityClassification, error: null }), ok: true }
      if (url.endsWith('/api/v1/capacity')) return { json: async () => ({ data: fleetCapacity, error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)
    await user.click(await screen.findByRole('button', { name: '业务命名空间配置' }))
    expect(await screen.findByRole('heading', { name: '业务命名空间配置' })).toBeInTheDocument()
    expect(await screen.findByLabelText('业务 Namespace')).toHaveValue('app')
    expect(screen.queryByLabelText('按技术栈查看 Namespace')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '加入业务 app' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '加入业务 new-system' }))
    await user.type(screen.getByLabelText('规则变更说明'), '确认全局业务清单')
    await user.click(screen.getByRole('button', { name: '保存业务清单' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/capacity/classification', expect.objectContaining({ method: 'PUT' })))
    const saveCall = fetchMock.mock.calls.find(([url, init]) => String(url).endsWith('/api/v1/capacity/classification') && init?.method === 'PUT')
    expect(JSON.parse(saveCall![1].body)).toEqual({ business_namespaces: ['app', 'new-system'], note: '确认全局业务清单' })
    expect(await screen.findByText('业务命名空间清单已保存。')).toBeInTheDocument()
  })

  it('opens the add cluster dialog', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(emptyClusterResponse))
    render(<App />)

    expect(screen.queryByRole('button', { name: '添加集群' })).not.toBeInTheDocument()
    const clustersNavigation = screen.getByText('集群', { selector: '.ks-nav-label' })
    await user.click(clustersNavigation)
    await user.click(screen.getByRole('button', { name: '添加集群' }))

    expect(screen.getByRole('heading', { name: '纳管新集群' })).toBeInTheDocument()
    expect(screen.getByLabelText('集群名称')).toBeInTheDocument()
    expect(screen.getByLabelText('所属环境')).toBeInTheDocument()
    expect(screen.getByText('上传文件')).toBeInTheDocument()
    expect(screen.getByText('粘贴内容')).toBeInTheDocument()
  })

  it('collapses and expands the desktop sidebar', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(emptyClusterResponse))
    const { container } = render(<App />)
    const collapseButton = container.querySelector<HTMLButtonElement>('button[aria-label="收起侧边栏"]')

    expect(collapseButton).not.toBeNull()
    await user.click(collapseButton!)
    expect(container.querySelector('.ks-page')).toHaveClass('ks-page--sidebar-collapsed')
    expect(collapseButton).toHaveAttribute('aria-label', '展开侧边栏')

    await user.click(collapseButton!)
    expect(container.querySelector('.ks-page')).not.toHaveClass('ks-page--sidebar-collapsed')
  })

  it('filters clusters by name', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      json: async () => ({ data: realClusters, error: null }), ok: true,
    }))
    render(<App />)

    expect(await screen.findByText('cluster-a')).toBeInTheDocument()
    expect(screen.getByText('23.4%')).toBeInTheDocument()
    await user.type(screen.getByLabelText('搜索集群'), 'cluster-a')

    const clusterTable = screen.getByRole('grid', { name: '集群列表' })
    expect(within(clusterTable).getByText('cluster-a')).toBeInTheDocument()
    expect(within(clusterTable).queryByText('cluster-b')).not.toBeInTheDocument()
  })

  it('shows and clears the active status filter', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      json: async () => ({ data: realClusters, error: null }), ok: true,
    }))
    render(<App />)

    expect(await screen.findByText('cluster-a')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('状态筛选'), 'unreachable')

    expect(screen.getByLabelText('状态筛选')).toHaveValue('unreachable')
    expect(screen.getByRole('heading', { name: '没有匹配的集群' })).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('状态筛选'), 'ALL')
    expect(screen.getByText('cluster-a')).toBeInTheDocument()
    expect(screen.getAllByText('cluster-b').length).toBeGreaterThan(0)
  })

  it('filters managed clusters by technology stack', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      json: async () => ({ data: realClusters, error: null }), ok: true,
    }))
    render(<App />)

    expect(await screen.findByText('cluster-a')).toBeInTheDocument()
    const clusterTable = screen.getByRole('grid', { name: '集群列表' })
    await user.selectOptions(screen.getByLabelText('技术栈筛选'), 'TCS')
    expect(within(clusterTable).getByText('cluster-a')).toBeInTheDocument()
    expect(within(clusterTable).queryByText('cluster-b')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('技术栈筛选'), 'CMBK8S')
    expect(within(clusterTable).queryByText('cluster-a')).not.toBeInTheDocument()
    expect(within(clusterTable).getByText('cluster-b')).toBeInTheDocument()
  })

  it('removes a managed cluster after confirmation', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        json: async () => ({ data: realClusters, error: null }), ok: true,
      })
      .mockResolvedValueOnce({
        json: async () => ({ data: realClusters[0], error: null }), ok: true,
      })
      .mockResolvedValueOnce({ ok: true })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByText('cluster-a')).toBeInTheDocument()
    const clusterTable = screen.getByRole('grid', { name: '集群列表' })
    await user.click(within(clusterTable).getByRole('button', { name: 'cluster-a' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/overview')
    expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '移除集群' }))

    expect(screen.getByRole('heading', { name: /确认移除集群/ })).toBeInTheDocument()
    expect(screen.getByText(/不会删除 Kubernetes 集群/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '确认移除' }))

    expect(await screen.findByText('集群已移除')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/clusters/cluster-a', { method: 'DELETE' })
    expect(window.location.pathname).toBe('/clusters')
    expect(screen.queryByText('cluster-a')).not.toBeInTheDocument()
  })

  it('updates an existing cluster classification for capacity pooling', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/overview')
    const updated = { ...realClusters[0], cluster_type: 'G01', technology_stack: 'CMBK8S' }
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/classification') && init?.method === 'PATCH') {
        return { json: async () => ({ data: updated, error: null }), ok: true }
      }
      return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑分类' }))
    await user.selectOptions(screen.getByLabelText('详情集群类型'), 'G01')
    await user.selectOptions(screen.getByLabelText('详情技术栈'), 'CMBK8S')
    await user.click(screen.getByRole('button', { name: '保存分类' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/clusters/cluster-a/classification', expect.objectContaining({ method: 'PATCH' })))
    expect(screen.queryByRole('heading', { name: '编辑集群分类' })).not.toBeInTheDocument()
  })

  it('opens the ACM-style nodes tab with live node data', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/overview')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/clusters/cluster-a/nodes')) {
        return { json: async () => ({ data: clusterNodes, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a')) {
        return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '概览' })).toHaveAttribute('aria-selected', 'true')

    await user.click(screen.getByRole('tab', { name: '节点' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/nodes')
    const nodeTable = await screen.findByRole('grid', { name: '节点列表' })
    expect(within(nodeTable).getByText('worker-1')).toBeInTheDocument()
    expect(within(nodeTable).getByText('16 GiB')).toBeInTheDocument()
    expect(within(nodeTable).getByText('cn-east-1a')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: '概览' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/overview')
    expect(screen.getByRole('heading', { name: '资源使用' })).toBeInTheDocument()
  })

  it('shows cluster capacity and locks an immutable delivery baseline', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/overview')
    let baselineLocked = false
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/clusters/cluster-a/capacity/baselines') && init?.method === 'POST') {
        baselineLocked = true
        return { json: async () => ({ data: { id: 1, cluster_id: 'cluster-a' }, error: null }), ok: true, status: 201 }
      }
      if (url.endsWith('/clusters/cluster-a/capacity')) {
        const baseline = baselineLocked ? { captured_at: '2026-10-17T10:00:00Z', cluster_id: 'cluster-a', cpu_allocatable_milli: 16000, id: 1, memory_allocatable_bytes: 68719476736, platform_cpu_limits_milli: 4000, platform_cpu_requests_milli: 2000, platform_memory_limits_bytes: 8589934592, platform_memory_requests_bytes: 4294967296, system_namespaces: ['kube-system', 'cmb-monitoring'], worker_nodes: 2 } : null
        return { json: async () => ({ data: { ...clusterCapacity, baseline, comparison: { ...clusterCapacity.comparison, status: baselineLocked ? 'stable' : 'not_set' } }, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a')) return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: '容量' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/capacity')
    expect(await screen.findByRole('heading', { name: 'CPU 容量' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '内存容量' })).toBeInTheDocument()
    expect(screen.getByText('实际使用数据不可用')).toBeInTheDocument()
    expect(screen.getByText('业务资源声明不完整，剩余容量可能被高估')).toBeInTheDocument()
    expect(within(screen.getByRole('grid', { name: '命名空间容量列表' })).getByText('app')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '锁定当前为基线' }))
    expect(screen.getByRole('heading', { name: /锁定当前容量基线/ })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '确认锁定' }))
    expect((await screen.findAllByText('版本 1')).length).toBeGreaterThan(0)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/clusters/cluster-a/capacity/baselines', { method: 'POST' })
  })

  it('opens a cluster capacity deep link after page reload', async () => {
    window.history.replaceState(null, '', '/clusters/cluster-a/capacity')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/clusters/cluster-a/capacity')) return { json: async () => ({ data: clusterCapacity, error: null }), ok: true }
      if (url.endsWith('/clusters/cluster-a')) return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'CPU 容量' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '容量' })).toHaveAttribute('aria-selected', 'true')
  })

  it('requires a reason when a changed classification keeps the old baseline', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/capacity')
    const pendingCapacity = {
      ...clusterCapacity,
      baseline: { captured_at: '2026-10-17T10:00:00Z', cluster_id: 'cluster-a', cpu_allocatable_milli: 16000, id: 1, memory_allocatable_bytes: 68719476736, platform_cpu_limits_milli: 4000, platform_cpu_requests_milli: 2000, platform_memory_limits_bytes: 8589934592, platform_memory_requests_bytes: 4294967296, system_namespaces: ['kube-system'], worker_nodes: 2 },
      baseline_review: { status: 'pending' },
      new_platform_namespaces: ['empty-addon'],
    }
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/clusters/cluster-a/capacity/baseline-decisions') && init?.method === 'POST') return { json: async () => ({ data: { id: 2 }, error: null }), ok: true }
      if (url.endsWith('/clusters/cluster-a/capacity')) return { json: async () => ({ data: pendingCapacity, error: null }), ok: true }
      if (url.endsWith('/clusters/cluster-a')) return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByText('平台组件范围已变化，需要确认交付基线')).toBeInTheDocument()
    expect(screen.getByText('发现交付后新增的平台 Namespace')).toBeInTheDocument()
    expect(screen.getByText(/empty-addon/)).toBeInTheDocument()
    expect(screen.queryByText('新增未分类')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '暂不更新' }))
    await user.click(screen.getByRole('button', { name: '保存原因并沿用旧基线' }))
    expect(screen.getByText('暂不更新基线时必须填写原因。')).toBeInTheDocument()
    await user.type(screen.getByLabelText('暂不更新基线的原因'), '本次仅补录已有组件')
    await user.click(screen.getByRole('button', { name: '保存原因并沿用旧基线' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/clusters/cluster-a/capacity/baseline-decisions', expect.objectContaining({ method: 'POST' })))
  })

  it('drills from an unhealthy cluster into affected workloads', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-b/overview')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/clusters/cluster-b/workloads')) {
        return { json: async () => ({ data: clusterWorkloads, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-b/pods')) {
        return { json: async () => ({ data: clusterPods, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-b')) {
        return { json: async () => ({ data: realClusters[1], error: null }), ok: true }
      }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    }))
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'cluster-b' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /查看受影响工作负载/ }))

    expect(window.location.pathname).toBe('/clusters/cluster-b/workloads')
    expect(window.location.search).toBe('?attention=1')
    expect(await screen.findByText('正在查看受影响工作负载')).toBeInTheDocument()
    const workloadTable = await screen.findByRole('grid', { name: '工作负载列表' })
    expect(within(workloadTable).getByText('web')).toBeInTheDocument()
    expect(within(workloadTable).queryByText('cache')).not.toBeInTheDocument()
    expect(screen.getByLabelText('筛选状态')).toHaveValue('ATTENTION')
  })

  it('lists and filters real workloads before drilling into Pod instances', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/overview')
    const fetchMock = vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/clusters/cluster-a/pods/app/web-abc-1/logs?')) {
        return { json: async () => ({ data: { container: 'web', previous: false, tail_lines: 200, text: '2026-10-16T01:00:00Z INFO server started on 10.244.1.8\n2026-10-16T01:00:01Z WARNING retrying request\n2026-10-16T01:00:02Z ERROR request failed\n', truncated: false }, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a/workloads/Deployment/app/web')) {
        return { json: async () => ({ data: webWorkloadDetail, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a/pods/app/web-abc-1')) {
        return { json: async () => ({ data: webPodDetail, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a/workloads')) {
        return { json: async () => ({ data: clusterWorkloads, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a/pods')) {
        return { json: async () => ({ data: clusterPods, error: null }), ok: true }
      }
      if (url.endsWith('/clusters/cluster-a')) {
        return { json: async () => ({ data: realClusters[0], error: null }), ok: true }
      }
      return { json: async () => ({ data: realClusters, error: null }), ok: true }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'cluster-a' })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: '工作负载' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/workloads')

    const workloadTable = await screen.findByRole('grid', { name: '工作负载列表' })
    expect(within(workloadTable).getByText('web')).toBeInTheDocument()
    expect(within(workloadTable).getByText('cache')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('筛选命名空间'), 'app')
    expect(within(workloadTable).getByText('web')).toBeInTheDocument()
    expect(within(workloadTable).queryByText('cache')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('筛选命名空间'), 'ALL')
    await user.click(screen.getByRole('tab', { name: /Pods \(2\)/ }))
    const podTable = screen.getByRole('grid', { name: 'Pod 列表' })
    expect(within(podTable).getByText('manual-pod')).toBeInTheDocument()
    expect(within(podTable).getByText('Deployment')).toBeInTheDocument()
    expect(within(podTable).queryByText('独立 Pod')).not.toBeInTheDocument()

    await user.click(within(podTable).getByRole('button', { name: 'web-abc-1' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/pods/app/web-abc-1')
    const containerTable = await screen.findByRole('grid', { name: 'Pod 容器列表' })
    expect(within(containerTable).getByText('CrashLoopBackOff')).toBeInTheDocument()
    expect(screen.getByText('app=web')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: '日志' }))
    const logOutput = await screen.findByLabelText('Pod 日志内容')
    expect(logOutput).toHaveTextContent('server started on 10.244.1.8')
    expect(within(logOutput).getByText('ERROR')).toHaveClass('is-error')
    expect(within(logOutput).getByText('WARNING')).toHaveClass('is-warning')
    expect(within(logOutput).getByText('10.244.1.8')).toHaveClass('is-ip')
    expect(within(logOutput).getByText('2026-10-16T01:00:00Z')).toHaveClass('is-timestamp')
    expect(screen.getByLabelText('日志高亮图例')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('选择日志时间范围'), '15m')
    await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes('since='))).toBe(true))
    expect(screen.getByLabelText('选择日志行数')).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('选择日志时间范围'), 'custom')
    expect(screen.getByLabelText('日志开始时间')).toBeInTheDocument()
    expect(screen.getByLabelText('日志结束时间')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: '详情' }))
    await user.click(screen.getByRole('button', { name: 'Deployment / web' }))
    expect(window.location.pathname).toBe('/clusters/cluster-a/workloads/Deployment/app/web')
    expect(await screen.findByRole('grid', { name: '工作负载实例列表' })).toBeInTheDocument()

    const podsTable = await screen.findByRole('grid', { name: '工作负载实例列表' })
    expect(within(podsTable).getByText('web-abc-1')).toBeInTheDocument()
    expect(within(podsTable).getByText('CrashLoopBackOff')).toBeInTheDocument()
    expect(screen.getByText('app=web')).toBeInTheDocument()
  })

  it('onboards a pasted kubeconfig in one step', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters')
    let onboarded = false
    const createdCluster = {
            api_latency_ms: 18, api_server: 'https://cluster.example.test', authentication_type: 'Bearer Token',
            context_name: 'default', connection_status: 'connected', created_at: '2026-10-16T01:00:00Z',
            core_components_ready: 3, core_components_total: 3, cpu_utilization: 23.4,
            environment: '生产环境', id: 'cluster-1', instances_ready: 12, instances_total: 12,
            kubernetes_version: 'v1.32.4', last_checked_at: '2026-10-16T01:00:00Z',
            memory_utilization: 51.2, metrics_available: true, name: 'prd-test-01', nodes_ready: 3,
            nodes_total: 3, technology_stack: 'CMBK8S', worker_cores: 16, worker_nodes: 2,
    }
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/v1/capacity')) return { json: async () => ({ data: { collected_at: '', failures: [], items: [] }, error: null }), ok: true }
      if (url.endsWith('/api/v1/clusters') && init?.method === 'POST') {
        onboarded = true
        return { json: async () => ({ data: createdCluster, error: null }), ok: true }
      }
      if (url.endsWith('/api/v1/clusters')) return { json: async () => ({ data: onboarded ? [realClusters[0]] : [], error: null }), ok: true }
      return emptyClusterResponse
    }))
    render(<App />)

    await user.click(screen.getByRole('button', { name: '添加集群' }))
    await user.click(screen.getByRole('tab', { name: /粘贴内容/ }))
    await user.type(screen.getByLabelText('集群名称'), 'prd-test-01')
    await user.type(screen.getByLabelText('所属环境'), '生产环境')
    await user.selectOptions(screen.getByLabelText('集群类型'), 'G01')
    await user.selectOptions(screen.getByLabelText('技术栈'), 'CMBK8S')
    await user.type(screen.getByLabelText('Kubeconfig 内容'), 'apiVersion: v1')
    await user.click(screen.getByRole('button', { name: '纳管集群' }))

    expect(await screen.findByText('集群纳管成功')).toBeInTheDocument()
    expect(screen.getByText('prd-test-01 已成功纳管。')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('heading', { name: '纳管新集群' })).not.toBeInTheDocument()
    })
  })
  it('opens exact search resources from an existing cluster page and across clusters', async () => {
    const user = userEvent.setup()
    window.history.replaceState(null, '', '/clusters/cluster-a/overview')
    const results = [
      { cluster_id: 'cluster-a', cluster_name: 'cluster-a', kind: 'Deployment', namespace: 'app', name: 'web' },
      { cluster_id: 'cluster-a', cluster_name: 'cluster-a', kind: 'Pod', namespace: 'app', name: 'web-abc-1' },
      { cluster_id: 'cluster-b', cluster_name: 'cluster-b', kind: 'Deployment', namespace: 'other-app', name: 'other-web' },
    ]
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      let data: unknown = realClusters
      if (url.includes('/search/resources')) data = { items: results.map(result => ({ ...result, environment: 'test', technology_stack: 'TCS', images: [], owner_kind: '', owner_name: '', status: 'Running' })), failed_clusters: [], truncated: false }
      else if (url.endsWith('/capacity')) data = fleetCapacityByPool
      else if (url.endsWith('/workloads/Deployment/app/web')) data = webWorkloadDetail
      else if (url.endsWith('/workloads/Deployment/other-app/other-web')) data = { ...webWorkloadDetail, name: 'other-web', namespace: 'other-app' }
      else if (url.endsWith('/pods/app/web-abc-1')) data = webPodDetail
      else if (url.endsWith('/workloads')) data = clusterWorkloads
      else if (url.endsWith('/pods')) data = clusterPods
      else if (url.endsWith('/clusters/cluster-a')) data = realClusters[0]
      else if (url.endsWith('/clusters/cluster-b')) data = realClusters[1]
      return { ok: true, json: async () => ({ data, error: null }) }
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)
    await screen.findByRole('heading', { name: 'cluster-a' })
    async function choose(name: string) {
      await user.click(screen.getByRole('button', { name: '跨集群搜索应用与资源' }))
      const dialog = within(await screen.findByRole('dialog', { name: '跨集群资源搜索' }))
      await user.clear(dialog.getByLabelText('跨集群搜索'))
      await user.type(dialog.getByLabelText('跨集群搜索'), 'web')
      await user.click(await dialog.findByRole('button', { name }))
    }
    await choose('web')
    expect(await screen.findByRole('heading', { name: 'web', level: 2 })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/clusters/cluster-a/workloads/Deployment/app/web')
    expect(screen.queryByRole('grid', { name: '工作负载列表' })).not.toBeInTheDocument()
    await choose('web-abc-1')
    expect(await screen.findByRole('heading', { name: 'web-abc-1', level: 2 })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/clusters/cluster-a/pods/app/web-abc-1')
    await choose('other-web')
    expect(await screen.findByRole('heading', { name: 'other-web', level: 2 })).toBeInTheDocument()
    expect(window.location.pathname).toBe('/clusters/cluster-b/workloads/Deployment/other-app/other-web')
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/clusters/cluster-b/workloads/Deployment/other-app/other-web'))).toBe(true)
  })

})
