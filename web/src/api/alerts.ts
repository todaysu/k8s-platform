type ApiResponse<T> = { data: T | null; error: { code: string; message: string } | null }

export type ClusterAlert = {
  id: string
  cluster_id: string
  fingerprint: string
  name: string
  severity: string
  namespace: string
  resource_kind: string
  resource_name: string
  summary: string
  state: string
  source: string
  starts_at: string
  last_seen_at: string
  ends_at?: string
}

export type AlertCoverage = {
  cluster_id: string
  status: string
  collected_at?: string
  message?: string
}

export type ClusterAlertPage = {
  items: ClusterAlert[]
  total: number
  coverage: AlertCoverage[]
  collected_at: string
}

async function fetchCurrentAlerts(clusterId?: string): Promise<ClusterAlertPage> {
  const params = new URLSearchParams({ page: '1', view: 'current' })
  if (clusterId) params.set('cluster', clusterId)
  const response = await fetch(`/api/v1/alerts?${params}`)
  const payload = await response.json() as ApiResponse<ClusterAlertPage>
  if (!response.ok || payload.error || !payload.data) {
    throw new Error(payload.error?.message || '读取集群告警失败。')
  }
  return payload.data
}

export function fetchCurrentClusterAlerts(clusterId: string): Promise<ClusterAlertPage> {
  return fetchCurrentAlerts(clusterId)
}

export function fetchCurrentFleetAlerts(): Promise<ClusterAlertPage> {
  return fetchCurrentAlerts()
}
