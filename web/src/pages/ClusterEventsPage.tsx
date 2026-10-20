import { Alert, Button, Card, CardBody, Checkbox, Label, SearchInput, Spinner, Title } from '@patternfly/react-core'
import { ExclamationTriangleIcon, InfoCircleIcon, SyncAltIcon } from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { fetchClusterEventFacets, fetchClusterEvents, fetchClusterEventSummary, type ClusterEvent, type ClusterEventFacets, type ClusterEventPage, type ClusterEventSummary } from '../api/clusters'
import './ClusterEventsPage.css'

type Props = { clusterId: string }
const emptyPage: ClusterEventPage = { items: [], total: 0 }
const emptyFacets: ClusterEventFacets = { kinds: [], namespaces: [], types: [] }
const emptySummary: ClusterEventSummary = { affected_objects: 0, attention: 0, collector_state: 'starting', component_warnings: 0, total: 0, warning: 0 }

function formatTime(value?: string) { if (!value) return '—'; const date = new Date(value); return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false }) }
function isComponent(event: ClusterEvent) { return event.regarding_kind === 'Node' || event.regarding_namespace === 'kube-system' || event.regarding_namespace.startsWith('openshift-') }
function requiresAttention(event: ClusterEvent) { return event.type.toLowerCase() === 'warning' || ['backoff', 'failed', 'failedscheduling', 'failedmount', 'failedattachvolume', 'unhealthy', 'evicted', 'imagepullbackoff', 'errimagepull'].includes(event.reason.toLowerCase()) }

