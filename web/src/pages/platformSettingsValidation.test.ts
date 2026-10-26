import { describe, expect, it } from 'vitest'
import type { PlatformSettings } from '../api/settings'
import { validateSection } from './platformSettingsValidation'

const base = (): PlatformSettings => ({
  runtime: { query_concurrency: 64, realtime_concurrency: 20, monitoring_concurrency: 8, session_concurrency: 10, per_cluster_concurrency: 2, auto_refresh_seconds: 30, operational_reports_enabled: false },
  collection: { health_interval_seconds: 60, health_timeout_seconds: 20, health_workers: 5, capacity_interval_seconds: 300, capacity_timeout_seconds: 45, capacity_workers: 3, resource_interval_seconds: 600, resource_timeout_seconds: 30, resource_workers: 2 },
  events: { normal_retention_days: 7, warning_retention_days: 30, rollup_retention_days: 365, daily_retention_days: 31, cleanup_batch_size: 5000 },
  alerts: { resolved_retention_days: 180, transition_retention_days: 180, hourly_retention_days: 400, daily_retention_days: 1095, coverage_retention_days: 400, cleanup_batch_size: 5000 },
  security: { kube_bench_image: 'repo/kube-bench:v1', kube_bench_image_amd64: '', kube_bench_image_arm64: '', image_pull_secret: '', namespace: 'kube-system', scan_workers: 2, host_workers: 5, scan_timeout_minutes: 30, pod_startup_timeout_minutes: 5 },
  java: { arthas_url: 'http://repo.internal/tools/arthas-bin.zip', arthas_version: '4.3.5', arthas_sha256: '', download_timeout_seconds: 60, diagnostic_timeout_minutes: 30, max_concurrency: 2, heap_dump_enabled: false, heap_dump_max_gib: 4, temporary_retention_hours: 24 },
  database: { backup_enabled: false, backup_time: '02:00', backup_retention_days: 30, backup_target_node: '', backup_target_path: '', space_warning_percent: 80, space_critical_percent: 90, connection_check_seconds: 30 },
  updated_at: {},
})

describe('platform settings validation', () => {
  it('accepts the recommended defaults for every section', () => {
    for (const section of ['runtime', 'collection', 'events', 'alerts', 'security', 'java', 'database'] as const) {
      expect(validateSection(section, base())).toEqual({})
    }
  })

  it('validates alert history retention and cleanup batches', () => {
    const settings = base()
    settings.alerts.resolved_retention_days = 0
    settings.alerts.cleanup_batch_size = 10
    expect(validateSection('alerts', settings)).toEqual({
      resolved_retention_days: '请输入 1–3650 之间的整数。',
      cleanup_batch_size: '请输入 100–50000 之间的整数。',
    })
  })

  it('reports out-of-range and non-integer runtime values', () => {
    const settings = base()
    settings.runtime.query_concurrency = 0
    settings.runtime.realtime_concurrency = 1.5
    expect(validateSection('runtime', settings)).toEqual({
      query_concurrency: '请输入 1–1000 之间的整数。',
      realtime_concurrency: '请输入整数。',
    })
  })

  it('keeps collection limits aligned with the server', () => {
    const settings = base()
    settings.collection.health_interval_seconds = 86400
    settings.collection.health_timeout_seconds = 600
    settings.collection.capacity_workers = 64
    expect(validateSection('collection', settings)).toEqual({
      health_interval_seconds: '请输入 15–3600 之间的整数。',
      health_timeout_seconds: '请输入 5–300 之间的整数。',
      capacity_workers: '请输入 1–32 之间的整数。',
    })
  })

  it('keeps warning retention at least as long as normal retention', () => {
    const settings = base()
    settings.events.warning_retention_days = 3
    expect(validateSection('events', settings)).toEqual({
      warning_retention_days: 'Warning 保留天数不能短于 Normal 事件保留天数。',
    })
  })

  it('requires an image source and a valid namespace for security scans', () => {
    const settings = base()
    settings.security.kube_bench_image = ''
    settings.security.namespace = 'Kube System'
    expect(validateSection('security', settings)).toEqual({
      kube_bench_image: '通用镜像和 x86_64 镜像至少填写一个。',
      namespace: '请输入合法的 Namespace（小写字母、数字和 -）。',
    })
    settings.security.kube_bench_image_arm64 = 'repo/kube-bench:arm64'
    expect(validateSection('security', settings).kube_bench_image).toBe('通用镜像和 x86_64 镜像至少填写一个。')
  })

  it('validates the Arthas URL and SHA-256 format', () => {
    const settings = base()
    settings.java.arthas_url = 'ftp://repo.internal/arthas.zip'
    settings.java.arthas_sha256 = 'abc'
    expect(validateSection('java', settings)).toEqual({
      arthas_url: '请输入 http:// 或 https:// 开头的有效地址。',
      arthas_sha256: 'SHA-256 需要 64 位十六进制字符。',
    })
  })

  it('accepts built-in Arthas and rejects unsafe offline overrides', () => {
    const settings = base()
    settings.java.arthas_url = ''
    expect(validateSection('java', settings)).toEqual({})
    settings.java.arthas_url = 'https://user:secret@repo.internal/arthas.zip'
    settings.java.arthas_version = '4.3.5;unsafe'
    settings.java.download_timeout_seconds = 0
    expect(validateSection('java', settings)).toEqual({
      arthas_url: '请输入 http:// 或 https:// 开头的有效地址。',
      arthas_version: '请输入更新包版本，例如 4.3.5。',
      download_timeout_seconds: '请输入 5–600 之间的整数。',
    })
  })

  it('validates backup targets and disk thresholds', () => {
    const settings = base()
    settings.database.backup_enabled = true
    settings.database.backup_time = '2:00'
    settings.database.backup_target_path = 'data/backup'
    settings.database.space_warning_percent = 90
    settings.database.space_critical_percent = 80
    expect(validateSection('database', settings)).toEqual({
      backup_time: '时间格式应为 HH:MM。',
      backup_target_node: '启用定时备份后必须填写目标节点。',
      backup_target_path: '启用定时备份后必须填写以 / 开头的绝对路径。',
      space_critical_percent: '严重阈值必须高于告警阈值。',
    })
  })
})
