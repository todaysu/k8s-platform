import {useEffect,useMemo,useRef,useState} from 'react'
import {Alert,Button} from '@patternfly/react-core'
import type {ManagedCluster} from '../api/clusters'
import {relatedRequests,resourceKey,type AuditImport,type AuditResource,type RelationKind} from '../tools/audit'
import './OperationsToolsPage.css'
import {auditSample} from '../tools/auditSample'
import {operationsTools} from '../tools/catalog'
import AuditExportDialog from '../components/AuditExportDialog'
import JvmAnalysisPage from './JvmAnalysisPage'
const writes=new Set(['create','update','patch','delete','deletecollection'])
const verbs:Record<string,string>={create:'创建',update:'更新',patch:'修改',delete:'删除',deletecollection:'批量删除',get:'读取',list:'列举',watch:'监听'}
// 关联类型 → 中文标签与视觉层级。inferred 表示 ownerReferences 缺失、按命名规则推断。
const relationLabels:Record<RelationKind,string>={self:'当前资源',child:'下级 · 创建链',parent:'上级 · 触发方','inferred-child':'下级 · 按命名推断','inferred-parent':'上级 · 按命名推断'}
const showTime=(time:string)=>time?new Date(time).toLocaleString('zh-CN',{hour12:false}):'—'
export default function OperationsToolsPage({clusters}:{clusters:Pick<ManagedCluster,'id'|'name'>[]}){
 const [activePath,setActivePath]=useState(location.pathname)
 const [data,setData]=useState<AuditImport|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false),[filenames,setFilenames]=useState<string[]>([]),[exportOpen,setExportOpen]=useState(false)
 const [cluster,setCluster]=useState(''),[namespace,setNamespace]=useState(''),[query,setQuery]=useState(''),[selected,setSelected]=useState(''),[children,setChildren]=useState(true),[requestSourceIp,setRequestSourceIp]=useState(''),[requestUser,setRequestUser]=useState(''),[requestVerb,setRequestVerb]=useState(''),[requestResult,setRequestResult]=useState(''),[page,setPage]=useState(1)
 const worker=useRef<Worker|null>(null), generation=useRef(0)
 // Worker and generation refs intentionally use their latest values during teardown.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 useEffect(()=>{const change=()=>setActivePath(location.pathname);window.addEventListener('popstate',change);return()=>{window.removeEventListener('popstate',change);generation.current++;worker.current?.terminate()}},[])
 const navigate=(path:string)=>{history.pushState({},'',path);setActivePath(path)}
 const clearRequestFilters=()=>{setRequestSourceIp('');setRequestUser('');setRequestVerb('');setRequestResult('');setSelected('');setPage(1)}
 const reset=()=>{generation.current++;worker.current?.terminate();worker.current=null;setBusy(false);setData(null);setFilenames([]);setSelected('');setError('');clearRequestFilters()}
 const load=async(files:File[])=>{
  reset();if(!files.length)return;if(files.length>5){setError('一次最多导入 5 个文件。');return};const oversized=files.find(file=>file.size>20*1024*1024);if(oversized){setError(`${oversized.name} 超过 20 MiB，请先按条件过滤或拆分。`);return}
  const ticket=generation.current;setBusy(true);setFilenames(files.map(file=>file.name))
  try{const text=(await Promise.all(files.map(file=>file.text()))).join('\n');if(ticket!==generation.current)return
   const next=new Worker(new URL('../tools/audit.worker.ts',import.meta.url),{type:'module'});worker.current=next
   next.onmessage=event=>{if(ticket!==generation.current)return;setBusy(false);next.terminate();worker.current=null;if(event.data.error){setError(event.data.error);return};const parsed=event.data.data as AuditImport;setData(parsed);setNamespace('');setQuery('');clearRequestFilters();if(!parsed.requests.length)setError('未找到有效审计记录。请导入 Kubernetes audit JSON Lines 文件，每行一条审计事件。')}
   next.onerror=()=>{if(ticket!==generation.current)return;setBusy(false);setError('解析失败，请检查文件后重试。');next.terminate();worker.current=null};next.postMessage(text)
  }catch{if(ticket===generation.current){setBusy(false);setError('文件读取失败，请重新选择。')}}
 }
 const allRequests=useMemo(()=>data?.requests||[],[data])
 const requestMatches=(item:AuditImport['requests'][number])=>{
  const verbMatches=requestVerb===''||(requestVerb==='writes'?writes.has(item.verb):item.verb===requestVerb)
  const failed=item.stage==='Panic'||(item.code!==null&&item.code>=400)
  const resultMatches=requestResult===''||(requestResult==='failed'?failed:requestResult==='success'?item.code!==null&&!failed:item.code===null&&!failed)
  return verbMatches&&resultMatches&&(!requestSourceIp||item.sourceIPs.includes(requestSourceIp))&&(!requestUser||item.user===requestUser)
 }
 const filteredPool=allRequests.filter(requestMatches)
 const resources=useMemo(()=>{const map=new Map<string,AuditResource>();for(const item of allRequests){if(item.resource.name)map.set(resourceKey(item.resource),item.resource)}return [...map.values()]},[allRequests])
 const filteredResourceKeys=new Set(filteredPool.map(item=>resourceKey(item.resource)))
 const candidates=resources.filter(r=>filteredResourceKeys.has(resourceKey(r))&&(!namespace||(namespace==='__cluster__'?!r.namespace:r.namespace===namespace))&&(!query||`${r.resource} ${r.name} ${r.uid}`.toLowerCase().includes(query.toLowerCase())))
 const target=resources.find(r=>resourceKey(r)===selected)
 const related=useMemo(()=>target&&data?relatedRequests(data.requests,target,children):[],[data,target,children])
 const requests=useMemo(()=>related.map(entry=>entry.item),[related])
 const inferredCount=related.filter(entry=>entry.kind.startsWith('inferred')).length
 const sourceIpOptions=useMemo(()=>[...new Set(allRequests.flatMap(item=>item.sourceIPs))].sort(),[allRequests])
 const userOptions=useMemo(()=>[...new Set(allRequests.map(item=>item.user).filter(Boolean))].sort(),[allRequests])
 const verbOptions=useMemo(()=>[...new Set(allRequests.map(item=>item.verb).filter(Boolean))].sort(),[allRequests])
 const filtered=related.filter(({item})=>requestMatches(item))
 const hasRequestFilters=Boolean(requestSourceIp||requestUser||requestVerb||requestResult)
 const changeRequestFilter=(change:()=>void)=>{change();setSelected('');setPage(1)}
 const downloadSample=()=>{const url=URL.createObjectURL(new Blob([auditSample+'\n'],{type:'application/x-ndjson'}));const anchor=document.createElement('a');anchor.href=url;anchor.download='kubernetes-audit-demo-2026-10-19.jsonl';anchor.click();URL.revokeObjectURL(url)}
 if(activePath==='/tools/jvm')return <JvmAnalysisPage onBack={()=>navigate('/tools')}/>
 if(activePath!=='/tools/audit')return <div className="ks-tools"><p className="ks-tools-lead">选择工具，围绕集群或资源开展排障。</p><div className="ks-tools-catalog">{operationsTools.map(tool=><article key={tool.id}><span className="ks-tool-symbol" aria-hidden="true">{tool.symbol}</span><div><h2>{tool.title}</h2><p>{tool.description}</p><span className="ks-tool-tag">{tool.tag}</span></div><Button onClick={()=>navigate(tool.path)}>打开工具</Button></article>)}</div></div>
 return <div className="ks-tools"><div className="ks-tool-heading"><div><Button variant="link" isInline onClick={()=>navigate('/tools')}>运维工具</Button><h2>审计日志分析</h2><p>追踪一个资源从创建到变更的操作过程。</p></div><span className="ks-tool-tag">日志仅在当前浏览器处理</span></div>
 <section className="ks-tool-panel"><div className="ks-tool-import"><div><h3>导入审计日志</h3><p>Kubernetes audit JSON Lines，一次可选 1–5 个同集群文件，每个最大 20 MiB。关闭页面后清除分析数据。</p></div><div className="ks-tool-import-buttons"><Button variant="link" isDisabled={busy} onClick={()=>void load([new File([auditSample],'示例日志.jsonl',{type:'application/json'})])}>查看示例</Button><Button variant="link" isDisabled={busy} onClick={downloadSample}>下载示例文件</Button><Button variant="secondary" isDisabled={busy} onClick={()=>setExportOpen(true)}>生成导出命令</Button><label className="ks-tool-upload">{busy?'正在解析…':'选择日志文件'}<input aria-label="导入审计日志" multiple type="file" accept=".log,.json,.jsonl,.ndjson,text/plain,application/json" onChange={event=>{const files=Array.from(event.target.files||[]);if(files.length)void load(files);event.target.value=''}}/></label></div></div>
 <div className="ks-tool-import-meta"><label>来源集群<select aria-label="日志来源集群" value={cluster} onChange={e=>setCluster(e.target.value)}><option value="">未绑定（离线分析）</option>{clusters.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></label><span>本次所有文件必须来自同一集群；重叠 auditID 会自动合并。</span>{filenames.length>0&&<><span className="ks-tool-filename" title={filenames.join('、')}>{filenames.length===1?filenames[0]:`${filenames.length} 个文件：${filenames.join('、')}`}</span><Button variant="link" onClick={reset}>清除数据</Button></>}</div></section>
 <AuditExportDialog open={exportOpen} onClose={()=>setExportOpen(false)}/>
 {filenames.includes('示例日志.jsonl')&&<Alert isInline variant="info" title="当前为虚构示例数据，不代表实际集群"/>}
 {error&&<Alert isInline variant="danger" title={error}/>}
{data&&data.requests.length>0&&data.requests.every(r=>r.level==='Metadata')&&<div className="ks-tool-notice ks-tool-notice-warning"><b>这份日志没有可用的 ownerReferences，已启用名称回退关联。</b><br/>平台会在同一 Namespace 内按 Deployment → ReplicaSet → Pod 的 Kubernetes 命名链进行类 grep 匹配，结果会标记为“按命名推断”。如需精确证据，请在审计策略中为相关资源保留 RequestResponse 级别。</div>}
{data&&data.requests.length>0&&<><div className="ks-tool-stats"><div><strong>{data.requests.length}</strong><span>合并后的 API 请求</span></div><div><strong>{resources.length}</strong><span>资源身份</span></div><div><strong>{data.requests.filter(r=>r.code!==null&&r.code>=400).length}</strong><span>失败请求</span></div><div><strong>{data.invalid+data.ignored}</strong><span>异常 / 非审计行</span></div></div>
     <div className="ks-tool-coverage">日志覆盖：{showTime(data.start)} — {showTime(data.end)} · 有效事件 {data.valid} 条{data.invalid>0&&` · JSON 解析失败 ${data.invalid} 行（行号 ${data.errors.join('、')}）`}{data.ignored>0&&` · 忽略非审计记录 ${data.ignored} 行`}</div>
 <section className="ks-tool-panel"><div className="ks-audit-request-filters is-global" aria-label="导入后请求过滤"><div className="ks-audit-filter-heading"><strong>先按请求过滤</strong><span>从全部导入日志中反查某个来源 IP 或操作人做了什么</span>{hasRequestFilters&&<Button variant="link" isInline onClick={clearRequestFilters}>清除请求过滤</Button>}</div><div className="ks-audit-filter-fields"><label>来源 IP<select aria-label="按来源 IP 筛选" value={requestSourceIp} onChange={e=>changeRequestFilter(()=>setRequestSourceIp(e.target.value))}><option value="">全部来源 IP（{sourceIpOptions.length}）</option>{sourceIpOptions.map(ip=><option key={ip} value={ip}>{ip}</option>)}</select></label><label>操作人<select aria-label="按操作人筛选" value={requestUser} onChange={e=>changeRequestFilter(()=>setRequestUser(e.target.value))}><option value="">全部操作人（{userOptions.length}）</option>{userOptions.map(user=><option key={user} value={user}>{user}</option>)}</select></label><label>动作<select aria-label="按动作筛选" value={requestVerb} onChange={e=>changeRequestFilter(()=>setRequestVerb(e.target.value))}><option value="">全部动作（默认）</option><option value="writes">仅变更操作</option>{verbOptions.map(verb=><option key={verb} value={verb}>{verbs[verb]||verb}（{verb}）</option>)}</select></label><label>结果<select aria-label="按结果筛选" value={requestResult} onChange={e=>changeRequestFilter(()=>setRequestResult(e.target.value))}><option value="">全部结果</option><option value="success">成功</option><option value="failed">失败</option><option value="unknown">结果未记录</option></select></label></div><div className="ks-audit-filter-count">匹配 <strong>{filteredPool.length}</strong> / {allRequests.length} 条请求，涉及 <strong>{filteredResourceKeys.size}</strong> 个资源</div></div><div className="ks-tool-filters"><label>命名空间<select aria-label="筛选命名空间" value={namespace} onChange={e=>{setNamespace(e.target.value);setSelected('');setPage(1)}}><option value="">全部命名空间</option>{[...new Set(resources.map(r=>r.namespace))].sort().map(ns=><option key={ns} value={ns||'__cluster__'}>{ns||'集群级资源'}</option>)}</select></label><label>搜索匹配资源<input aria-label="搜索审计资源" placeholder="名称、资源类型或 UID" value={query} onChange={e=>{setQuery(e.target.value);setSelected('');setPage(1)}}/></label><label className="ks-tool-resource-select">下钻资源<select aria-label="选择分析资源" value={selected} onChange={e=>{setSelected(e.target.value);setPage(1)}}><option value="">查看全部匹配请求（{candidates.length} 个资源）</option>{candidates.map(r=><option key={resourceKey(r)} value={resourceKey(r)}>{r.namespace||'集群级'} / {r.resource} / {r.name} · {r.uid?r.uid.slice(0,12):'UID 未记录'}</option>)}</select></label></div>
     {!target?<div className="ks-audit-global-results"><div className="ks-audit-global-heading"><div><h3>匹配请求</h3><p>点击资源可继续查看它的 Deployment、ReplicaSet、Pod 关联链。</p></div><span>显示前 {Math.min(25,filteredPool.length)} 条</span></div>{filteredPool.length?<div className="ks-monitor-table-wrap"><table className="ks-monitor-table"><thead><tr><th>时间</th><th>来源 IP</th><th>操作人</th><th>动作</th><th>资源</th><th>结果</th></tr></thead><tbody>{filteredPool.slice(0,25).map(item=><tr key={item.id}><td>{showTime(item.time)}</td><td>{item.sourceIPs.join('、')||'未记录'}</td><td>{item.user}</td><td>{verbs[item.verb]||item.verb}</td><td><Button variant="link" isInline onClick={()=>{setSelected(resourceKey(item.resource));setPage(1)}}>{item.resource.namespace?`${item.resource.namespace} / `:''}{item.resource.resource}/{item.resource.name}</Button></td><td><span className={`ks-audit-result ${item.code===null?'is-unknown':item.code>=400?'is-failed':''}`}>{item.code===null?'结果未记录':item.code>=400?`失败 ${item.code}`:`API ${item.code}`}</span></td></tr>)}</tbody></table></div>:<div className="ks-tool-empty">没有匹配的请求，请调整上方过滤条件。</div>}</div>:<><div className="ks-tool-resource-heading"><h3>{target.resource} / {target.name}</h3><span>{target.namespace||'集群级资源'} · UID：{target.uid||'日志未记录，无法确认是否为同名重建资源'}</span></div><p className="ks-audit-summary">{requests.filter(r=>writes.has(r.verb)).length} 次变更请求 · {requests.filter(r=>r.code!==null&&r.code>=400).length} 次失败 · {requests.filter(r=>r.code===null).length} 次结果未记录</p><div className="ks-tool-toggles"><label><input type="checkbox" checked={children} onChange={e=>{setChildren(e.target.checked);setPage(1)}}/>包含关联子资源</label><span>{filtered.length} / {related.length} 条请求</span></div>
 <div className="ks-tool-notice">API 成功表示请求处理成功，不代表 Pod 已就绪。{requests.some(r=>r.level==='Metadata')?' 部分记录仅包含 Metadata，无法还原完整配置变化。':''}{target.uid?' 子资源优先按 ownerReferences UID 关联。':' 当前资源缺少 UID，无法做 UID 精确核对，但仍会在同 Namespace 内进行名称回退关联。'}{inferredCount?` ${inferredCount} 条操作按「合法资源层级 + 最长名称前缀」推断，请人工核对。`:''}</div>
 <ol className="ks-audit-timeline">{filtered.slice((page-1)*25,page*25).map(({item,kind})=>{
  const isInferred=kind.startsWith('inferred')
  const otherKey=resourceKey(item.resource)
  const isOther=otherKey!==resourceKey(target)
  return <li key={item.id} className={`${item.code!==null&&item.code>=400?'is-failed ':''}${kind.startsWith('child')?'is-child ':''}${kind==='parent'||kind==='inferred-parent'?'is-parent ':''}${isInferred?'is-inferred':''}`}>
    <div className="ks-audit-time">{showTime(item.time)}</div>
    <div className="ks-audit-event">
      <div className="ks-audit-event-title">
        {kind!=='self'&&<span className={`ks-audit-relation ${isInferred?'is-inferred':''}`}>{relationLabels[kind]}</span>}
        <strong className={isOther?'ks-audit-linked':''} onClick={isOther?()=>{setSelected(otherKey);setPage(1)}:undefined}
          title={isOther?'点击查看该资源的完整链路':undefined}>{verbs[item.verb]||item.verb} {item.resource.resource}/{item.resource.name}</strong>
        <span className={`ks-audit-result ${item.code===null?'is-unknown':item.code>=400?'is-failed':''}`}>{item.code===null?'结果未记录':item.code>=400?`失败 ${item.code}`:`API ${item.code}`}</span>
      </div>
      <p>{item.user} · 来源 IP：{item.sourceIPs.length?item.sourceIPs.join('、'):'未记录'}{item.reason?` · ${item.reason}`:''}</p>
      {(item.images.length>0||item.replicas!==null)&&<p className="ks-audit-config">{item.images.length>0&&`记录镜像：${item.images.join('，')}`}{item.replicas!==null&&` · 记录副本：${item.replicas}`}</p>}
      <details><summary>查看证据 · 合并 {item.stages.length} 个阶段</summary><p>审计级别 {item.level||'未记录'} · {item.stages.join(' → ')} · auditID {item.id}</p><pre>{JSON.stringify(item.evidence,null,2)}</pre><p>仅展示安全字段投影，已省略请求正文、Secret 数据、环境变量等内容。</p></details>
    </div>
  </li>})}</ol>
 {!filtered.length&&<div className="ks-tool-empty">当前条件下没有请求，请调整过滤条件。</div>}<div className="ks-tool-paging"><Button variant="secondary" isDisabled={page<=1} onClick={()=>setPage(p=>p-1)}>上一页</Button><span>{page} / {Math.max(1,Math.ceil(filtered.length/25))}</span><Button variant="secondary" isDisabled={page*25>=filtered.length} onClick={()=>setPage(p=>p+1)}>下一页</Button></div></>}
 </section></>}
 {!data&&!busy&&<div className="ks-tool-empty">导入日志后，可按资源查看创建、修改、删除及失败请求。</div>}
 </div>
}
