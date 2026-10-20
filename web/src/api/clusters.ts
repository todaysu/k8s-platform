export type ClusterValidationPreview = {
  api_server: string
  authentication_type: string
  context_name: string
  kubernetes_version: string
  name: string
}

export type ManagedCluster = ClusterValidationPreview & {
  api_latency_ms: number | null
  connection_status: string
  core_components_ready: number | null
  core_components_total: number | null
  created_at: string
  cpu_utilization: number | null
  environment: string
  cluster_type: string
  id: string
  instances_ready: number | null
  instances_total: number | null
  last_checked_at: string
  memory_utilization: number | null
  metrics_available: boolean
  nodes_ready: number | null
  nodes_total: number | null
  technology_stack: string
  worker_cores: number | null
  worker_nodes: number | null
}

export type ClusterNode = {
  cpu_cores: number
  created_at: string
  instance_type: string
  kubernetes_version: string
  memory_bytes: number
  name: string
  region: string
  roles: string[]
  schedulable: boolean
  status: 'Ready' | 'NotReady' | 'Unknown'
  zone: string
}

export type CapacityValues = {
  cpu_limits_milli: number
  cpu_requests_milli: number
  cpu_usage_milli: number | null
  memory_limits_bytes: number
  memory_requests_bytes: number
  memory_usage_bytes: number | null
  pods_without_cpu_requests?: number
  pods_without_memory_requests?: number
}

export type NamespaceCapacity = CapacityValues & {
  class: 'platform' | 'business' | 'unclassified'
  namespace: string
  pods: number
}

export type CapacityBaseline = {
  captured_at: string
  cluster_id: string
  cpu_allocatable_milli: number
  id: number
  memory_allocatable_bytes: number
  platform_cpu_limits_milli: number
  platform_cpu_requests_milli: number
  platform_memory_limits_bytes: number
  platform_memory_requests_bytes: number
  system_namespaces: string[]
  worker_nodes: number
}

export type ClusterCapacity = {
  baseline: CapacityBaseline | null
  business: CapacityValues
  unclassified: CapacityValues
  collected_at: string
  comparison: {
    cpu_allocatable_change_percent: number
    memory_allocatable_change_percent: number
    platform_cpu_request_change_percent: number
    platform_memory_request_change_percent: number
    status: 'not_set' | 'stable' | 'drift' | 'rebaseline'
  }
  cpu_allocatable_milli: number
  memory_allocatable_bytes: number
  metrics_available: boolean
  namespaces: NamespaceCapacity[]
  pending: CapacityValues
  platform: CapacityValues
  safety_reserve_percent: number
  system_namespaces: string[]
  worker_nodes: number
  classification_version_id: number
  new_platform_namespaces?: string[]
  baseline_review: { status: 'not_required' | 'pending' | 'kept'; reason?: string }
}

export type CapacityClassification = {
  business_namespaces: string[]
  common_platform_namespaces?: string[]
  created_at?: string
  id?: number
  note: string
  stack_platform_namespaces?: Record<string, string[]>
}

export type DiscoveredNamespace = {
  class: 'platform' | 'business' | 'unclassified'
  cluster_count: number
  cpu_requests_milli: number
  first_seen_at: string
  memory_requests_bytes: number
  namespace: string
  pods: number
  technology_stack: string
}

export type CapacityClassificationOverview = {
  classification: CapacityClassification
  discovered: DiscoveredNamespace[]
}

export type FleetCapacityItem = {
  capacity: ClusterCapacity
  cluster_id: string
  cluster_name: string
  cluster_type: string
  environment: string
  technology_stack: string
}

export type FleetCapacityFailure = {
  cluster_id: string
  cluster_name: string
  cluster_type: string
  code: string
  environment: string
  technology_stack: string
}

export type FleetCapacity = {
  collected_at: string
  failures: FleetCapacityFailure[]
  items: FleetCapacityItem[]
}

export type ClusterWorkload = {
  available: number
  created_at: string
  desired: number
  images: string[]
  kind: 'Deployment' | 'StatefulSet' | 'DaemonSet'
  last_updated_at: string
  name: string
  namespace: string
  pods_ready: number
  pods_total: number
  ready: number
  restart_count: number
  status: 'healthy' | 'progressing' | 'degraded' | 'unknown'
  status_reason: string
  updated: number
}

