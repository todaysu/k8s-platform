import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { Alert, Button, Label, Spinner } from '@patternfly/react-core'
import './FleetAlertsPage.css'

type Cluster = { id: string; name: string; environment: string }
type AlertItem = {
  id: string; cluster_id: string; fingerprint: string; name: string; severity: string
  namespace: string; resource_kind: string; resource_name: string; summary: string
  state: string; source: string; starts_at: string; last_seen_at: string; ends_at?: string
}
type Coverage = { cluster_id: string; status: string; collected_at?: string; message?: string }
type AlertPage = { items: AlertItem[]; total: number; coverage: Coverage[]; collected_at: string; retention_days?: number }

const rangeOptions = [
  { value: 'all', label: '全部保留期' },
  { value: '1h', label: '最近 1 小时' },
  { value: '6h', label: '最近 6 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
]

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
    params.set('range', range)
    history.replaceState({}, '', `/alerts?${params}`)
    setLoading(true)
    setError('')
    void fetch(`/api/v1/alerts?${params}`, { signal: controller.signal })
      .then(async response => { const body = await response.json(); if (!response.ok || body.error) throw new Error(body.error?.message || '读取告警失败'); return body.data as AlertPage })
      .then(result => { if (!controller.signal.aborted) setData(result) })
      .catch(cause => { if (!controller.signal.aborted) setError((cause as Error).message) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [view, cluster, severity, namespace, search, range, page, revision])

  const changeView = (next: string) => { setView(next); setPage(1); setSelected(null) }
  const navigate = (path: string) => { history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')) }
  const totalPages = Math.max(1, Math.ceil((data?.total || 0) / 30))

  return <section className="ks-alerts" aria-label="跨集群告警">
    <div className="ks-alerts-intro"><p>从各集群 Alertmanager 读取并留存告警状态。告警与 Kubernetes Event 分别统计，采集失败不视为零告警。</p><Button variant="secondary" isLoading={loading} onClick={() => setRevision(value => value + 1)}>刷新告警</Button></div>
    <div className="ks-alerts-overview"><div><span>当前筛选结果</span><strong>{data?.total ?? '—'}</strong><small>{view === 'current' ? '未恢复告警' : '已恢复告警'}</small></div><div><span>采集覆盖</span><strong>{healthy} / {coverage.length}</strong><small>近 10 分钟成功采集</small></div><div><span>采集异常或待采集</span><strong>{coverage.length - healthy}</strong><small>不能据此判断集群无告警</small></div></div>
    {coverage.length > healthy && <Alert isInline variant="warning" title="部分集群告警未完整覆盖"><div className="ks-alerts-coverage">{coverage.filter(item => item.status !== 'ok' || !item.collected_at || Date.now() - Date.parse(item.collected_at) >= 10 * 60_000).slice(0, 8).map(item => <span key={item.cluster_id}>{item.name}：{item.status === 'error' ? item.message || '采集失败' : item.status === 'pending' ? '等待首次采集' : '采集已过期'}</span>)}</div></Alert>}
    {error && <Alert isInline variant="danger" title={error} />}
    <div className="ks-alerts-card">
      <div className="ks-alerts-tabs" role="tablist" aria-label="告警视图"><button type="button" role="tab" aria-selected={view === 'current'} onClick={() => changeView('current')}>当前告警</button><button type="button" role="tab" aria-selected={view === 'history'} onClick={() => changeView('history')}>已恢复历史</button></div>
      <div className="ks-alerts-filters"><label>时间范围<select aria-label="告警时间范围" value={range} onChange={event => { setRange(event.target.value); setPage(1) }}>{rangeOptions.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select><small>{view === 'current' ? '按首次触发时间' : '按恢复时间'}</small></label><label>集群<select aria-label="告警集群" value={cluster} onChange={event => { setCluster(event.target.value); setPage(1) }}><option value="">全部集群</option>{clusters.map(item => <option key={item.id} value={item.id}>{item.name} · {item.environment}</option>)}</select></label><label>级别<select aria-label="告警级别" value={severity} onChange={event => { setSeverity(event.target.value); setPage(1) }}><option value="">全部级别</option><option value="critical">Critical</option><option value="warning">Warning</option><option value="info">Info</option></select></label><label>命名空间<input aria-label="告警命名空间" value={namespace} onChange={event => { setNamespace(event.target.value); setPage(1) }} placeholder="全部命名空间" /></label><label className="ks-alerts-search">搜索<input aria-label="搜索告警" value={search} onChange={event => { setSearch(event.target.value); setPage(1) }} placeholder="告警名、资源或摘要" /></label></div>
      <div className="ks-alerts-table-wrap"><table className="ks-alerts-table"><thead><tr><th>告警 / 资源</th><th>集群 / 命名空间</th><th>级别</th><th>首次出现</th><th>{view === 'current' ? '持续时间' : '恢复时间'}</th><th>状态</th><th>排查</th></tr></thead><tbody>{data?.items.map(item => <tr key={item.id}><td><strong>{item.name || '未命名告警'}</strong><small>{item.resource_kind ? `${item.resource_kind} / ${item.resource_name}` : item.summary || '未标注资源'}</small></td><td>{clusterName(item.cluster_id)}<small>{item.namespace || '集群级'}</small></td><td><Label isCompact status={item.severity === 'critical' ? 'danger' : item.severity === 'warning' ? 'warning' : 'info'}>{item.severity || '未标注'}</Label></td><td>{time(item.starts_at)}</td><td>{view === 'current' ? duration(item.starts_at) : time(item.ends_at)}</td><td><Label isCompact status={item.state === 'firing' ? 'danger' : item.state === 'suppressed' ? 'warning' : 'success'}>{item.state === 'firing' ? '未恢复' : item.state === 'suppressed' ? '已抑制' : '已恢复'}</Label></td><td><Button variant="link" isInline onClick={() => setSelected(item)}>查看详情</Button></td></tr>)}</tbody></table>{loading && !data && <div className="ks-alerts-empty"><Spinner size="lg" />正在读取告警…</div>}{!loading && !error && !data?.items.length && <div className="ks-alerts-empty">{healthy === 0 ? '尚无可用的告警采集结果' : view === 'history' ? '暂无已恢复告警' : '当前筛选范围暂无未恢复告警'}</div>}</div>
      <div className="ks-alerts-pager"><span>共 {data?.total ?? 0} 条 · 第 {page} / {totalPages} 页</span><Button variant="secondary" isDisabled={page <= 1} onClick={() => setPage(value => value - 1)}>上一页</Button><Button variant="secondary" isDisabled={page >= totalPages} onClick={() => setPage(value => value + 1)}>下一页</Button></div>
    </div>
    <p className="ks-alerts-footnote">历史仅包含平台成功采集到的告警；短于采集间隔的告警可能遗漏。已恢复记录当前保留 {data?.retention_days ?? 180} 天，管理员可在“平台管理 → 参数配置 → 告警保留”中调整。</p>
    {selected && createPortal(<div className="ks-alerts-backdrop" onClick={() => setSelected(null)}><aside className="ks-alerts-detail" role="dialog" aria-modal="true" aria-label="告警详情" onClick={event => event.stopPropagation()}><header><div><small>{clusterName(selected.cluster_id)} · {selected.namespace || '集群级'}</small><h2>{selected.name}</h2></div><Button variant="plain" aria-label="关闭告警详情" onClick={() => setSelected(null)}>×</Button></header><div className="ks-alerts-detail-body"><p>{selected.summary || 'Alertmanager 未提供摘要。'}</p><dl><dt>级别</dt><dd>{selected.severity || '未标注'}</dd><dt>资源</dt><dd>{selected.resource_kind ? `${selected.resource_kind} / ${selected.resource_name}` : '未标注'}</dd><dt>首次出现</dt><dd>{time(selected.starts_at)}</dd><dt>最近采集</dt><dd>{time(selected.last_seen_at)}</dd><dt>恢复时间</dt><dd>{time(selected.ends_at)}</dd><dt>来源</dt><dd>{selected.source}</dd><dt>指纹</dt><dd className="ks-alerts-fingerprint">{selected.fingerprint}</dd></dl><div className="ks-alerts-detail-actions"><Button onClick={() => navigate(detailPath(selected))}>打开资源</Button><Button variant="secondary" onClick={() => navigate(`/monitoring?cluster=${encodeURIComponent(selected.cluster_id)}`)}>查看监控</Button><Button variant="link" onClick={() => navigate(`/events?cluster=${encodeURIComponent(selected.cluster_id)}&q=${encodeURIComponent(selected.resource_name || selected.name)}`)}>查看相关 Event</Button></div><p className="ks-alerts-detail-note">关联 Event 用资源名检索，属于排障线索，不代表已经确认同一故障。</p></div></aside></div>, document.body)}
  </section>
}
