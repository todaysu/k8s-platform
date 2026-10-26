export type RuntimeSettings = {
  query_concurrency: number
  realtime_concurrency: number
  monitoring_concurrency: number
  session_concurrency: number
  per_cluster_concurrency: number
  auto_refresh_seconds: number
  operational_reports_enabled: boolean
}

export type CollectionSettings = {
  health_interval_seconds: number
  health_timeout_seconds: number
  health_workers: number
  capacity_interval_seconds: number
  capacity_timeout_seconds: number
  capacity_workers: number
  resource_interval_seconds: number
  resource_timeout_seconds: number
  resource_workers: number
}

export type EventSettings = {
  normal_retention_days: number
  warning_retention_days: number
  rollup_retention_days: number
  daily_retention_days: number
  cleanup_batch_size: number
}

export type AlertSettings = {
  resolved_retention_days: number
  transition_retention_days: number
  hourly_retention_days: number
  daily_retention_days: number
  coverage_retention_days: number
  cleanup_batch_size: number
}

export type SecuritySettings = {
  kube_bench_image: string
  kube_bench_image_amd64: string
  kube_bench_image_arm64: string
  image_pull_secret: string
  namespace: string
  scan_workers: number
  host_workers: number
  scan_timeout_minutes: number
  pod_startup_timeout_minutes: number
}

export type JavaSettings = {
  arthas_url: string
  arthas_version: string
  arthas_sha256: string
  download_timeout_seconds: number
  diagnostic_timeout_minutes: number
  max_concurrency: number
  heap_dump_enabled: boolean
  heap_dump_max_gib: number
  temporary_retention_hours: number
}

export type DatabaseSettings = {
  backup_enabled: boolean
  backup_time: string
  backup_retention_days: number
  backup_target_node: string
  backup_target_path: string
  space_warning_percent: number
  space_critical_percent: number
  connection_check_seconds: number
}

export type PlatformSettings = {
  runtime: RuntimeSettings
  collection: CollectionSettings
  events: EventSettings
  alerts: AlertSettings
  security: SecuritySettings
  java: JavaSettings
  database: DatabaseSettings
  updated_at: Record<string, string>
}

export type SettingsSection = Exclude<keyof PlatformSettings, 'updated_at'>

export type SettingsApplyStatus = {
  status: 'restarting' | 'saved' | 'failed' | 'restart_unavailable'
  targets?: string[]
  message: string
}

export type SettingsSaveResult = {
  settings: PlatformSettings
  apply: SettingsApplyStatus
}

type ApiResponse<T> = { data?: T; error?: { code: string; message: string } }

async function readResponse<T>(response: Response, fallback: string): Promise<T> {
  let payload: ApiResponse<T>
  try {
    payload = await response.json() as ApiResponse<T>
  } catch {
    throw new Error('平台返回了无法识别的配置响应。')
  }
  if (!response.ok || payload.error || !payload.data) {
    throw new Error(payload.error?.message ?? fallback)
  }
  return payload.data
}

export async function fetchPlatformSettings(): Promise<PlatformSettings> {
  return readResponse<PlatformSettings>(await fetch('/api/v1/settings'), '读取平台配置失败。')
}

export async function savePlatformSettings<K extends SettingsSection>(section: K, value: PlatformSettings[K]): Promise<SettingsSaveResult> {
  const response = await fetch(`/api/v1/settings/${encodeURIComponent(section)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(value),
  })
  return readResponse<SettingsSaveResult>(response, '保存平台配置失败。')
}