export type ClusterPod = {
  created_at: string
  images: string[]
  name: string
  namespace: string
  node_name: string
  owner_kind: string
  owner_name: string
  phase: string
  pod_ip: string
  ready_containers: number
  restart_count: number
  started_at: string | null
  status_reason: string
  total_containers: number
}

export type PodContainer = {
  image: string
  name: string
  ready: boolean
  restart_count: number
  state: string
  status_reason: string
}

export type PodCondition = {
  last_transition_time: string | null
  message: string
  reason: string
  status: string
  type: string
}

export type ClusterPodDetail = ClusterPod & {
  conditions: PodCondition[]
  containers: PodContainer[]
  host_ip: string
  labels: Record<string, string>
  qos_class: string
  service_account: string
}

export type PodLogSnapshot = {
  container: string
  previous: boolean
  since_time?: string
  tail_lines: number
  text: string
  truncated: boolean
  until_time?: string
}

export type WorkloadPod = {
  created_at: string
  name: string
  node_name: string
  phase: string
  pod_ip: string
  ready_containers: number
  restart_count: number
  started_at: string | null
  status_reason: string
  total_containers: number
}

export type ClusterWorkloadDetail = ClusterWorkload & {
  labels: Record<string, string>
  pods: WorkloadPod[]
}

export type WorkloadYAML = { yaml: string }

export type GlobalResourceResult = {
  cluster_id: string
  cluster_name: string
  environment: string
  images: string[]
  kind: string
  name: string
  namespace: string
  owner_kind: string
  owner_name: string
  status: string
  technology_stack: string
}

export type GlobalResourceSearch = {
  failed_clusters: string[]
  items: GlobalResourceResult[]
  truncated: boolean
}

export type PlatformCapabilities = { pod_exec_enabled: boolean }

export type ClusterEvent = {
  action: string
  cluster_id: string
  event_uid: string
  first_observed_at: string
  last_observed_at: string
  message: string
  namespace: string
  occurrence_count: number
  reason: string
  regarding_api_version: string
  regarding_kind: string
  regarding_name: string
  regarding_namespace: string
  regarding_uid: string
  reporting_controller: string
  reporting_instance: string
  resource_version: string
  type: string
}

export type ClusterEventPage = { items: ClusterEvent[]; next_cursor?: string; total: number }
export type ClusterEventFacets = { kinds: string[]; namespaces: string[]; types: string[] }
export type ClusterEventSummary = {
  affected_objects: number
  attention: number
  collector_state: string
  component_warnings: number
  last_error?: string
  last_success_at?: string
  total: number
  warning: number
}

export type FleetEvent = ClusterEvent & {
  cluster_name: string
  environment: string
  scope: 'business' | 'platform'
  technology_stack: string
}
export type FleetEventIssue = {
  title: string
  affected_clusters: number
  affected_objects: number
  cluster_ids: string[]
  event_count: number
  fingerprint: string
  first_observed_at: string
  last_observed_at: string
  message: string
  occurrence_count: number
  reason: string
  regarding_kind: string
  severity: 'critical' | 'warning' | 'normal'
  type: string
}
export type EventCollectorStatus = {
  cluster_id: string
  cluster_name: string
  last_error?: string
  last_success_at?: string
  state: string
}
export type FleetEventOverview = {
  objects: FleetEventObject[]
  pagination: { page: number; page_size: number; total_items: number; total_pages: number }
  failures: { cluster_id: string; cluster_name: string; code: string }[]
  since: string
  until: string
  business_configured: boolean
  collectors: EventCollectorStatus[]
  facets: ClusterEventFacets
  issues: FleetEventIssue[]
  items: FleetEvent[]
  summary: {
    affected_clusters: number
    affected_objects: number
    collectors_failed: number
    collectors_healthy: number
    event_records: number
    problem_groups: number
    total_clusters: number
    warning_groups: number
  }
  total: number
  truncated: boolean
  storage: {
    hourly_rows: number
    latest_rollup_at?: string
    normal_retention_days: number
    raw_rows: number
    rollup_retention_days: number
    warning_retention_days: number
  }
}
export type FleetEventFilters = EventFilters & {
  view?: 'issues' | 'events' | 'objects' | 'resources'
  issue?: string
  page?: number
  scope?: string
  include_normal?: boolean
  object_uid?: string
  object_name?: string
  since?: string
  until?: string
  cluster_id?: string
  environment?: string
  technology_stack?: string
}

