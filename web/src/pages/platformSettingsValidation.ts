import type { PlatformSettings, SettingsSection } from '../api/settings'

export type FieldErrors = Record<string, string>

const integer = (errors: FieldErrors, key: string, value: number, min: number, max: number) => {
  if (!Number.isInteger(value)) {
    errors[key] = '请输入整数。'
    return
  }
  if (value < min || value > max) {
    errors[key] = `请输入 ${min}–${max} 之间的整数。`
  }
}

const dnsLabel = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/
const sha256 = /^[a-fA-F0-9]{64}$/
const clock = /^([01]\d|2[0-3]):[0-5]\d$/

/** Mirrors the server-side rules so invalid values are caught before the PUT request. */
export function validateSection(section: SettingsSection, settings: PlatformSettings): FieldErrors {
  const errors: FieldErrors = {}

  if (section === 'runtime') {
    const value = settings.runtime
    integer(errors, 'query_concurrency', value.query_concurrency, 1, 1000)
    integer(errors, 'realtime_concurrency', value.realtime_concurrency, 1, 200)
    integer(errors, 'monitoring_concurrency', value.monitoring_concurrency, 1, 100)
    integer(errors, 'session_concurrency', value.session_concurrency, 1, 100)
    integer(errors, 'per_cluster_concurrency', value.per_cluster_concurrency, 1, 20)
    integer(errors, 'auto_refresh_seconds', value.auto_refresh_seconds, 10, 600)
    return errors
  }

  if (section === 'collection') {
    const value = settings.collection
    const groups = [
      ['health', 15, 3600, 5, 300, 64],
      ['capacity', 60, 86400, 5, 600, 32],
      ['resource', 60, 86400, 5, 600, 32],
    ] as const
    for (const [prefix, intervalMin, intervalMax, timeoutMin, timeoutMax, workerMax] of groups) {
      integer(errors, `${prefix}_interval_seconds`, value[`${prefix}_interval_seconds`], intervalMin, intervalMax)
      integer(errors, `${prefix}_timeout_seconds`, value[`${prefix}_timeout_seconds`], timeoutMin, timeoutMax)
      integer(errors, `${prefix}_workers`, value[`${prefix}_workers`], 1, workerMax)
    }
    return errors
  }

  if (section === 'events') {
    const value = settings.events
    integer(errors, 'normal_retention_days', value.normal_retention_days, 1, 365)
    integer(errors, 'warning_retention_days', value.warning_retention_days, 1, 730)
    integer(errors, 'rollup_retention_days', value.rollup_retention_days, 30, 3650)
    integer(errors, 'daily_retention_days', value.daily_retention_days, 7, 730)
    integer(errors, 'cleanup_batch_size', value.cleanup_batch_size, 100, 50000)
    if (!errors.normal_retention_days && !errors.warning_retention_days && value.warning_retention_days < value.normal_retention_days) {
      errors.warning_retention_days = 'Warning 保留天数不能短于 Normal 事件保留天数。'
    }
    return errors
  }

  if (section === 'alerts') {
    const value = settings.alerts
    integer(errors, 'resolved_retention_days', value.resolved_retention_days, 1, 3650)
    integer(errors, 'transition_retention_days', value.transition_retention_days, 1, 3650)
    integer(errors, 'hourly_retention_days', value.hourly_retention_days, 30, 3650)
    integer(errors, 'daily_retention_days', value.daily_retention_days, 90, 3650)
    integer(errors, 'coverage_retention_days', value.coverage_retention_days, 30, 3650)
    integer(errors, 'cleanup_batch_size', value.cleanup_batch_size, 100, 50000)
    return errors
  }

  if (section === 'security') {
    const value = settings.security
    if (!value.kube_bench_image.trim() && !value.kube_bench_image_amd64.trim()) {
      errors.kube_bench_image = '通用镜像和 x86_64 镜像至少填写一个。'
    }
    if (!dnsLabel.test(value.namespace)) {
      errors.namespace = '请输入合法的 Namespace（小写字母、数字和 -）。'
    }
    integer(errors, 'scan_workers', value.scan_workers, 1, 20)
    integer(errors, 'host_workers', value.host_workers, 1, 50)
    integer(errors, 'scan_timeout_minutes', value.scan_timeout_minutes, 5, 240)
    integer(errors, 'pod_startup_timeout_minutes', value.pod_startup_timeout_minutes, 1, 30)
    return errors
  }

  if (section === 'java') {
    const value = settings.java
    if (value.arthas_url !== '') {
      try {
        const parsed = new URL(value.arthas_url)
        if ((parsed.protocol !== 'http:' && parsed.protocol !== 'https:') || !parsed.host || parsed.username || parsed.password || parsed.hash) throw new Error('unsupported URL')
      } catch {
        errors.arthas_url = '请输入 http:// 或 https:// 开头的有效地址。'
      }
      if (!/^[0-9]+\.[0-9]+\.[0-9]+(?:[.-][A-Za-z0-9]+)*$/.test(value.arthas_version) || value.arthas_version.length > 64) {
        errors.arthas_version = '请输入更新包版本，例如 4.3.5。'
      }
    }
    if (value.arthas_sha256 !== '' && !sha256.test(value.arthas_sha256)) {
      errors.arthas_sha256 = 'SHA-256 需要 64 位十六进制字符。'
    }
    integer(errors, 'max_concurrency', value.max_concurrency, 1, 20)
    integer(errors, 'download_timeout_seconds', value.download_timeout_seconds, 5, 600)
    integer(errors, 'diagnostic_timeout_minutes', value.diagnostic_timeout_minutes, 5, 240)
    integer(errors, 'temporary_retention_hours', value.temporary_retention_hours, 1, 168)
    integer(errors, 'heap_dump_max_gib', value.heap_dump_max_gib, 1, 100)
    return errors
  }

  const value = settings.database
  if (!clock.test(value.backup_time)) {
    errors.backup_time = '时间格式应为 HH:MM。'
  }
  if (value.backup_enabled) {
    if (!value.backup_target_node.trim()) errors.backup_target_node = '启用定时备份后必须填写目标节点。'
    if (!value.backup_target_path.trim().startsWith('/')) errors.backup_target_path = '启用定时备份后必须填写以 / 开头的绝对路径。'
  }
  integer(errors, 'backup_retention_days', value.backup_retention_days, 1, 365)
  integer(errors, 'space_warning_percent', value.space_warning_percent, 50, 95)
  integer(errors, 'space_critical_percent', value.space_critical_percent, 60, 99)
  integer(errors, 'connection_check_seconds', value.connection_check_seconds, 10, 600)
  if (!errors.space_warning_percent && !errors.space_critical_percent && value.space_critical_percent <= value.space_warning_percent) {
    errors.space_critical_percent = '严重阈值必须高于告警阈值。'
  }
  return errors
}
