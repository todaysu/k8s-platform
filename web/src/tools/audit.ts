export type AuditResource = { resource: string; namespace: string; name: string; uid: string }
export type AuditRequest = { id:string; time:string; end:string; verb:string; user:string; sourceIPs:string[]; stage:string; stages:string[]; code:number|null; reason:string; level:string; resource:AuditResource; owners:string[]; images:string[]; replicas:number|null; evidence:Record<string,unknown> }
export type AuditImport = { requests:AuditRequest[]; valid:number; invalid:number; ignored:number; errors:number[]; start:string; end:string }
type Obj=Record<string,unknown>
const object=(value:unknown):Obj=>value&&typeof value==='object'&&!Array.isArray(value)?value as Obj:{}
const str=(value:unknown)=>typeof value==='string'?value:''
const date=(value:unknown)=>{const text=str(value);return text&&Number.isFinite(Date.parse(text))?new Date(text).toISOString():''}
const list=(value:unknown)=>Array.isArray(value)?value:[]
const ranks:Record<string,number>={RequestReceived:0,ResponseStarted:1,ResponseComplete:2,Panic:3}
function normalize(value:unknown,line:number):AuditRequest|null {
 const event=object(value), ref=object(event.objectRef), response=object(event.responseStatus)
 if(!str(event.verb)||!str(event.auditID)||!str(event.stage))return null
 const body=object(event.responseObject), request=object(event.requestObject)
 const meta=object(body.metadata), requestMeta=object(request.metadata)
 const bodySpec=object(body.spec), requestSpec=object(request.spec)
 const spec=Object.keys(bodySpec).length?bodySpec:requestSpec
 const template=object(spec.template), podSpec=Object.keys(template).length?object(template.spec):spec
 const secret=ref.resource==='secrets'
 const resource={resource:str(ref.resource),namespace:str(ref.namespace),name:str(ref.name)||str(meta.name)||str(requestMeta.name),uid:str(ref.uid)||str(meta.uid)||str(requestMeta.uid)}
 const owners=secret?[]:list(meta.ownerReferences??requestMeta.ownerReferences).map(owner=>str(object(owner).uid)).filter(Boolean)
 const images=secret?[]:list(podSpec.containers).map(container=>str(object(container).image)).filter(Boolean)
 const code=typeof response.code==='number'?response.code:null
 const time=date(event.requestReceivedTimestamp)||date(event.stageTimestamp)
 if(!time)return null
 const sourceIPs=Array.from(new Set(list(event.sourceIPs).map(str).filter(Boolean)))
 const reason=secret?'':str(response.reason)
 const replicas=typeof spec.replicas==='number'?spec.replicas:null
 // Strict projection: request bodies, tokens, Secrets, URL queries, annotations and env never enter UI state.
 const evidence={auditID:str(event.auditID),level:str(event.level),stage:str(event.stage),verb:str(event.verb),requestReceivedTimestamp:time,stageTimestamp:date(event.stageTimestamp),user:{username:str(object(event.user).username)},sourceIPs,objectRef:resource,responseStatus:{code,reason},configuration:secret?'Secret 内容已隐藏':{images,replicas,ownerUIDs:owners},sourceLine:line}
 return {id:str(event.auditID),time,end:date(event.stageTimestamp)||time,verb:str(event.verb),user:str(object(event.user).username)||'未知',sourceIPs,stage:str(event.stage),stages:[str(event.stage)],code,reason,level:str(event.level),resource,owners,images,replicas:secret?null:replicas,evidence}
}
export function parseAudit(text:string):AuditImport {
 const requests=new Map<string,AuditRequest>();let valid=0,invalid=0,ignored=0;const errors:number[]=[]
 const lines=text.replace(/^\uFEFF/,'').split(/\r?\n/)
 for(let i=0;i<lines.length;i++){
  if(!lines[i].trim())continue
  let value:unknown
  try{value=JSON.parse(lines[i])}catch{invalid++;if(errors.length<10)errors.push(i+1);continue}
  const current=normalize(value,i+1)
  if(!current){ignored++;continue};valid++
  const previous=requests.get(current.id)
  if(!previous){requests.set(current.id,current);continue}
  const chosen=(ranks[current.stage]??-1)>=(ranks[previous.stage]??-1)?current:previous
  const other=chosen===current?previous:current
  requests.set(current.id,{...chosen,time:previous.time<current.time?previous.time:current.time,end:previous.end>current.end?previous.end:current.end,stages:Array.from(new Set([...previous.stages,...current.stages])).sort((a,b)=>(ranks[a]??-1)-(ranks[b]??-1)),sourceIPs:Array.from(new Set([...previous.sourceIPs,...current.sourceIPs])),resource:{...chosen.resource,uid:chosen.resource.uid||other.resource.uid},images:chosen.images.length?chosen.images:other.images,replicas:chosen.replicas??other.replicas,owners:chosen.owners.length?chosen.owners:other.owners})
 }
 const items=[...requests.values()].sort((a,b)=>a.time.localeCompare(b.time))
 return {requests:items,valid,invalid,ignored,errors,start:items[0]?.time||'',end:items.reduce((end,item)=>item.end>end?item.end:end,'')}
}
export const resourceKey=(r:AuditResource)=>JSON.stringify([r.resource,r.namespace,r.name,r.uid])

