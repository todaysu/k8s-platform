import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { Alert, Button, Label, Spinner } from '@patternfly/react-core'
import './FleetAlertsPage.css'

type Cluster = { id: string; name: string; environment: string }
type AlertItem = {
  id: string; cluster_id: string; fingerprint: string; name: string; severity: string
  namespace: string; resource_kind: string; resource_name: string; summary: string
  state: string; source: string; starts_at: string; last_seen_at: string; ends_at?: string
  description: string; runbook_url: string; rule_url: string; labels: Record<string, string>
  owner_kind: string; owner_name: string; source_type: string; occurrence_count: number
}
type Coverage = { cluster_id: string; status: string; collected_at?: string; message?: string }
type AlertPage = { items: AlertItem[]; total: number; coverage: Coverage[]; collected_at: string; retention_days?: number; summary: { critical: number; warning: number; info: number; affected_clusters: number; affected_resources: number } }

const rangeOptions = [
  { value: 'all', label: '全部保留期' },
  { value: '1h', label: '最近 1 小时' },
  { value: '6h', label: '最近 6 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
]
const durationOptions = [{ value: '', label: '全部未恢复' }, { value: 'lt1h', label: '持续小于 1 小时' }, { value: '1h-6h', label: '持续 1–6 小时' }, { value: '6h-24h', label: '持续 6–24 小时' }, { value: 'gt24h', label: '持续超过 24 小时' }]
const safeURL = (value: string) => { try { const url = new URL(value); return ['http:', 'https:'].includes(url.protocol) ? value : '' } catch { return '' } }

const time = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
const duration = (from: string, to?: string) => {
  const seconds = Math.max(0, Math.floor((Date.parse(to || new Date().toISOString()) - Date.parse(from)) / 1000))
  if (!Number.isFinite(seconds)) return '—'
  if (seconds >= 86400) return `${Math.floor(seconds / 86400)} 天 ${Math.floor(seconds % 86400 / 3600)} 小时`
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds % 3600 / 60)} 分`
  return `${Math.max(1, Math.floor(seconds / 60))} 分`
}
const detailPath = (item: AlertItem) => {
  const cluster = encodeURIComponent(item.cluster_id)
  const namespace = encodeURIComponent(item.namespace)
  const name = encodeURIComponent(item.resource_name)
  if (item.resource_kind === 'Pod' && item.namespace && item.resource_name) return `/clusters/${cluster}/pods/${namespace}/${name}`
  if (item.resource_kind === 'Deployment' && item.namespace && item.resource_name) return `/clusters/${cluster}/workloads/Deployment/${namespace}/${name}`
  if (item.resource_kind === 'Node') return `/clusters/${cluster}/nodes`
  return `/clusters/${cluster}/overview`
}

export default function FleetAlertsPage({ clusters }: { clusters: Cluster[] }) {
  const initial = new URLSearchParams(location.search)
  const [view, setView] = useState(initial.get('view') === 'history' ? 'history' : 'current')
  const [cluster, setCluster] = useState(initial.get('cluster') || '')
  const [severity, setSeverity] = useState(initial.get('severity') || '')
  const [namespace, setNamespace] = useState(initial.get('namespace') || '')
  const [search, setSearch] = useState(initial.get('q') || '')
  const [range, setRange] = useState(rangeOptions.some(item => item.value === initial.get('range')) ? initial.get('range')! : 'all')
  const [durationFilter, setDurationFilter] = useState(durationOptions.some(item => item.value === initial.get('duration')) ? initial.get('duration')! : '')
  const [stateFilter, setStateFilter] = useState(initial.get('state') || '')
  const [sourceFilter, setSourceFilter] = useState(initial.get('source') || '')
  const [sort, setSort] = useState(initial.get('sort') || 'severity')
  const [resourceOnly, setResourceOnly] = useState(initial.get('resource') === 'linked')
  const [coverageExpanded, setCoverageExpanded] = useState(false)
  const [page, setPage] = useState(1)
  const [revision, setRevision] = useState(0)
  const [data, setData] = useState<AlertPage | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<AlertItem | null>(null)
  const clusterName = (id: string) => clusters.find(item => item.id === id)?.name || id
  const coverage = useMemo(() => {
    const statuses = new Map(data?.coverage.map(item => [item.cluster_id, item]) || [])
    const scoped = cluster ? clusters.filter(item => item.id === cluster) : clusters
    return scoped.map(item => ({ name: item.name, ...statuses.get(item.id), cluster_id: item.id, status: statuses.get(item.id)?.status || 'pending' }))
  }, [clusters, cluster, data])
  const healthy = coverage.filter(item => item.status === 'ok' && item.collected_at && Date.now() - Date.parse(item.collected_at) < 10 * 60_000).length

  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) setRevision(value => value + 1) }, 60_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({ view, page: String(page) })
    if (cluster) params.set('cluster', cluster)
    if (severity) params.set('severity', severity)
    if (namespace) params.set('namespace', namespace)
    if (search.trim()) params.set('q', search.trim())
    if (view === 'history') params.set('range', range)
    else if (durationFilter) params.set('duration', durationFilter)
    if (stateFilter) params.set('state', stateFilter)
    if (sourceFilter) params.set('source', sourceFilter)
    if (sort !== 'severity') params.set('sort', sort)
    if (resourceOnly) params.set('resource', 'linked')
    history.replaceState({}, '', `/alerts?${params}`)
    setLoading(true)
    setError('')
    void fetch(`/api/v1/alerts?${params}`, { signal: controller.signal })
      .then(async response => { const body = await response.json(); if (!response.ok || body.error) throw new Error(body.error?.message || '读取告警失败'); return body.data as AlertPage })
      .then(result => { if (!controller.signal.aborted) setData(result) })
      .catch(cause => { if (!controller.signal.aborted) setError((cause as Error).message) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [view, cluster, severity, namespace, search, range, durationFilter, stateFilter, sourceFilter, sort, resourceOnly, page, revision])

  const changeView = (next: string) => { setView(next); setStateFilter(''); setPage(1); setSelected(null) }
  const navigate = (path: string) => { history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')) }
  const totalPages = Math.max(1, Math.ceil((data?.total || 0) / 30))

  return <section className="ks-alerts" aria-label="跨集群告警">
    <div className="ks-alerts-intro"><p>从各集群 Alertmanager 读取并留存告警状态。告警与 Kubernetes Event 分别统计，采集失败不视为零告警。</p><Button variant="secondary" isLoading={loading} onClick={() => setRevision(value => value + 1)}>刷新告警</Button></div>
    <div className="ks-alerts-overview" aria-label="告警快捷筛选"><button type="button" className="is-critical" aria-pressed={severity === 'critical'} onClick={() => { setSeverity(value => value === 'critical' ? '' : 'critical'); setPage(1) }}><span>Critical</span><strong>{data?.summary?.critical ?? '—'}</strong><small>{severity === 'critical' ? '已筛选，再点取消' : '点击查看紧急告警'}</small></button><button type="button" className="is-warning" aria-pressed={severity === 'warning'} onClick={() => { setSeverity(value => value === 'warning' ? '' : 'warning'); setPage(1) }}><span>Warning</span><strong>{data?.summary?.warning ?? '—'}</strong><small>{severity === 'warning' ? '已筛选，再点取消' : '点击查看警告'}</small></button><button type="button" aria-pressed={Boolean(cluster)} onClick={() => { if (cluster) { setCluster(''); setPage(1) } else { document.querySelector<HTMLSelectElement>('[aria-label="告警集群"]')?.focus() } }}><span>受影响集群</span><strong>{data?.summary?.affected_clusters ?? '—'}</strong><small>{cluster ? '已筛选，点击恢复全部' : '点击选择集群'}</small></button><button type="button" aria-pressed={resourceOnly} onClick={() => { setResourceOnly(value => !value); setPage(1) }}><span>受影响资源</span><strong>{data?.summary?.affected_resources ?? '—'}</strong><small>{resourceOnly ? '已筛选关联资源' : '点击只看关联资源'}</small></button><button type="button" aria-expanded={coverageExpanded} aria-pressed={coverageExpanded} onClick={() => setCoverageExpanded(value => !value)}><span>采集覆盖</span><strong>{healthy} / {coverage.length}</strong><small>{coverageExpanded ? '已展开采集明细' : coverage.length - healthy ? `${coverage.length - healthy} 套异常，点击查看` : '点击查看采集明细'}</small></button></div>
    {coverageExpanded && <div className="ks-alerts-coverage-detail" aria-label="告警采集覆盖明细">{coverage.map(item => { const fresh = item.status === 'ok' && item.collected_at && Date.now() - Date.parse(item.collected_at) < 10 * 60_000; return <button type="button" key={item.cluster_id} onClick={() => { setCluster(item.cluster_id); setPage(1) }}><span className={fresh ? 'is-ok' : 'is-risk'} /> <strong>{item.name}</strong><small>{fresh ? `正常 · ${time(item.collected_at)}` : item.message || (item.status === 'pending' ? '等待首次采集' : '采集过期')}</small></button> })}</div>}
    {coverage.length > healthy && <Alert isInline variant="warning" title="部分集群告警未完整覆盖"><div className="ks-alerts-coverage">{coverage.filter(item => item.status !== 'ok' || !item.collected_at || Date.now() - Date.parse(item.collected_at) >= 10 * 60_000).slice(0, 8).map(item => <span key={item.cluster_id}>{item.name}：{item.status === 'error' ? item.message || '采集失败' : item.status === 'pending' ? '等待首次采集' : '采集已过期'}</span>)}</div></Alert>}
    {error && <Alert isInline variant="danger" title={error} />}
    <div className="ks-alerts-card">
      <div className="ks-alerts-tabs" role="tablist" aria-label="告警视图"><button type="button" role="tab" aria-selected={view === 'current'} onClick={() => changeView('current')}>当前告警</button><button type="button" role="tab" aria-selected={view === 'history'} onClick={() => changeView('history')}>已恢复历史</button></div>
      <div className="ks-alerts-filters"><label>{view === 'current' ? '持续时长' : '恢复时间'}<select aria-label="告警时间范围" value={view === 'current' ? durationFilter : range} onChange={event => { if (view === 'current') setDurationFilter(event.target.value); else setRange(event.target.value); setPage(1) }}>{(view === 'current' ? durationOptions : rangeOptions).map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select><small>{view === 'current' ? '默认不隐藏长时间未恢复告警' : '按恢复时间'}</small></label><label>集群<select aria-label="告警集群" value={cluster} onChange={event => { setCluster(event.target.value); setPage(1) }}><option value="">全部集群</option>{clusters.map(item => <option key={item.id} value={item.id}>{item.name} · {item.environment}</option>)}</select></label><label>级别<select aria-label="告警级别" value={severity} onChange={event => { setSeverity(event.target.value); setPage(1) }}><option value="">全部级别</option><option value="critical">Critical</option><option value="warning">Warning</option><option value="info">Info</option></select></label><label>状态<select aria-label="告警状态" value={stateFilter} onChange={event => { setStateFilter(event.target.value); setPage(1) }}><option value="">全部状态</option>{view === 'current' ? <><option value="firing">未恢复</option><option value="suppressed">已抑制</option></> : <option value="resolved">已恢复</option>}</select></label><label>来源<select aria-label="告警来源" value={sourceFilter} onChange={event => { setSourceFilter(event.target.value); setPage(1) }}><option value="">全部来源</option><option value="platform">平台系统组件</option><option value="business">业务资源</option></select></label><label>排序<select aria-label="告警排序" value={sort} onChange={event => { setSort(event.target.value); setPage(1) }}><option value="severity">严重程度优先</option><option value="duration">持续时间最长</option><option value="recent">最近发生</option><option value="recurrence">重复次数最多</option></select></label><label>命名空间<input aria-label="告警命名空间" value={namespace} onChange={event => { setNamespace(event.target.value); setPage(1) }} placeholder="全部命名空间" /></label><label className="ks-alerts-search">搜索<input aria-label="搜索告警" value={search} onChange={event => { setSearch(event.target.value); setPage(1) }} placeholder="告警名、资源或摘要" /></label></div>
      <div className="ks-alerts-table-wrap"><table className="ks-alerts-table"><thead><tr><th>告警 / 资源</th><th>集群 / 命名空间</th><th>来源</th><th>级别</th><th>首次出现</th><th>{view === 'current' ? '持续时间' : '恢复时间'}</th><th>状态</th><th>排查</th></tr></thead><tbody>{data?.items.map(item => <tr key={item.id}><td><strong>{item.name || '未命名告警'}</strong><small>{item.resource_kind ? `${item.resource_kind} / ${item.resource_name}` : item.summary || '未标注资源'}{item.occurrence_count > 1 ? ` · 保留期第 ${item.occurrence_count} 次` : ''}</small></td><td>{clusterName(item.cluster_id)}<small>{item.namespace || '集群级'}</small></td><td><Label isCompact>{item.source_type === 'business' ? '业务' : '平台'}</Label></td><td><Label isCompact status={item.severity === 'critical' ? 'danger' : item.severity === 'warning' ? 'warning' : 'info'}>{item.severity || '未标注'}</Label></td><td>{time(item.starts_at)}</td><td>{view === 'current' ? duration(item.starts_at) : time(item.ends_at)}</td><td><Label isCompact status={item.state === 'firing' ? 'danger' : item.state === 'suppressed' ? 'warning' : 'success'}>{item.state === 'firing' ? '未恢复' : item.state === 'suppressed' ? '已抑制' : '已恢复'}</Label></td><td><Button variant="link" isInline onClick={() => setSelected(item)}>查看详情</Button></td></tr>)}</tbody></table>{loading && !data && <div className="ks-alerts-empty"><Spinner size="lg" />正在读取告警…</div>}{!loading && !error && !data?.items.length && <div className="ks-alerts-empty">{healthy === 0 ? '尚无可用的告警采集结果' : view === 'history' ? '暂无已恢复告警' : '当前筛选范围暂无未恢复告警'}</div>}</div>
      <div className="ks-alerts-pager"><span>共 {data?.total ?? 0} 条 · 第 {page} / {totalPages} 页</span><Button variant="secondary" isDisabled={page <= 1} onClick={() => setPage(value => value - 1)}>上一页</Button><Button variant="secondary" isDisabled={page >= totalPages} onClick={() => setPage(value => value + 1)}>下一页</Button></div>
    </div>
    <p className="ks-alerts-footnote">历史仅包含平台成功采集到的告警；短于采集间隔的告警可能遗漏。已恢复记录当前保留 {data?.retention_days ?? 180} 天，管理员可在“平台管理 → 参数配置 → 告警保留”中调整。</p>
    {selected && createPortal(<div className="ks-alerts-backdrop" onClick={() => setSelected(null)}><aside className="ks-alerts-detail" role="dialog" aria-modal="true" aria-label="告警详情" onClick={event => event.stopPropagation()}><header><div><small>{clusterName(selected.cluster_id)} · {selected.namespace || '集群级'}</small><h2>{selected.name}</h2></div><Button variant="plain" aria-label="关闭告警详情" onClick={() => setSelected(null)}>×</Button></header><div className="ks-alerts-detail-body"><p>{selected.description || selected.summary || 'Alertmanager 未提供说明。'}</p><dl><dt>级别</dt><dd>{selected.severity || '未标注'}</dd><dt>资源</dt><dd>{selected.resource_kind ? `${selected.resource_kind} / ${selected.resource_name}` : '未标注'}</dd><dt>所属工作负载</dt><dd>{selected.owner_kind ? `${selected.owner_kind} / ${selected.owner_name}` : '未识别'}</dd><dt>资源分类</dt><dd>{selected.source_type === 'business' ? '业务资源' : '平台系统组件'}</dd><dt>保留期重复</dt><dd>{selected.occurrence_count || 1} 次</dd><dt>首次出现</dt><dd>{time(selected.starts_at)}</dd><dt>最近采集</dt><dd>{time(selected.last_seen_at)}</dd><dt>恢复时间</dt><dd>{time(selected.ends_at)}</dd><dt>Alertmanager</dt><dd>{selected.source}</dd><dt>指纹</dt><dd className="ks-alerts-fingerprint">{selected.fingerprint}</dd></dl>{Object.keys(selected.labels || {}).length > 0 && <details className="ks-alerts-labels"><summary>告警标签（{Object.keys(selected.labels).length}）</summary><dl>{Object.entries(selected.labels).map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl></details>}<div className="ks-alerts-detail-actions"><Button onClick={() => navigate(detailPath(selected))}>打开资源</Button><Button variant="secondary" onClick={() => navigate(`/monitoring?cluster=${encodeURIComponent(selected.cluster_id)}`)}>查看监控</Button><Button variant="link" onClick={() => navigate(`/events?cluster=${encodeURIComponent(selected.cluster_id)}&q=${encodeURIComponent(selected.resource_name || selected.name)}`)}>查看相关 Event</Button>{safeURL(selected.runbook_url) && <a href={safeURL(selected.runbook_url)} target="_blank" rel="noopener noreferrer">Runbook</a>}</div><p className="ks-alerts-detail-note">关联 Event 用资源名检索，属于排障线索，不代表已经确认同一故障。</p></div></aside></div>, document.body)}
  </section>
}