export type EventFilters = { component?: boolean; kind?: string; limit?: number; namespace?: string; q?: string; range?: string; type?: string }

function eventParams(filters: EventFilters) {
  const params = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => { if (value !== undefined && value !== '' && value !== false) params.set(key, String(value)) })
  return params.toString()
}

export async function fetchClusterEvents(id: string, filters: EventFilters): Promise<ClusterEventPage> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/events?${eventParams(filters)}`)
  const payload = await response.json() as ApiResponse<ClusterEventPage>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取事件失败。')
  return payload.data
}

export async function fetchFleetEvents(filters: FleetEventFilters): Promise<FleetEventOverview> {
  const query = new URLSearchParams()
  Object.entries(filters).forEach(([key, value]) => {
    if (value !== undefined && value !== '' && value !== false) query.set(key, String(value))
  })
  const response = await fetch(`/api/v1/events?${query.toString()}`)
  const payload = await response.json() as ApiResponse<FleetEventOverview>
  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取跨集群事件失败。')
  }
  return { ...payload.data, items: payload.data.items ?? [], objects: payload.data.objects ?? [], issues: payload.data.issues ?? [], failures: payload.data.failures ?? [] }
}

export async function fetchClusterEventSummary(id: string, range: string): Promise<ClusterEventSummary> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/events/summary?range=${encodeURIComponent(range)}`)
  const payload = await response.json() as ApiResponse<ClusterEventSummary>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取事件概要失败。')
  return payload.data
}

export async function fetchClusterEventFacets(id: string, range: string): Promise<ClusterEventFacets> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/events/options?range=${encodeURIComponent(range)}`)
  const payload = await response.json() as ApiResponse<ClusterEventFacets>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取事件筛选项失败。')
  return payload.data
}

type ApiResponse<T> = {
  data: T | null
  error: { code: string; message: string } | null
}

export class ClusterApiError extends Error {
  code: string

  constructor(code: string, message: string) {
    super(message)
    this.code = code
  }
}

export async function fetchPlatformCapabilities(): Promise<PlatformCapabilities> {
  const response = await fetch('/api/v1/capabilities')
  const payload = await response.json() as ApiResponse<PlatformCapabilities>
  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取平台功能状态失败。')
  }
  return payload.data
}

export async function fetchClusterCapacity(id: string): Promise<ClusterCapacity> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/capacity`)
  const payload = await response.json() as ApiResponse<ClusterCapacity>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取集群容量失败。')
  return payload.data
}

export async function fetchFleetCapacity(): Promise<FleetCapacity> {
  const response = await fetch('/api/v1/capacity')
  const payload = await response.json() as ApiResponse<FleetCapacity>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取资源池容量失败。')
  return payload.data
}

export async function fetchCapacityClassification(): Promise<CapacityClassificationOverview> {
  const response = await fetch('/api/v1/capacity/classification')
  const payload = await response.json() as ApiResponse<CapacityClassificationOverview>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取容量分类失败。')
  return payload.data
}

export async function saveCapacityClassification(value: CapacityClassification): Promise<CapacityClassification> {
  const response = await fetch('/api/v1/capacity/classification', { body: JSON.stringify(value), headers: { 'Content-Type': 'application/json' }, method: 'PUT' })
  const payload = await response.json() as ApiResponse<CapacityClassification>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '保存容量分类失败。')
  return payload.data
}

