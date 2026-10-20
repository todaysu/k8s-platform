import { Alert, Button, Card, CardBody, Checkbox, Label, SearchInput, Spinner, Tab, Tabs, TabTitleText, Title } from '@patternfly/react-core'
import { SyncAltIcon, TimesIcon } from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchEventResource, fetchFleetEvents, type EventResourceSnapshot, type FleetEvent, type FleetEventFilters, type FleetEventIssue, type FleetEventObject, type FleetEventOverview } from '../api/clusters'
import './FleetEventsPage.css'

type Props = { clusters: { environment: string; id: string; name: string; technologyStack: string }[] }
type Filters = { cluster_id: string; environment: string; technology_stack: string; kind: string; namespace: string; scope: string; q: string; range: string; type: string }
const defaults: Filters = { cluster_id: '', environment: '', technology_stack: '', kind: '', namespace: '', scope: '', q: '', range: '24h', type: '' }
const ranges: Record<string, number> = { '1h': 1, '6h': 6, '24h': 24, '7d': 168, '30d': 720 }
function formatTime(value?: string) { if (!value) return '—'; const date = new Date(value); return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false }) }
const objectKey = (o: FleetEventObject) => `${o.cluster_id}/${o.kind}/${o.namespace}/${o.uid || o.name}`
const liveNames = { normal: '当前正常', abnormal: '当前异常', unverified: '未核验', deleted: '资源已删除', replaced: '同名资源已替换' }
function objectWindow(object: FleetEventObject, minutes = 15) {
 const last = new Date(object.last_observed_at).getTime()
 return { since: new Date(last - minutes * 60_000).toISOString(), until: new Date(Math.min(Date.now(), last + minutes * 60_000)).toISOString() }
}
function Pager({ page, pages, total, onChange, loading }: { page: number; pages: number; total: number; loading: boolean; onChange: (page: number) => void }) {
 return <div className="ks-event-pager"><span>共 {total} 项 · 第 {page} / {pages} 页</span><Button isDisabled={loading || page <= 1} onClick={() => onChange(page - 1)} variant="secondary">上一页</Button><Button isDisabled={loading || page >= pages} onClick={() => onChange(page + 1)} variant="secondary">下一页</Button></div>
}
function Evidence({ event }: { event: FleetEvent }) {
 return <details className="ks-event-evidence"><summary>{event.reason || event.type} · {event.message || '未提供消息'}</summary><pre>{event.message || '未提供消息'}</pre><dl><div><dt>首次 / 最近发生</dt><dd>{formatTime(event.first_observed_at)} / {formatTime(event.last_observed_at)}</dd></div><div><dt>累计发生次数</dt><dd>{event.occurrence_count}（非所选时间内新增次数）</dd></div><div><dt>来源</dt><dd>{event.reporting_controller || '未知'} / {event.reporting_instance || '—'}</dd></div><div><dt>Event UID</dt><dd>{event.event_uid}</dd></div><div><dt>资源 UID</dt><dd>{event.regarding_uid || '缺失，无法按 UID 核验'}</dd></div></dl></details>
}
function ResourceEventRow({ object, query, onInspect, initiallyExpanded = false, inspectMember, onExpansionChange }: { object: FleetEventObject; query: string; onInspect: () => void; initiallyExpanded?: boolean; onExpansionChange?: (open: boolean) => void; inspectMember: (object: FleetEventObject) => void }) {
 const [expanded, setExpanded] = useState(initiallyExpanded)
 const [page, setPage] = useState(1)
 const [result, setResult] = useState<FleetEventOverview | null>(null)
 const [error, setError] = useState('')
 useEffect(() => {
  if (!expanded || !onExpansionChange) return
  onExpansionChange(true)
  return () => onExpansionChange(false)
 }, [expanded, onExpansionChange])
 useEffect(() => {
  if (!expanded) return
  let active = true
  void fetchFleetEvents({ ...JSON.parse(query), cluster_id: object.cluster_id, namespace: object.namespace, kind: object.kind, object_uid: object.uid, object_name: object.name, view: 'events', page, limit: 10 }).then(data => { if (active) setResult(data) }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : '事件读取失败') })
  return () => { active = false }
 }, [expanded, query, object, page])
 return <><Tr><Td>{formatTime(object.last_observed_at)}</Td><Td><strong>{object.cluster_name}</strong><span>{object.namespace || '集群级'} · {object.scope === 'business' ? '业务' : '平台'}</span><span>{object.kind}/{object.name}</span><small>UID：{object.uid || '缺失，仅按名称合并'}</small>{object.grouping_note && <small>{object.grouping_note}</small>}</Td><Td><p className="ks-event-snippet">{object.message}</p></Td><Td>{object.event_count} 条事件{object.members?.length ? <small>涉及 {object.members.length} 个 Pod</small> : null}<small>累计发生 {object.occurrence_count} 次</small></Td><Td><Button aria-label={`展开事件 ${object.name}`} aria-expanded={expanded} onClick={() => setExpanded(value => !value)} variant="link">{expanded ? '收起事件' : '展开事件'}</Button><Button aria-label={`排查 ${object.name}`} onClick={onInspect} variant="link">核验对象</Button></Td></Tr>{expanded && <Tr><Td colSpan={5}><div className="ks-event-group-evidence">{!object.uid && <p>资源 UID 缺失，按名称关联，可能包含同名资源。</p>}{error && <Alert isInline variant="warning" title="关联事件读取失败">{error}</Alert>}{!result && !error && <Spinner aria-label="正在展开资源事件" />}{!!result?.failures.length && <Alert isInline variant="warning" title="关联事件数据不完整" />}{result?.items.map(event => <article className="ks-event-timeline" key={event.event_uid}><small>{formatTime(event.last_observed_at)} · {event.type}</small><Evidence event={event} /></article>)}{object.members?.length ? <Table aria-label={`工作负载 ${object.name} 的 Pod 事件`} variant="compact"><Thead><Tr><Th>最近发生</Th><Th>具体 Pod</Th><Th>最近消息</Th><Th>关联事件</Th><Th screenReaderText="排查 Pod" /></Tr></Thead><Tbody>{object.members.map(member => <ResourceEventRow key={objectKey(member)} object={member} query={query} initiallyExpanded inspectMember={inspectMember} onInspect={() => inspectMember(member)} />)}</Tbody></Table> : null}{result && result.pagination.total_items > 0 && <Pager page={page} pages={result.pagination.total_pages} total={result.pagination.total_items} loading={false} onChange={next => { setResult(null); setError(''); setPage(next) }} />}</div></Td></Tr>}</>
}
function diagnosis(message: string) {
 const text = message.toLowerCase()
 if (text.includes('proxyconnect') && text.includes('connection refused')) return '证据包含代理连接被拒绝。下一步检查容器运行时代理和镜像仓库连通性。'
 if (text.includes('unauthorized') || text.includes('pull access denied')) return '证据包含访问被拒绝。下一步核对镜像仓库地址及 imagePullSecrets。'
 if (text.includes('insufficient')) return '证据包含资源不足。下一步核对 Pod Requests 与节点可分配资源。'
 if (text.includes('imagepullbackoff') || text.includes('errimagepull') || text.includes('back-off pulling image')) return '这是镜像拉取重试表现；展开关联事件查看具体拉取错误，如未找到可扩大证据时间范围。'
 return '先核对当前状态，再查看具体错误及前后事件；相近时间的症状不能直接证明因果关系。'
}

