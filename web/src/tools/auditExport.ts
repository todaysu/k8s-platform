import type {AuditRequest, AuditResource, RelationKind} from './audit'

export type AuditExportRelation = RelationKind | 'match'
export type AuditExportFormat = 'csv' | 'json'

export type AuditExportRow = { item: AuditRequest; relation: AuditExportRelation }

export type AuditExportFilters = {
  namespace: string
  query: string
  sourceIp: string
  user: string
  verb: string
  result: string
}

export type AuditExportScope = {
  type: 'requests' | 'resource'
  cluster: string
  resource?: AuditResource
  includeChildren?: boolean
  filters: AuditExportFilters
  coverage: { start: string; end: string }
}

export type AuditExportMeta = { generatedAt: string; scope: AuditExportScope }

export const auditRelationLabels: Record<AuditExportRelation, string> = {
  self: '当前资源',
  child: '下级 · 创建链',
  parent: '上级 · 触发方',
  'inferred-child': '下级 · 按命名推断',
  'inferred-parent': '上级 · 按命名推断',
  match: '匹配请求',
}

const resultLabel = (item: AuditRequest) => {
  if (item.stage === 'Panic') return '失败'
  if (item.code === null) return '未记录'
  return item.code >= 400 ? '失败' : '成功'
}

// Excel 会把以 = + - @ 开头的单元格当公式执行。审计里的用户名、资源名来自集群，
// 必须按不可信文本处理，导出前加前置单引号。
const safeText = (value: string) => (value && /^[=+\-@\t\r]/.test(value) ? `'${value}` : value)

const csvCell = (value: string | number | null | undefined) => {
  if (value === null || value === undefined) return ''
  if (typeof value === 'number') return String(value)
  const text = safeText(value).replaceAll(/\r?\n/g, ' ')
  return /[",]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

const csvHeaders = [
  '时间(UTC)', '结束时间(UTC)', '动作', '操作人', '来源 IP', '资源类型', '命名空间', '资源名称',
  'UID', '结果', '状态码', '原因', '审计级别', '阶段', '关联', '镜像', '副本数', 'auditID',
]

export function auditExportCSV(rows: AuditExportRow[]) {
  const lines = [
    csvHeaders.join(','),
    ...rows.map(({item, relation}) => [
      item.time,
      item.end,
      item.verb,
      item.user,
      item.sourceIPs.join(';'),
      item.resource.resource,
      item.resource.namespace,
      item.resource.name,
      item.resource.uid,
      resultLabel(item),
      item.code ?? '',
      item.reason,
      item.level,
      item.stages.join(' -> '),
      auditRelationLabels[relation],
      item.images.join(';'),
      item.replicas ?? '',
      item.id,
    ].map(csvCell).join(',')),
  ]
  // BOM 让 Excel 正确识别 UTF-8；CRLF 兼容 Windows。
  return '\uFEFF' + lines.join('\r\n') + '\r\n'
}

export function auditExportJSON(rows: AuditExportRow[], meta: AuditExportMeta, includeEvidence: boolean) {
  return JSON.stringify({
    schema: 'kubescope.audit.analysis/v1',
    generated_at: meta.generatedAt,
    source_cluster: meta.scope.cluster || '未绑定',
    scope: meta.scope,
    count: rows.length,
    requests: rows.map(({item, relation}) => ({
      audit_id: item.id,
      time: item.time,
      end: item.end,
      verb: item.verb,
      user: item.user,
      source_ips: item.sourceIPs,
      resource: item.resource,
      code: item.code,
      result: resultLabel(item),
      reason: item.reason,
      level: item.level,
      stages: item.stages,
      relation,
      images: item.images,
      replicas: item.replicas,
      ...(includeEvidence ? {evidence: item.evidence} : {}),
    })),
  }, null, 2)
}

const slug = (value: string, fallback: string) => {
  const text = value.trim()
    .replaceAll(/[^a-zA-Z0-9\u4e00-\u9fa5._-]+/g, '-')
    .replaceAll(/^-+|-+$/g, '')
  return (text || fallback).slice(0, 60)
}

export function auditExportFilename(scope: AuditExportScope, format: AuditExportFormat, now = new Date()) {
  const stamp = now.toISOString().replaceAll(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z')
  const cluster = slug(scope.cluster, 'offline')
  const target = scope.type === 'resource' && scope.resource
    ? slug(`${scope.resource.namespace}-${scope.resource.resource}-${scope.resource.name}`, 'resource')
    : 'requests'
  return `kubescope-audit-${cluster}-${target}-${stamp}.${format}`
}

export const auditExportMime = (format: AuditExportFormat) =>
  format === 'csv' ? 'text/csv;charset=utf-8' : 'application/json;charset=utf-8'