export async function keepCapacityBaseline(id: string, reason: string): Promise<void> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/capacity/baseline-decisions`, { body: JSON.stringify({ reason }), headers: { 'Content-Type': 'application/json' }, method: 'POST' })
  const payload = await response.json() as ApiResponse<unknown>
  if (!response.ok || payload.error) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '保存基线决策失败。')
}

export async function lockClusterCapacityBaseline(id: string): Promise<CapacityBaseline> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/capacity/baselines`, { method: 'POST' })
  const payload = await response.json() as ApiResponse<CapacityBaseline>
  if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '建立容量基线失败。')
  return payload.data
}

export async function validateCluster(
  name: string,
  kubeconfig: string,
): Promise<ClusterValidationPreview> {
  const response = await fetch('/api/v1/clusters/validate', {
    body: JSON.stringify({ name, kubeconfig }),
    headers: { 'Content-Type': 'application/json' },
    method: 'POST',
  })

  let payload: ApiResponse<ClusterValidationPreview>
  try {
    payload = await response.json() as ApiResponse<ClusterValidationPreview>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '测试连接失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function onboardCluster(
  name: string,
  environment: string,
  clusterType: string,
  technologyStack: string,
  kubeconfig: string,
): Promise<ManagedCluster> {
  const response = await fetch('/api/v1/clusters', {
    body: JSON.stringify({ cluster_type: clusterType, environment, name, technology_stack: technologyStack, kubeconfig }),
    headers: { 'Content-Type': 'application/json' },
    method: 'POST',
  })

  let payload: ApiResponse<ManagedCluster>
  try {
    payload = await response.json() as ApiResponse<ManagedCluster>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '纳管集群失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusters(): Promise<ManagedCluster[]> {
  const response = await fetch('/api/v1/clusters')

  let payload: ApiResponse<ManagedCluster[]>
  try {
    payload = await response.json() as ApiResponse<ManagedCluster[]>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取集群列表失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchCluster(id: string): Promise<ManagedCluster> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}`)

  let payload: ApiResponse<ManagedCluster>
  try {
    payload = await response.json() as ApiResponse<ManagedCluster>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取集群详情失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusterNodes(id: string): Promise<ClusterNode[]> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/nodes`)

  let payload: ApiResponse<ClusterNode[]>
  try {
    payload = await response.json() as ApiResponse<ClusterNode[]>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取节点列表失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusterWorkloads(id: string): Promise<ClusterWorkload[]> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/workloads`)

  let payload: ApiResponse<ClusterWorkload[]>
  try {
    payload = await response.json() as ApiResponse<ClusterWorkload[]>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取工作负载失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusterPods(id: string): Promise<ClusterPod[]> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/pods`)

  let payload: ApiResponse<ClusterPod[]>
  try {
    payload = await response.json() as ApiResponse<ClusterPod[]>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取 Pod 列表失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusterPod(id: string, namespace: string, name: string): Promise<ClusterPodDetail> {
  const path = [id, 'pods', namespace, name].map((segment) => encodeURIComponent(segment)).join('/')
  const response = await fetch(`/api/v1/clusters/${path}`)

  let payload: ApiResponse<ClusterPodDetail>
  try {
    payload = await response.json() as ApiResponse<ClusterPodDetail>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取 Pod 详情失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchPodLogs(
  id: string,
  namespace: string,
  name: string,
  container: string,
  tailLines: number,
  previous: boolean,
  since?: string,
  until?: string,
): Promise<PodLogSnapshot> {
  const path = [id, 'pods', namespace, name, 'logs'].map((segment) => encodeURIComponent(segment)).join('/')
  const query = new URLSearchParams({ container, previous: String(previous), tail_lines: String(tailLines) })
  if (since) query.set('since', since)
  if (until) query.set('until', until)
  const response = await fetch(`/api/v1/clusters/${path}?${query.toString()}`)

  let payload: ApiResponse<PodLogSnapshot>
  try {
    payload = await response.json() as ApiResponse<PodLogSnapshot>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }
  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取容器日志失败，请稍后重试。',
    )
  }
  return payload.data
}

export async function fetchClusterWorkload(
  id: string,
  kind: string,
  namespace: string,
  name: string,
): Promise<ClusterWorkloadDetail> {
  const path = [id, 'workloads', kind, namespace, name].map((segment) => encodeURIComponent(segment)).join('/')
  const response = await fetch(`/api/v1/clusters/${path}`)

  let payload: ApiResponse<ClusterWorkloadDetail>
  try {
    payload = await response.json() as ApiResponse<ClusterWorkloadDetail>
  } catch {
    throw new ClusterApiError('INVALID_RESPONSE', '平台返回了无法识别的响应。')
  }

  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(
      payload.error?.code ?? 'REQUEST_FAILED',
      payload.error?.message ?? '读取工作负载详情失败，请稍后重试。',
    )
  }

  return payload.data
}

export async function fetchClusterWorkloadYAML(
  id: string,
  kind: string,
  namespace: string,
  name: string,
): Promise<WorkloadYAML> {
  const path = [id, 'workloads', kind, namespace, name, 'yaml'].map((segment) => encodeURIComponent(segment)).join('/')
  const response = await fetch(`/api/v1/clusters/${path}`)
  const payload = await response.json() as ApiResponse<WorkloadYAML>
  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '读取工作负载 YAML 失败。')
  }
  return payload.data
}

