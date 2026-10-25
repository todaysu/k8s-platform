import {useEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import {useFullscreenPanel} from '../monitoring/useFullscreenPanel'
import {validateTimeRange,localDateTime,type TimeRange} from '../monitoring/timeRange'
import {Alert,Button} from '@patternfly/react-core'
import MetricChart from '../monitoring/MetricChart'
import ResourceCombobox from '../monitoring/ResourceCombobox'
import type {MonitoringSnapshot} from '../monitoring/types'
import './FleetMonitoringPage.css'
type Cluster={id:string;name:string;environment:string;status:string;cpuUtilization:number|null;memoryUtilization:number|null;metricsAvailable:boolean;nodes:{ready:number;total:number}|null;lastChecked:string}
const views=[['overview','总览'],['nodes','节点性能'],['pods','Pod 资源'],['apiserver','API Server'],['etcd','etcd']] as const
export default function FleetMonitoringPage({clusters,onOpenCluster}:{clusters:Cluster[];onOpenCluster:(id:string)=>void;onRefresh:()=>void}){
 const initial=new URLSearchParams(location.search)
 const [selected,setSelected]=useState(initial.get('cluster')||''),[view,setView]=useState(initial.get('view')||'overview'),[hours,setHours]=useState(initial.get('from')&&initial.get('to')?'custom':initial.get('hours')||'1'),[namespace,setNamespace]=useState(initial.get('namespace')||''),[pod,setPod]=useState(initial.get('pod')||''),[refresh,setRefresh]=useState(0),[auto,setAuto]=useState('0')
 const [data,setData]=useState<MonitoringSnapshot|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState(''),[errorCode,setErrorCode]=useState(''),[pods,setPods]=useState<{name:string;namespace:string}[]>([]),[resourceError,setResourceError]=useState('')
 const [absolute,setAbsolute]=useState<TimeRange|null>(initial.get('from')&&initial.get('to')?{start:initial.get('from')!,end:initial.get('to')!}:null)
 const [fullscreen,setFullscreen]=useState(false)
 const root=useRef<HTMLDivElement|null>(null)
 useFullscreenPanel(fullscreen,()=>setFullscreen(false),root)
 const applyRange=(start:number,end:number)=>{setAbsolute({start:new Date(start*1000).toISOString(),end:new Date(end*1000).toISOString()});setHours('custom');setAuto('0')}
 const scopeRef=useRef('')
 const requestRef=useRef<AbortController|null>(null)
 useEffect(()=>{const restore=()=>{const params=new URLSearchParams(location.search);setSelected(params.get('cluster')||'');setView(params.get('view')||'overview');setHours(params.get('from')&&params.get('to')?'custom':params.get('hours')||'1');setAbsolute(params.get('from')&&params.get('to')?{start:params.get('from')!,end:params.get('to')!}:null);setNamespace(params.get('namespace')||'');setPod(params.get('pod')||'')};window.addEventListener('popstate',restore);return()=>window.removeEventListener('popstate',restore)},[])
 const preferredCluster=clusters.find(c=>c.metricsAvailable&&c.status!=='unreachable')
 const clusterID=selected||preferredCluster?.id||clusters[0]?.id||''
 const cluster=clusters.find(c=>c.id===clusterID)
 const effectiveView=views.some(v=>v[0]===view)?view:'overview'
 const effectiveHours=['1','6','24','168'].includes(hours)?Number(hours):1
 // Synchronize resource loading state with the external cluster request.
 // eslint-disable-next-line react/set-state-in-effect
 useEffect(()=>{if(!clusterID||effectiveView!=='pods')return;const controller=new AbortController();setResourceError('');void fetch(`/api/v1/clusters/${encodeURIComponent(clusterID)}/pods`,{signal:controller.signal}).then(async r=>{const body=await r.json();if(!r.ok||body.error)throw new Error();if(!controller.signal.aborted)setPods(body.data||[])}).catch(()=>{if(!controller.signal.aborted){setPods([]);setResourceError('无法读取 Pod 列表，可手动填写命名空间和 Pod。')}});return()=>controller.abort()},[clusterID,effectiveView])
 // Synchronize loading/error state with a new monitoring request; abort prevents stale results.
 // eslint-disable-next-line react/set-state-in-effect
 useEffect(()=>{if(!clusterID)return;const controller=new AbortController();requestRef.current=controller;const scope=`${clusterID}/${effectiveView}/${namespace}/${pod}`;if(scopeRef.current!==scope){setData(null);scopeRef.current=scope};setError('');setErrorCode('');setLoading(true)
  const rangeError=hours==='custom'&&absolute?validateTimeRange(absolute):''
  if(rangeError){setData(null);setError(rangeError);setLoading(false);return()=>controller.abort()}
  const end=hours==='custom'&&absolute?Date.parse(absolute.end)/1000:Math.floor(Date.now()/30000)*30,start=hours==='custom'&&absolute?Date.parse(absolute.start)/1000:end-effectiveHours*3600
  const params=new URLSearchParams({view:effectiveView,start:new Date(start*1000).toISOString(),end:new Date(end*1000).toISOString()})
  if(effectiveView==='pods'){if(namespace)params.set('namespace',namespace);if(pod)params.set('pod',pod)}
  const locationParams=new URLSearchParams({cluster:clusterID,view:effectiveView,hours:hours==='custom'?'custom':String(effectiveHours)});if(hours==='custom'&&absolute){locationParams.set('from',absolute.start);locationParams.set('to',absolute.end)};if(namespace&&effectiveView==='pods')locationParams.set('namespace',namespace);if(pod&&effectiveView==='pods')locationParams.set('pod',pod);history.replaceState({},'',`/monitoring?${locationParams}`)
  void fetch(`/api/v1/clusters/${encodeURIComponent(clusterID)}/monitoring?${params}`,{signal:controller.signal}).then(async r=>{const body=await r.json();if(!r.ok||body.error)throw Object.assign(new Error(body.error?.message||'监控查询失败'),{code:body.error?.code||''});if(!controller.signal.aborted)setData(body.data)}).catch(cause=>{if(!controller.signal.aborted){setData(null);setError((cause as Error).message);setErrorCode((cause as Error&{code?:string}).code||'')}}).finally(()=>{if(!controller.signal.aborted)setLoading(false)})
  return()=>controller.abort()
 },[clusterID,effectiveView,effectiveHours,hours,absolute,namespace,pod,refresh])
 useEffect(()=>{if(auto==='0'||hours==='custom')return;const timer=window.setInterval(()=>{if(!document.hidden&&!loading)setRefresh(n=>n+1)},Number(auto)*1000);return()=>clearInterval(timer)},[auto,loading,hours])
 const content=<div ref={root} className={`ks-monitoring${fullscreen?' is-fullscreen':''}`} role={fullscreen?'dialog':undefined} aria-modal={fullscreen||undefined} aria-label={fullscreen?'监控看板全屏':undefined}><div className="ks-monitor-controls"><label>集群<select aria-label="监控集群" value={clusterID} onChange={e=>{setSelected(e.target.value);setNamespace('');setPod('');setPods([])}}>{clusters.map(c=><option key={c.id} value={c.id}>{c.name} · {c.environment||'未标记'}</option>)}</select></label><label>时间范围<select aria-label="监控时间范围" value={hours==='custom'?'custom':String(effectiveHours)} onChange={e=>{setHours(e.target.value);if(e.target.value==='custom'){setAuto('0');setAbsolute(current=>current||{start:data?.start||new Date(Date.now()-3600000).toISOString(),end:data?.end||new Date().toISOString()})}}}><option value="1">最近 1 小时</option><option value="6">最近 6 小时</option><option value="24">最近 24 小时</option><option value="168">最近 7 天</option><option value="custom">自定义时间</option></select></label><label>自动刷新<select aria-label="监控自动刷新" disabled={hours==='custom'} value={auto} onChange={e=>setAuto(e.target.value)}><option value="0">关闭</option><option value="30">30 秒</option><option value="60">1 分钟</option></select></label><Button variant="secondary" isDisabled={!clusterID||loading} isLoading={loading} onClick={()=>setRefresh(n=>n+1)}>刷新曲线</Button>{loading&&<Button variant="link" onClick={()=>{requestRef.current?.abort();setLoading(false);setError('查询已取消，可重新刷新。')}}>取消查询</Button>}{cluster&&<Button variant="link" onClick={()=>onOpenCluster(clusterID)}>集群详情</Button>}<Button variant="secondary" onClick={()=>setFullscreen(value=>!value)}>{fullscreen?'退出看板全屏':'看板全屏'}</Button></div>
 {hours==='custom'&&absolute&&<TimeRangeForm key={`${absolute.start}/${absolute.end}`} range={absolute} onApply={range=>{setAbsolute(range);setAuto('0')}}/>}
 {!clusters.length?<div className="ks-monitor-empty">暂无集群，请先纳管集群。</div>:<><div className="ks-monitor-context"><span>{cluster?.name||clusterID}</span><span>节点 Ready {cluster?.nodes?`${cluster.nodes.ready} / ${cluster.nodes.total}`:'未获取'}</span><span>集群连接：{cluster?.status==='unreachable'?'异常':'可达'}</span><span>指标查询独立判断，集群可达不代表采集完整</span></div><div role="tablist" aria-label="监控视图" className="ks-monitor-tabs">{views.map(([key,label])=><button key={key} role="tab" aria-selected={effectiveView===key} onClick={()=>setView(key)}>{label}</button>)}</div>
 {effectiveView==='pods'&&<PodFilters key={`${clusterID}/${namespace}/${pod}`} namespace={namespace} pod={pod} pods={pods} onApply={(ns,name)=>{setNamespace(ns);setPod(name)}}/>}
 {resourceError&&effectiveView==='pods'&&<Alert isInline variant="warning" title={resourceError}/>}{error&&<Alert isInline variant={errorCode==='MONITORING_NOT_CONFIGURED'?'info':'danger'} title={`${cluster?.name||clusterID}：${error}`}/>} {loading&&<div role="status" className="ks-monitor-loading">正在查询历史指标…</div>}
 {data&&<><div className="ks-monitor-source"><span>数据源：{data.source}</span><span>{data.panels.filter(p=>p.status==='available').length} / {data.panels.length} 个面板有样本</span><span>采样间隔 {data.step} 秒 · 查询于 {new Date(data.collected_at).toLocaleTimeString('zh-CN',{hour12:false})}</span></div>{data.panels.some(p=>p.status!=='available')&&<Alert isInline variant="info" title="部分面板缺少数据或查询失败">缺少样本不代表健康。请检查采集目标、指标、资源筛选及历史保留期；磁盘 I/O 需 cAdvisor 对应指标，etcd 需自身指标。</Alert>}<div className={`ks-monitor-charts${loading?' is-refreshing':''}`} aria-busy={loading}>{data.panels.map(panel=><MetricChart key={`${clusterID}/${effectiveView}/${panel.id}`} panel={panel} start={Date.parse(data.start)/1000} end={Date.parse(data.end)/1000} step={data.step} onZoom={loading?undefined:applyRange}/>)}</div><p className="ks-monitor-footnote">曲线表示历史采样。零值、无样本和查询失败分别展示，不据单个指标自动判定根因。</p></>}
 </>}
 </div>
 return fullscreen?createPortal(content,document.body):content
}
function PodFilters({namespace,pod,pods,onApply}:{namespace:string;pod:string;pods:{namespace:string;name:string}[];onApply:(namespace:string,pod:string)=>void}){
 const [ns,setNs]=useState(namespace),[name,setName]=useState(pod)
 // Remount on target changes to avoid carrying a previous cluster's resource scope.
 return <form className="ks-monitor-pod-filters" onSubmit={e=>{e.preventDefault();onApply(ns.trim(),name.trim())}}><ResourceCombobox label="监控命名空间" value={ns} options={[...new Set(pods.map(p=>p.namespace))].sort()} placeholder="全部命名空间" onChange={value=>{setNs(value);setName('')}}/><ResourceCombobox label="监控 Pod" value={name} options={pods.filter(p=>p.namespace===ns).map(p=>p.name).sort()} placeholder="全部 Pod" disabled={!ns} onChange={setName}/><Button type="submit" variant="secondary">应用筛选</Button><span>支持手动输入已删除 Pod 的名称；历史取决于 Prometheus 保留期。</span>{namespace&&pod&&<Button variant="link" onClick={()=>{history.pushState({},'',`/clusters/${encodeURIComponent(new URLSearchParams(location.search).get('cluster')||'')}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}`);window.dispatchEvent(new PopStateEvent('popstate'))}}>打开 Pod</Button>}</form>
}

function TimeRangeForm({range,onApply}:{range:TimeRange;onApply:(range:TimeRange)=>void}){
 const [start,setStart]=useState(localDateTime(range.start)),[end,setEnd]=useState(localDateTime(range.end)),[error,setError]=useState('')
 return <form className="ks-monitor-time-range" onSubmit={event=>{event.preventDefault();const next={start,end};const message=validateTimeRange(next);setError(message);if(!message)onApply({start:new Date(start).toISOString(),end:new Date(end).toISOString()})}}><label>开始时间<input aria-label="监控开始时间" type="datetime-local" step="1" required value={start} onChange={event=>setStart(event.target.value)}/></label><label>结束时间<input aria-label="监控结束时间" type="datetime-local" step="1" required value={end} onChange={event=>setEnd(event.target.value)}/></label><Button type="submit" variant="primary">按时间查询</Button><span>本地时间 · 最少 30 秒，最多 7 天 · 固定时间范围暂停自动刷新</span>{error&&<p role="alert">{error}</p>}</form>
}
