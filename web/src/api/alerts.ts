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
  description: string
  runbook_url: string
  rule_url: string
  labels: Record<string, string>
  owner_kind: string
  owner_name: string
  source_type: string
  occurrence_count: number
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
  retention_days?: number
  summary: { critical: number; warning: number; info: number; affected_clusters: number; affected_resources: number }
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