export async function searchGlobalResources(query: string): Promise<GlobalResourceSearch> {
  const response = await fetch(`/api/v1/search/resources?q=${encodeURIComponent(query)}`)
  const payload = await response.json() as ApiResponse<GlobalResourceSearch>
  if (!response.ok || payload.error || !payload.data) {
    throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED', payload.error?.message ?? '跨集群搜索失败。')
  }
  return payload.data
}

export async function deleteManagedCluster(id: string): Promise<void> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (response.ok) return

  let payload: ApiResponse<unknown> | null = null
  try {
    payload = await response.json() as ApiResponse<unknown>
  } catch {
    // The fallback below intentionally avoids exposing a raw server response.
  }
  throw new ClusterApiError(
    payload?.error?.code ?? 'REQUEST_FAILED',
    payload?.error?.message ?? '解除集群纳管失败，请稍后重试。',
  )
}

export async function updateClusterClassification(id: string, clusterType: string, technologyStack: string): Promise<ManagedCluster> {
  const response = await fetch(`/api/v1/clusters/${encodeURIComponent(id)}/classification`, {
    body: JSON.stringify({ cluster_type: clusterType, technology_stack: technologyStack }),
    headers: { 'Content-Type': 'application/json' },
    method: 'PATCH',
  })
  let payload: ApiResponse<ManagedCluster> | null = null
  try {
    payload = await response.json() as ApiResponse<ManagedCluster>
  } catch {
    // The fallback below intentionally avoids exposing a raw server response.
  }
  if (!response.ok || payload?.error || !payload?.data) {
    throw new ClusterApiError(payload?.error?.code ?? 'REQUEST_FAILED', payload?.error?.message ?? '保存集群分类失败。')
  }
  return payload.data
}

export type FleetEventObject = {
  members?: FleetEventObject[]
  grouping_note?: string
 cluster_id: string; cluster_name: string; namespace: string; kind: string; name: string; uid: string; scope: string
 event_count: number; occurrence_count: number; message: string; last_observed_at: string
}
export type EventResourceSnapshot = {
 status: 'normal' | 'abnormal' | 'unverified' | 'deleted' | 'replaced'; detail: string; checked_at: string; uid: string; node_name: string
 owners: { kind: string; name: string; uid: string }[]
 containers: { name: string; state: string; reason: string; exit_code?: number; previous_exit_code?: number; previous_reason?: string; restarts: number; has_logs: boolean; has_previous_logs: boolean }[]
}
export async function fetchEventResource(object: FleetEventObject): Promise<EventResourceSnapshot> {
 const query=new URLSearchParams({ cluster_id:object.cluster_id,namespace:object.namespace,kind:object.kind,name:object.name,uid:object.uid })
 const response=await fetch(`/api/v1/events/resource?${query}`)
 const payload=await response.json() as ApiResponse<EventResourceSnapshot>
 if (!response.ok || payload.error || !payload.data) throw new ClusterApiError(payload.error?.code ?? 'REQUEST_FAILED',payload.error?.message ?? '无法核验资源当前状态。')
 return payload.data
}