export type RelationKind='self'|'child'|'parent'|'inferred-child'|'inferred-parent'

export type RelatedRequest={item:AuditRequest;kind:RelationKind}

// K8s 的子资源命名规则：<父名>-<hash>，用于 ownerReferences 缺失时补全归属关系。
// 典型场景：Metadata 级别日志不记录请求体，ownerReferences 恒为空，
// 只能依赖 objectRef.name 的命名链串联 Deployment → ReplicaSet → Pod。
const namePrefix=(value:string)=>{const text=str(value);const cut=text.lastIndexOf('-');return cut>0?text.slice(0,cut):text}

// 归属标识。ownerReferences 里记录的是 UID，而 objectRef.uid 未必每条都有
//（Metadata 级别下 create pods 常见 uid 为空），因此以「资源类型 + namespace + 名称」为统一主键，
//UID 仅在两侧都存在时用于人工核对。
const identity=(r:AuditResource)=>`${r.resource}/${r.namespace}/${r.name}`

// 命名规则兜底：仅在父子资源类型匹配、同一 namespace、该子资源未记录 ownerReferences 时认定。
const CHILD_PARENT:Record<string,string>={pods:'replicasets',replicasets:'deployments',jobs:'cronjobs'}
function inferredEdges(requests:AuditRequest[]){
 // 索引键是「对象自身名」。子资源查找时逐级剥离后缀去撞这些键：
 //   myapp-web-1-6c449fffb8 → myapp-web-1 → 命中 Deployment myapp-web-1
 const byName=new Map<string,AuditRequest[]>()
 const add=(key:string,item:AuditRequest)=>{
  if(!key)return
  const list=byName.get(key)
  if(list)list.push(item);else byName.set(key,[item])
 }
 for(const item of requests){
  if(!item.resource.name)continue
  add(item.resource.name,item)
  // Deployment 名本身可能含连字符（如 myapp-web-1），额外把剥一层的前缀也登记为候选，
  // 避免 myapp-web-1 被误认为 myapp-web 的子资源。
  add(namePrefix(item.resource.name),item)
 }
 const inferred=new Map<string,Set<string>>()
 const knownUids=new Set(requests.map(item=>item.resource.uid).filter(Boolean))
 const push=(from:string,to:string)=>{if(!inferred.has(from))inferred.set(from,new Set());inferred.get(from)!.add(to)}
 for(const item of requests){
  const expectedParent=CHILD_PARENT[item.resource.resource]
  if(!expectedParent||!item.resource.namespace||!item.resource.name)continue
  // 只有 owner UID 能在本批日志中解析到父资源时才停止回退。
  // 多文件不完整时，ownerReferences 可能存在，但对应父资源记录不在本批数据中。
  if(item.owners.some(uid=>knownUids.has(uid)))continue
  // 逐级剥离后缀去撞父资源名：
  //   myapp-web-1-6c449fffb8 → myapp-web-1 → 命中 Deployment myapp-web-1
  let prefix=namePrefix(item.resource.name)
  const childId=identity(item.resource)
  let linked=false
  while(prefix){
   const parent=(byName.get(prefix)||[]).find(entry=>
    entry.resource.resource===expectedParent
    && entry.resource.namespace===item.resource.namespace
    && identity(entry.resource)!==childId)
   if(parent){push(identity(parent.resource),childId);linked=true;break}
   const next=namePrefix(prefix)
   if(next===prefix)break
   prefix=next
  }
  // 导入的时间窗口可能没有 ReplicaSet 记录，但仍有 Deployment 和 Pod。
  // 此时允许 Pod 直接回退到最长 Deployment 名称前缀。
  if(!linked&&item.resource.resource==='pods'){
   prefix=namePrefix(item.resource.name)
   while(prefix){
    const parent=(byName.get(prefix)||[]).find(entry=>entry.resource.resource==='deployments'&&entry.resource.namespace===item.resource.namespace)
    if(parent){push(identity(parent.resource),childId);break}
    const next=namePrefix(prefix);if(next===prefix)break;prefix=next
   }
  }
 }
 return inferred
}