export default function FleetEventsPage({ clusters }: Props) {
 const [params] = useState(() => new URLSearchParams(window.location.search))
 const [filters, setFilters] = useState<Filters>(() => Object.fromEntries(Object.entries(defaults).map(([key, value]) => [key, params.get(key) ?? value])) as Filters)
 const [view, setView] = useState<'issues' | 'events'>(() => params.get('view') === 'events' ? 'events' : 'issues')
 const [page, setPage] = useState(() => Math.max(1, Math.min(10000, Number(params.get('page')) || 1)))
 const [until, setUntil] = useState(() => { const saved = params.get('until'); return saved && Number.isFinite(Date.parse(saved)) ? new Date(saved).toISOString() : new Date().toISOString() })
 const [normal, setNormal] = useState(params.get('include_normal') === 'true')
 const toolbarRef = useRef<HTMLDivElement>(null)
 const [advanced, setAdvanced] = useState(() => ['environment','technology_stack','namespace','kind','type'].some(k => !!params.get(k)))
 useEffect(() => {
  if (!advanced) return
  const dismiss = (event: PointerEvent) => { if (!toolbarRef.current?.contains(event.target as Node)) setAdvanced(false) }
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setAdvanced(false) }
  document.addEventListener('pointerdown', dismiss); document.addEventListener('keydown', escape)
  return () => { document.removeEventListener('pointerdown', dismiss); document.removeEventListener('keydown', escape) }
 }, [advanced])
 const [overview, setOverview] = useState<FleetEventOverview | null>(null)
 const [error, setError] = useState('')
 const [loading, setLoading] = useState(true)
 const [issue, setIssue] = useState<FleetEventIssue | null>(null)
 const [objects, setObjects] = useState<FleetEventOverview | null>(null)
 const [objectPage, setObjectPage] = useState(1)
 const [objectError, setObjectError] = useState('')
 const [objectsLoading, setObjectsLoading] = useState(false)
 const [selected, setSelected] = useState<FleetEventObject | null>(null)
 const [snapshot, setSnapshot] = useState<EventResourceSnapshot | null>(null)
 const [snapshotError, setSnapshotError] = useState('')
 const [snapshotLoading, setSnapshotLoading] = useState(false)
 const [evidence, setEvidence] = useState<FleetEventOverview | null>(null)
 const [evidencePage, setEvidencePage] = useState(1)
 const [evidenceError, setEvidenceError] = useState('')
 const [evidenceLoading, setEvidenceLoading] = useState(false)
 const [rawContext, setRawContext] = useState<FleetEventObject | null>(() => {
  try { const saved = JSON.parse(params.get('object') || 'null') as FleetEventObject | null; return saved && ['cluster_id','cluster_name','kind','name','namespace','uid'].every(k => typeof saved[k as keyof FleetEventObject] === 'string') && Number.isFinite(Date.parse(saved.last_observed_at)) ? saved : null } catch { return null }
 })
 const [evidenceMinutes, setEvidenceMinutes] = useState(15)
 const [contextMinutes, setContextMinutes] = useState(() => [15,60,360,1440].includes(Number(params.get('evidence_minutes'))) ? Number(params.get('evidence_minutes')) : 15)
 const [liveRefresh, setLiveRefresh] = useState(0)
 const drawerOpen = !!issue || !!selected
 const [expandedGroups, setExpandedGroups] = useState(0)
 const expansionChanged = useCallback((open: boolean) => setExpandedGroups(count => Math.max(0, count + (open ? 1 : -1))), [])
 const issuePage = useRef(page)
 const change = (key: keyof Filters, value: string) => { setFilters(current => ({ ...current, [key]: value, ...(['environment', 'technology_stack'].includes(key) ? { cluster_id: '' } : {}) })); setPage(1); setRawContext(null); setIssue(null); setSelected(null) }
 const baseQuery = useCallback((): FleetEventFilters => {
  const duration = ranges[filters.range] ?? 24
  return { ...filters, since: new Date(new Date(until).getTime() - duration * 3600_000).toISOString(), until }
 }, [filters, until])
 useEffect(() => {
  let active = true
  const timer = window.setTimeout(() => {
   setLoading(true); setError('')
   const contextual = rawContext ? { ...objectWindow(rawContext, contextMinutes), cluster_id: rawContext.cluster_id, namespace: rawContext.namespace, kind: rawContext.kind, object_uid: rawContext.uid, object_name: rawContext.name, q: '', environment: '', technology_stack: '' } : {}
   void fetchFleetEvents({ ...baseQuery(), ...contextual, view: view === 'events' ? 'resources' : view, page, limit: 25, include_normal: view === 'events' && normal }).then(data => { if (active) setOverview(data) }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : '事件读取失败。') }).finally(() => { if (active) setLoading(false) })
  }, 250)
  return () => { active = false; window.clearTimeout(timer) }
 }, [baseQuery, view, page, normal, rawContext, contextMinutes])
 useEffect(() => {
  const query = new URLSearchParams({ ...filters, view, page: String(page), until, include_normal: String(normal) })
  if (rawContext) { query.set('object', JSON.stringify({ ...rawContext, message: '' })); query.set('evidence_minutes', String(contextMinutes)) }
  for (const [key, value] of [...query]) if (!value) query.delete(key)
  window.history.replaceState(window.history.state, '', `/events?${query}`)
 }, [filters, view, page, until, normal, rawContext, contextMinutes])
 useEffect(() => {
  if (drawerOpen || expandedGroups > 0 || page !== 1 || rawContext) return
  const timer = window.setInterval(() => { if (!document.hidden) setUntil(new Date().toISOString()) }, 30_000)
  return () => window.clearInterval(timer)
 }, [drawerOpen, expandedGroups, page, rawContext])
 useEffect(() => {
  if (!issue) return
  let active = true
  const timer = window.setTimeout(() => {
  setObjectsLoading(true); setObjectError(''); setObjects(null)
  void fetchFleetEvents({ ...baseQuery(), view: 'objects', issue: issue.fingerprint, page: objectPage, limit: 20 }).then(data => { if (active) setObjects(data) }).catch(cause => { if (active) setObjectError(cause instanceof Error ? cause.message : '影响对象读取失败。') }).finally(() => { if (active) setObjectsLoading(false) })
  }, 0)
  return () => { active = false; window.clearTimeout(timer) }
 }, [issue, objectPage, baseQuery])
 useEffect(() => {
  if (!selected) return
  let active = true
  const timer = window.setTimeout(() => {
  setSnapshot(null); setSnapshotError(''); setSnapshotLoading(true)
  void fetchEventResource(selected).then(data => { if (active) setSnapshot(data) }).catch(cause => { if (active) setSnapshotError(cause instanceof Error ? cause.message : '实时核验失败。') }).finally(() => { if (active) setSnapshotLoading(false) })
  }, 0)
  return () => { active = false; window.clearTimeout(timer) }
 }, [selected, liveRefresh])
 useEffect(() => {
  if (!selected) return
  let active = true
  const timer = window.setTimeout(() => {
  setEvidence(null); setEvidenceError(''); setEvidenceLoading(true)
  void fetchFleetEvents({ ...objectWindow(selected, evidenceMinutes), cluster_id: selected.cluster_id, kind: selected.kind, namespace: selected.namespace, object_uid: selected.uid, object_name: selected.name, view: 'events', page: evidencePage, limit: 10, include_normal: true }).then(data => { if (active) setEvidence(data) }).catch(cause => { if (active) setEvidenceError(cause instanceof Error ? cause.message : '关联证据读取失败。') }).finally(() => { if (active) setEvidenceLoading(false) })
  }, 0)
  return () => { active = false; window.clearTimeout(timer) }
 }, [selected, evidencePage, evidenceMinutes])
 const selectObject = (object: FleetEventObject) => { setSelected(object); setSnapshot(null); setEvidence(null); setSnapshotLoading(true); setEvidenceLoading(true); setEvidencePage(1); setEvidenceMinutes(15) }
 const closeDrawer = () => { setIssue(null); setSelected(null) }
 const selectView = (next: 'issues' | 'events') => { if (view === 'issues') issuePage.current = page; setView(next); setPage(next === 'issues' ? issuePage.current : 1); setRawContext(null); closeDrawer() }
 const navigate = (suffix: string) => {
  if (!selected) return
  window.history.pushState({}, '', `/clusters/${encodeURIComponent(selected.cluster_id)}/${suffix}`); window.dispatchEvent(new PopStateEvent('popstate'))
 }
 const openResource = () => {
  if (!selected) return
  if (selected.kind === 'Pod') navigate(`pods/${encodeURIComponent(selected.namespace)}/${encodeURIComponent(selected.name)}`)
  else if (selected.kind === 'Node') navigate('nodes')
  else navigate(`workloads/${selected.kind}/${encodeURIComponent(selected.namespace)}/${encodeURIComponent(selected.name)}`)
 }
 const loadingEmpty = loading && !overview
 const pagination = overview?.pagination
 const collectorProblems = overview?.collectors.filter(item => item.state !== 'watching') ?? []
 const liveSafe = snapshot && (snapshot.status === 'normal' || snapshot.status === 'abnormal')
 return <div className="ks-fleet-events">
  {error && <Alert isInline title={overview ? '刷新失败，当前显示上次结果' : '事件读取失败'} variant="danger">{error}</Alert>}
  {collectorProblems.length > 0 && <Alert isInline title={`${collectorProblems.length} 套集群的采集数据需要核对`} variant="warning">{collectorProblems.map(item => `${item.cluster_name}：${item.last_error || item.state}`).join('；')}。未采集到事件不代表集群正常。</Alert>}
  {(overview?.failures?.length ?? 0) > 0 && <Alert isInline title="部分集群事件读取失败" variant="warning">{overview!.failures.map(item => item.cluster_name).join('、')} 未纳入本次汇总，其他结果仍可查看。</Alert>}
  <div className="ks-fleet-event-summary">{[
   ['异常类别', overview?.summary.problem_groups], ['涉及对象', overview?.summary.affected_objects], ['涉及集群', overview?.summary.affected_clusters], ['采集连接', `${overview?.summary.collectors_healthy ?? 0} / ${overview?.summary.total_clusters ?? clusters.length}`, ''],
  ].map(([label, value]) => <Card key={String(label)}><CardBody><span>{label}</span><strong>{value ?? '—'}</strong></CardBody></Card>)}</div>
  <Tabs activeKey={view} aria-label="跨集群事件视图" onSelect={(_, key) => selectView(key as 'issues' | 'events')}><Tab eventKey="issues" title={<TabTitleText>异常排查</TabTitleText>} /><Tab eventKey="events" title={<TabTitleText>事件检索</TabTitleText>} /></Tabs>
  <Card><div ref={toolbarRef} className="ks-fleet-events-toolbar">
   <div className="ks-fleet-events-search"><label htmlFor="fleet-event-search">搜索</label><SearchInput id="fleet-event-search" aria-label="搜索跨集群事件" onChange={(_, value) => change('q', value)} onClear={() => change('q', '')} placeholder="Reason、资源名称、消息" value={filters.q} /></div>
   <label>时间<select aria-label="事件时间范围" onChange={e => { change('range', e.target.value); setUntil(new Date().toISOString()) }} value={filters.range}><option value="1h">近 1 小时</option><option value="6h">近 6 小时</option><option value="24h">近 24 小时</option><option value="7d">近 7 天</option><option value="30d">近 30 天</option></select></label>
   <label>集群<select aria-label="事件集群" onChange={e => change('cluster_id', e.target.value)} value={filters.cluster_id}><option value="">全部集群</option>{clusters.filter(c => (!filters.environment || c.environment === filters.environment) && (!filters.technology_stack || c.technologyStack === filters.technology_stack)).map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></label>
   <label>资源归属<select aria-label="事件资源归属" onChange={e => change('scope', e.target.value)} value={filters.scope}><option value="">全部归属</option><option value="business">业务</option><option value="platform">平台组件</option></select></label>
   <Button onClick={() => setAdvanced(!advanced)} aria-expanded={advanced} variant="link">更多筛选</Button><Button icon={<SyncAltIcon />} isLoading={loading} onClick={() => { setPage(1); setUntil(new Date().toISOString()) }} variant="secondary">刷新</Button>
   {advanced && <div className="ks-event-advanced"><label>环境<select aria-label="事件环境" value={filters.environment} onChange={e => change('environment', e.target.value)}><option value="">全部环境</option>{[...new Set(clusters.map(c => c.environment))].map(v => <option key={v}>{v}</option>)}</select></label><label>技术栈<select aria-label="事件技术栈" value={filters.technology_stack} onChange={e => change('technology_stack', e.target.value)}><option value="">全部技术栈</option>{[...new Set(clusters.map(c => c.technologyStack))].map(v => <option key={v}>{v}</option>)}</select></label><label>Namespace<input aria-label="事件 Namespace" value={filters.namespace} onChange={e => change('namespace', e.target.value)} placeholder="完整名称" /></label><label>资源类型<select aria-label="事件资源类型" value={filters.kind} onChange={e => change('kind', e.target.value)}><option value="">全部类型</option>{['Pod', 'Node', 'Deployment', 'ReplicaSet', 'StatefulSet', 'DaemonSet', 'PersistentVolumeClaim', 'Job'].map(v => <option key={v}>{v}</option>)}</select></label>{view === 'events' && <label>事件类型<select aria-label="检索事件类型" value={filters.type} onChange={e => { change('type', e.target.value); if (e.target.value === 'Normal') setNormal(true) }}><option value="">全部类型</option><option value="Warning">Warning</option><option value="Normal">Normal</option></select></label>}<Button onClick={() => { setFilters(defaults); setPage(1); setRawContext(null) }} variant="link">重置筛选</Button></div>}
  </div></Card>
  {overview && !overview.business_configured && <Alert isInline title="全局业务 Namespace 清单未配置" variant="info">当前均按平台组件统计，可在容量页面配置固定业务清单。</Alert>}
  {rawContext && <Alert isInline title={`对象上下文：${rawContext.cluster_name} / ${rawContext.namespace || '集群级'} / ${rawContext.kind}/${rawContext.name}`} variant="info">UID：{rawContext.uid || '缺失，仅按名称检索'}；{formatTime(objectWindow(rawContext, contextMinutes).since)} 至 {formatTime(objectWindow(rawContext, contextMinutes).until)}。<label>对象证据范围<select aria-label="检索对象时间范围" value={contextMinutes} onChange={e => { setContextMinutes(Number(e.target.value)); setPage(1) }}>{[[15,'前后 15 分钟'],[60,'前后 1 小时'],[360,'前后 6 小时'],[1440,'前后 24 小时']].map(([n,label]) => <option key={n} value={n}>{label}</option>)}</select></label><Button onClick={() => { setRawContext(null); setPage(1) }} variant="link">清除对象上下文</Button></Alert>}
  <Card className="ks-fleet-events-card"><div className="ks-fleet-events-section-title"><div><Title headingLevel="h2" size="lg">{view === 'issues' ? '异常线索' : '历史事件检索'}</Title></div>{view === 'events' && <Checkbox id="fleet-normal-events" label="包含 Normal 前后文" isChecked={normal} onChange={(_, value) => { setNormal(value); setPage(1) }} />}</div>
   {loadingEmpty ? <div className="ks-fleet-events-empty"><Spinner /><span>正在读取事件…</span></div> : view === 'issues' ? <div className="ks-fleet-events-table"><Table className="ks-event-issues-table" aria-label="跨集群异常列表" variant="compact"><Thead><Tr><Th>异常类别 / 证据样例</Th><Th>影响集群</Th><Th>影响对象</Th><Th>最近发生</Th><Th screenReaderText="排查" /></Tr></Thead><Tbody>{overview?.issues.map(item => <Tr key={item.fingerprint}><Td><strong>{item.title || item.reason}</strong><p className="ks-event-snippet">{item.message}</p><small>{item.event_count} 条事件记录 · 未核验当前状态</small></Td><Td>{item.affected_clusters} 套</Td><Td>{item.affected_objects} 个</Td><Td>{formatTime(item.last_observed_at)}</Td><Td><Button aria-label={`查看影响对象 ${item.title || item.reason}`} onClick={() => { setIssue(item); setObjectPage(1); setSelected(null) }} variant="link">查看影响对象</Button></Td></Tr>)}</Tbody></Table></div> : <div className="ks-fleet-events-table"><Table aria-label="跨集群事件检索" variant="compact"><Thead><Tr><Th>最近发生</Th><Th>集群 / 对象</Th><Th>最近事件消息</Th><Th>关联事件</Th><Th screenReaderText="排查" /></Tr></Thead><Tbody>{overview?.objects.map(object => {
    const query = JSON.stringify({ ...baseQuery(), ...(rawContext ? { ...objectWindow(rawContext, contextMinutes), q: '', environment: '', technology_stack: '' } : {}), include_normal: normal })
    return <ResourceEventRow key={`${objectKey(object)}/${query}/${page}`} object={object} query={query} onExpansionChange={expansionChanged} inspectMember={member => { setIssue(null); selectObject(member) }} onInspect={() => { setIssue(null); selectObject(object) }} />
   })}</Tbody></Table></div>}
   {!loading && (pagination?.total_items ?? 0) === 0 && <div className="ks-fleet-events-empty"><strong>{overview?.failures?.length ? '数据不完整，不能判断没有异常' : '当前筛选范围没有匹配记录'}</strong><span>可调整时间或筛选；历史记录为空不等于所有资源当前正常。</span></div>}
   {pagination && <Pager page={page} pages={pagination.total_pages} total={pagination.total_items} loading={loading} onChange={setPage} />}
  </Card>
  <details className="ks-event-collection"><summary>采集详情</summary><p>Normal 保留 {overview?.storage.normal_retention_days ?? 7} 天，异常证据保留 {overview?.storage.warning_retention_days ?? 30} 天。</p>{overview?.collectors.map(c => <p key={c.cluster_id}>{c.cluster_name} · {c.state} · 最近成功 {formatTime(c.last_success_at)}</p>)}</details>
  {drawerOpen && <aside aria-label="异常排查详情" className="ks-event-drawer"><header><div><Title headingLevel="h2" size="lg">{issue?.title || '对象排查'}</Title><p>历史线索 → 影响对象 → 当前状态与证据</p></div><Button aria-label="关闭排查详情" icon={<TimesIcon />} onClick={closeDrawer} variant="plain" /></header><div className="ks-event-drawer-body">
   {issue && !selected && <section><Alert isInline title="同类症状，不代表共同根因" variant="info">{issue.affected_clusters} 套集群、{issue.affected_objects} 个对象；请逐个选择核验。</Alert><details className="ks-event-evidence"><summary>查看问题证据样例</summary><pre>{issue.message}</pre></details><Title headingLevel="h3" size="md">受影响对象</Title>{objectError && <Alert isInline title="影响对象读取失败" variant="danger">{objectError}</Alert>}{(objects?.failures?.length ?? 0) > 0 && <Alert isInline title="部分影响对象数据缺失" variant="warning">{objects!.failures.map(f => f.cluster_name).join('、')} 的持久化事件读取失败。</Alert>}{objectsLoading ? <Spinner aria-label="正在读取影响对象" /> : <><div className="ks-fleet-events-table"><Table aria-label="受影响对象" variant="compact"><Thead><Tr><Th>集群 / Namespace</Th><Th>历史对象</Th><Th>最近发生</Th><Th screenReaderText="核验" /></Tr></Thead><Tbody>{objects?.objects?.map(object => <Tr key={objectKey(object)}><Td>{object.cluster_name}<small>{object.namespace || '集群级'}</small></Td><Td><strong>{object.kind}/{object.name}</strong><small>UID：{object.uid || '缺失'}</small></Td><Td>{formatTime(object.last_observed_at)}</Td><Td><Button aria-label={`核验并排查 ${object.name}`} onClick={() => selectObject(object)} variant="link">核验并排查</Button></Td></Tr>)}</Tbody></Table></div>{objects && <Pager page={objectPage} pages={objects.pagination.total_pages} total={objects.pagination.total_items} loading={objectsLoading} onChange={setObjectPage} />}</>}</section>}
   {selected && <section className="ks-event-resource">{selected.members?.length ? <div><Title headingLevel="h3" size="md">关联 Pod（{selected.members.length}）</Title>{selected.members.map(member => <Button key={objectKey(member)} variant="link" onClick={() => selectObject(member)}>排查 Pod/{member.name}（{member.event_count} 条事件）</Button>)}</div> : null}{issue && <Button variant="link" onClick={() => setSelected(null)}>返回受影响对象</Button>}<Title headingLevel="h3" size="lg">{selected.cluster_name} / {selected.namespace || '集群级'} / {selected.kind}/{selected.name}</Title><p>历史 UID：{selected.uid || '缺失'}</p>{snapshotLoading ? <Spinner aria-label="正在核验当前资源" /> : snapshotError ? <Alert isInline title="当前状态未核验" variant="warning">{snapshotError}，不能判断是否恢复。</Alert> : snapshot && <><Label color={snapshot.status === 'normal' ? 'green' : snapshot.status === 'abnormal' ? 'orange' : 'grey'}>{liveNames[snapshot.status]}</Label><p>{snapshot.detail}</p><small>核验于 {formatTime(snapshot.checked_at)}</small>{liveSafe && <><p>所属：{snapshot.owners.length ? snapshot.owners.map(o => `${o.kind}/${o.name}`).join(' → ') : selected.kind === 'Pod' ? '独立 Pod，无副本控制器' : '无控制器'}</p>{snapshot.node_name && <p>所在节点：{snapshot.node_name}</p>}{snapshot.containers.map(c => <p key={c.name}>容器 {c.name}：{c.state} · {c.reason || '—'} · 重启 {c.restarts} 次{c.exit_code !== undefined ? ` · 退出码 ${c.exit_code}` : ''}{c.previous_exit_code !== undefined ? ` · 上次退出 ${c.previous_exit_code}（${c.previous_reason || '未知原因'}）` : ''}</p>)}</>}</>}
    <div className="ks-event-resource-actions"><Button onClick={() => setLiveRefresh(n => n + 1)} variant="secondary">重新核验</Button>{liveSafe && ['Pod', 'Node', 'Deployment', 'StatefulSet', 'DaemonSet'].includes(selected.kind) && <Button onClick={openResource}>查看资源详情</Button>}{liveSafe && snapshot.owners.filter(o => ['Deployment', 'StatefulSet', 'DaemonSet'].includes(o.kind)).map(o => <Button key={o.uid} variant="link" onClick={() => navigate(`workloads/${o.kind}/${encodeURIComponent(selected.namespace)}/${encodeURIComponent(o.name)}`)}>查看 {o.kind}/{o.name}</Button>)}{liveSafe && selected.kind === 'Pod' && snapshot.containers.some(c => c.has_logs || c.has_previous_logs) && <Button onClick={() => navigate(`pods/${encodeURIComponent(selected.namespace)}/${encodeURIComponent(selected.name)}?section=logs`)} variant="link">查看容器日志</Button>}{liveSafe && selected.kind === 'Pod' && snapshot.containers.some(c => c.has_previous_logs) && <Button onClick={() => navigate(`pods/${encodeURIComponent(selected.namespace)}/${encodeURIComponent(selected.name)}?section=logs&previous=true`)} variant="link">查看上一次容器日志</Button>}{liveSafe && snapshot.node_name && <Button onClick={() => navigate('nodes')} variant="link">查看所在节点</Button>}</div>
    {liveSafe && selected.kind === 'Pod' && !snapshot.containers.some(c => c.has_logs || c.has_previous_logs) && <Alert isInline title="容器尚无可用日志" variant="info">容器可能尚未启动，请先查看拉取、挂载或调度事件。</Alert>}
    <Alert isInline title="排查提示（依据消息，不是根因结论）" variant="info">{diagnosis(evidence?.items.find(e => e.message.toLowerCase().includes('proxyconnect'))?.message || selected.message)}</Alert>
    <Title headingLevel="h3" size="md">关联事件与前后文</Title><label>证据时间范围<select aria-label="关联证据时间范围" value={evidenceMinutes} onChange={e => { setEvidenceMinutes(Number(e.target.value)); setEvidencePage(1) }}>{[[15,'前后 15 分钟'],[60,'前后 1 小时'],[360,'前后 6 小时'],[1440,'前后 24 小时']].map(([n,label]) => <option key={n} value={n}>{label}</option>)}</select></label><p>仅当前对象，包含 Normal，最近发生在前。{!selected.uid && '历史 UID 缺失，以下仅按名称关联，可能包含同名资源。'}</p>
    {evidenceMinutes === 15 && evidence && evidence.items.length > 0 && evidence.items.every(e => ['backoff','pulling'].includes(e.reason.toLowerCase())) && <Alert isInline title="当前范围仅有拉取重试记录" variant="info">最初失败可能发生得更早。<Button onClick={() => { setEvidenceMinutes(60); setEvidencePage(1) }} variant="link">扩大到前后 1 小时</Button></Alert>}
    {evidenceError && <Alert isInline title="关联证据读取失败" variant="warning">{evidenceError}</Alert>}{(evidence?.failures?.length ?? 0) > 0 && <Alert isInline title="关联事件数据缺失" variant="warning">读取失败，不能据此判断没有历史事件。</Alert>}{evidenceLoading ? <Spinner aria-label="正在读取关联证据" /> : <>{evidence?.items.map(e => <article className="ks-event-timeline" key={`${e.cluster_id}/${e.event_uid}`}><small>{formatTime(e.last_observed_at)} · {e.type}</small><Evidence event={e} /></article>)}{evidence && evidence.items.length === 0 && <p>{evidence.failures.length ? '关联事件读取失败，请稍后重新检索。' : '此时间段没有保留下来的关联记录，可调整范围继续检索。'}</p>}{evidence && <Pager page={evidencePage} pages={evidence.pagination.total_pages} total={evidence.pagination.total_items} loading={evidenceLoading} onChange={setEvidencePage} />}</>}
    {!selected.members?.length && <Button variant="link" onClick={() => { if (view === 'issues') issuePage.current = page; setRawContext(selected); setContextMinutes(evidenceMinutes); setNormal(true); setView('events'); setPage(1); closeDrawer() }}>在事件检索中查看此对象</Button>}
   </section>}
  </div></aside>}
 </div>
}