export default function ClusterEventsPage({ clusterId }: Props) {
  const [range, setRange] = useState('24h'); const [type, setType] = useState(''); const [namespace, setNamespace] = useState(''); const [kind, setKind] = useState(''); const [query, setQuery] = useState(''); const [component, setComponent] = useState(false)
  const [page, setPage] = useState(emptyPage); const [summary, setSummary] = useState(emptySummary); const [loading, setLoading] = useState(true); const [error, setError] = useState('')
  const [facets, setFacets] = useState(emptyFacets)
  const filters = useMemo(() => ({ component, kind, namespace, q: query, range, type }), [component, kind, namespace, query, range, type])
  const load = useCallback(async () => { setLoading(true); setError(''); try { const [events, overview, options] = await Promise.all([fetchClusterEvents(clusterId, filters), fetchClusterEventSummary(clusterId, range), fetchClusterEventFacets(clusterId, range)]); setPage(events); setSummary(overview); setFacets(options) } catch (cause) { setError(cause instanceof Error ? cause.message : '事件读取失败。') } finally { setLoading(false) } }, [clusterId, filters, range])
  useEffect(() => { const timer=window.setTimeout(() => void load(), 300); const interval=window.setInterval(() => void load(),30_000); return()=>{window.clearTimeout(timer);window.clearInterval(interval)} },[load])
  const openObject = (event: ClusterEvent) => { let path=''; if(event.regarding_kind==='Pod') path=`/clusters/${clusterId}/pods/${encodeURIComponent(event.regarding_namespace)}/${encodeURIComponent(event.regarding_name)}`; else if(['Deployment','StatefulSet','DaemonSet'].includes(event.regarding_kind)) path=`/clusters/${clusterId}/workloads/${event.regarding_kind}/${encodeURIComponent(event.regarding_namespace)}/${encodeURIComponent(event.regarding_name)}`; else if(event.regarding_kind==='Node') path=`/clusters/${clusterId}/nodes`; if(path){ window.history.pushState({},'',path);window.dispatchEvent(new PopStateEvent('popstate')) } }
  return <div className="ks-events-page">
    <div className="ks-events-heading"><div><Title headingLevel="h2" size="xl">事件</Title></div><Button icon={<SyncAltIcon/>} isLoading={loading} onClick={()=>void load()} variant="secondary">刷新</Button></div>
    {error && <Alert isInline title="事件数据读取失败" variant="danger">{error}</Alert>}
    <div className="ks-event-summary">
      <Card><CardBody><span>事件总数</span><strong>{summary.total}</strong><small>{range} 时间窗口</small></CardBody></Card>
      <Card className={summary.attention ? 'is-warning':''}><CardBody><span>需要关注</span><strong>{summary.attention}</strong><small>Kubernetes Warning {summary.warning} 条</small></CardBody></Card>
      <Card><CardBody><span>受影响对象</span><strong>{summary.affected_objects}</strong><small>Pod、Node 与工作负载</small></CardBody></Card>
      <Card className={summary.component_warnings ? 'is-warning':''}><CardBody><span>集群组件 Warning</span><strong>{summary.component_warnings}</strong><small>Node / kube-system</small></CardBody></Card>
    </div>
    {summary.collector_state !== 'watching' && <Alert isInline title="事件采集器正在建立连接" variant="warning">状态：{summary.collector_state}。{summary.last_error || '完成首次 List 后将进入持续 Watch。'}</Alert>}
    <Card className="ks-events-card">
      <div className="ks-events-toolbar">
        <SearchInput aria-label="搜索事件" onChange={(_,v)=>setQuery(v)} onClear={()=>setQuery('')} placeholder="搜索 Reason、对象、消息或来源" value={query}/>
        <select aria-label="时间范围" onChange={e=>setRange(e.target.value)} value={range}><option value="1h">近 1 小时</option><option value="6h">近 6 小时</option><option value="24h">近 24 小时</option><option value="7d">近 7 天</option><option value="30d">近 30 天</option></select>
        <select aria-label="事件类型" onChange={e=>setType(e.target.value)} value={type}><option value="">全部类型</option>{facets.types.map(value=><option key={value} value={value}>{value}</option>)}</select>
        <select aria-label="命名空间" onChange={e=>setNamespace(e.target.value)} value={namespace}><option value="">全部命名空间</option>{facets.namespaces.map(value=><option key={value} value={value}>{value}</option>)}</select>
        <select aria-label="资源类型" onChange={e=>setKind(e.target.value)} value={kind}><option value="">全部资源</option>{facets.kinds.map(value=><option key={value} value={value}>{value}</option>)}</select>
        <Checkbox id="component-events" isChecked={component} label="仅集群组件" onChange={(_,checked)=>setComponent(checked)}/>
      </div>
      {loading && page.items.length===0 ? <div className="ks-events-empty"><Spinner/><span>正在读取事件…</span></div> : <div className="ks-events-table"><Table aria-label="Kubernetes 事件" variant="compact"><Thead><Tr><Th>最后发生</Th><Th>类型</Th><Th>Reason</Th><Th>涉及对象</Th><Th>消息</Th><Th>次数</Th><Th>上报组件</Th></Tr></Thead><Tbody>{page.items.map(event=><Tr className={requiresAttention(event)?'ks-event-warning':''} key={event.event_uid}><Td dataLabel="最后发生">{formatTime(event.last_observed_at)}</Td><Td dataLabel="类型"><Label color={event.type.toLowerCase()==='warning'?'orange':'blue'} icon={event.type.toLowerCase()==='warning'?<ExclamationTriangleIcon/>:<InfoCircleIcon/>}>{event.type}</Label>{requiresAttention(event)&&event.type.toLowerCase()!=='warning'&&<Label color="orange" isCompact>需关注</Label>}</Td><Td dataLabel="Reason"><strong>{event.reason||'—'}</strong>{isComponent(event)&&<Label color="purple" isCompact>集群组件</Label>}</Td><Td dataLabel="涉及对象"><Button isDisabled={!['Pod','Node','Deployment','StatefulSet','DaemonSet'].includes(event.regarding_kind)} isInline onClick={()=>openObject(event)} variant="link">{event.regarding_kind}/{event.regarding_name}</Button><small>{event.regarding_namespace||'集群级'}</small></Td><Td dataLabel="消息" className="ks-event-message">{event.message||'—'}</Td><Td dataLabel="次数">{event.occurrence_count}</Td><Td dataLabel="上报组件">{event.reporting_controller||'—'}</Td></Tr>)}</Tbody></Table>{page.items.length===0&&<div className="ks-events-empty"><strong>当前筛选条件下没有事件</strong><span>可扩大时间范围或清除筛选条件。</span></div>}</div>}
      <div className="ks-events-footer">显示 {page.items.length} / {page.total} 条 · 最近采集 {formatTime(summary.last_success_at)}</div>
    </Card>
  </div>
}