export function relatedRequests(requests:AuditRequest[],target:AuditResource,includeChildren:boolean){
 const key=resourceKey(target)
 const targetId=identity(target)
 // ownerReferences 记录的是 UID，而对象身份用名称，两者需通过 uidById 互相翻译。
 const idByUid=new Map<string,string>()
 for(const item of requests){
  if(item.resource.uid&&!idByUid.has(item.resource.uid))idByUid.set(item.resource.uid,identity(item.resource))
 }
 const resolve=(uid:string)=>idByUid.get(uid)??`uid:${uid}`
 // 身份 → 上级身份集合
 const ownersOf=new Map<string,Set<string>>()
 for(const item of requests){
  const child=identity(item.resource)
  for(const owner of item.owners){
   if(item.resource.uid&&owner===item.resource.uid)continue
   if(!ownersOf.has(child))ownersOf.set(child,new Set())
   ownersOf.get(child)!.add(resolve(owner))
  }
 }
 const inferred=includeChildren?inferredEdges(requests):new Map<string,Set<string>>()

 // 单独计算只由 ownerReferences 证明的上下游，用于区分“精确关联”与“名称推断”。
 const declaredAncestors=new Set<string>()
 if(includeChildren){let changed=true;let frontier=new Set([targetId]);while(changed){changed=false;const next=new Set(frontier);for(const id of frontier)for(const owner of ownersOf.get(id)||[])if(!declaredAncestors.has(owner)){declaredAncestors.add(owner);next.add(owner);changed=true};frontier=next}}
 const declaredDescendants=new Set<string>()
 if(includeChildren){let changed=true;while(changed){changed=false;for(const [child,owners] of ownersOf)if(!declaredDescendants.has(child)&&[...owners].some(owner=>owner===targetId||declaredDescendants.has(owner))){declaredDescendants.add(child);changed=true}}}

 // 祖先链：自身 + ownerReferences 上游 + 命名规则推导出的上游
 // 命名规则只产出「父 → 子」边，因此把 inferred 反转后并入上游遍历。
 const parentsOf=new Map<string,Set<string>>()
 for(const [parent,kids] of inferred){
  for(const kid of kids){
   if(!parentsOf.has(kid))parentsOf.set(kid,new Set())
   parentsOf.get(kid)!.add(parent)
  }
 }
 const ancestors=new Set<string>([targetId])
 if(includeChildren){
  let changed=true
  while(changed){
   changed=false
   for(const id of [...ancestors]){
    for(const owner of ownersOf.get(id)||[]){
     if(!ancestors.has(owner)){ancestors.add(owner);changed=true}
    }
    for(const parent of parentsOf.get(id)||[]){
     if(!ancestors.has(parent)){ancestors.add(parent);changed=true}
    }
   }
  }
 }

 // 后代链：ownerReferences 下游 + 命名规则下游
 const descendants=new Set<string>()
 if(includeChildren){
  let changed=true
  while(changed){
   changed=false
   for(const [child,owners] of ownersOf){
    if(descendants.has(child))continue
    if([...owners].some(o=>o===targetId||descendants.has(o))){descendants.add(child);changed=true}
   }
   for(const [parent,kids] of inferred){
    if(parent===targetId||descendants.has(parent))for(const kid of kids){if(!descendants.has(kid)){descendants.add(kid);changed=true}}
   }
  }
 }

 const related:RelatedRequest[]=[]
 for(const item of requests){
  const itemKey=resourceKey(item.resource)
  const id=identity(item.resource)
  const sameIdentity=id===targetId&&(!item.resource.uid||!target.uid||item.resource.uid===target.uid)
  if(itemKey===key||sameIdentity){related.push({item,kind:'self'});continue}
  if(!includeChildren)continue
// 命中祖先链即为上级；父边来自 ownerReferences 记为 parent，命名推断记为 inferred-parent
 if(ancestors.has(id)&&id!==targetId){
  related.push({item,kind:declaredAncestors.has(id)?'parent':'inferred-parent'});continue
 }
  if(descendants.has(id)){
   const declared=declaredDescendants.has(id)&&item.owners.length>0
   related.push({item,kind:declared?'child':'inferred-child'});continue
  }
  // 该记录的所有 owner 都属于目标的后代链 → 它是这些子资源的上级
  const owners=[...(ownersOf.get(id)||[])]
  if(owners.length&&owners.every(o=>descendants.has(o))){related.push({item,kind:'parent'});continue}
  if(owners.some(o=>ancestors.has(o)))related.push({item,kind:'parent'})
 }
 return related.sort((a,b)=>a.item.time.localeCompare(b.item.time))
}
