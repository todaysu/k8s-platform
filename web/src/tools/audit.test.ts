import {expect,it} from 'vitest'
import {parseAudit,relatedRequests} from './audit'
const event=(extra:object={})=>({auditID:'a',stage:'ResponseComplete',level:'Metadata',verb:'patch',requestReceivedTimestamp:'2026-10-19T01:00:00Z',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d1'},responseStatus:{code:200},...extra})
it('merges audit stages regardless of file ordering and keeps completion status',()=>{const result=parseAudit([event(),event({stage:'RequestReceived',responseStatus:{},requestReceivedTimestamp:'2026-10-19T00:59:59Z'})].map(e=>JSON.stringify(e)).join('\n'));expect(result.valid).toBe(2);expect(result.requests).toHaveLength(1);expect(result.requests[0].code).toBe(200);expect(result.requests[0].time).toBe('2026-10-19T00:59:59.000Z')})
it('keeps and merges source IPs as safe filter fields',()=>{const result=parseAudit([event({sourceIPs:['192.0.2.10']}),event({stage:'RequestReceived',responseStatus:{},sourceIPs:['10.0.0.8','192.0.2.10']})].map(e=>JSON.stringify(e)).join('\n'));expect(result.requests[0].sourceIPs).toEqual(['192.0.2.10','10.0.0.8']);expect(result.requests[0].evidence).toMatchObject({sourceIPs:['192.0.2.10']})})
it('counts bad lines and ignores non audit records',()=>{const result=parseAudit('\uFEFF'+JSON.stringify(event())+'\n{bad\n{}\n');expect(result.invalid).toBe(1);expect(result.ignored).toBe(1);expect(result.errors).toEqual([2])})
it('links owners by UID without merging same name replacements',()=>{const rows=[event(),event({auditID:'b',objectRef:{resource:'replicasets',namespace:'myapp',name:'web-1',uid:'r1'},responseObject:{metadata:{ownerReferences:[{uid:'d1'}]}}}),event({auditID:'c',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d2'}})];const parsed=parseAudit(rows.map(e=>JSON.stringify(e)).join('\n'));expect(relatedRequests(parsed.requests,parsed.requests[0].resource,true)).toHaveLength(2)})
it('never retains secret body or arbitrary request data',()=>{const secret=JSON.stringify(event({objectRef:{resource:'secrets',name:'credentials'},requestObject:{data:{token:'PRIVATE'},spec:{replicas:3}},responseStatus:{code:403,message:'PRIVATE'}}));expect(JSON.stringify(parseAudit(secret))).not.toContain('PRIVATE')})
it('includes failed child creation without assigning a fabricated UID',()=>{const rows=[event(),event({auditID:'failed-child',verb:'create',objectRef:{resource:'pods',namespace:'myapp',name:'new-pod'},responseStatus:{code:403},requestObject:{metadata:{ownerReferences:[{uid:'d1'}]}}})];const parsed=parseAudit(rows.map(r=>JSON.stringify(r)).join('\n'));const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true);expect(related).toHaveLength(2);expect(related[1].item.resource.uid).toBe('')})

// ── 变更链关联（Deployment ⇄ ReplicaSet ⇄ Pod）────────────────

const deployment=event({auditID:'dep-create',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:00Z',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d1'},responseObject:{spec:{replicas:3,template:{spec:{containers:[{name:'app',image:'web:v1'}]}}}}})
const replicaSet=event({auditID:'rs-create',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:01Z',objectRef:{resource:'replicasets',namespace:'myapp',name:'web-7c89d',uid:'r1'},responseObject:{metadata:{ownerReferences:[{uid:'d1'}]},spec:{replicas:3,template:{spec:{containers:[{name:'app',image:'web:v1'}]}}}}})
const podCreate=event({auditID:'pod-create',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:02Z',objectRef:{resource:'pods',namespace:'myapp',name:'web-7c89d-m8p6s',uid:'p1'},responseObject:{metadata:{ownerReferences:[{uid:'r1'}]}}})

it('pulls the whole create chain into a Deployment view',()=>{
 const parsed=parseAudit([deployment,replicaSet,podCreate].map(r=>JSON.stringify(r)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true)
 expect(related.map(entry=>entry.item.resource.resource)).toEqual(['deployments','replicasets','pods'])
 expect(related.map(entry=>entry.kind)).toEqual(['self','child','child'])
})

it('walks upward so a Pod view also shows the Deployment that triggered it',()=>{
 const parsed=parseAudit([deployment,replicaSet,podCreate].map(r=>JSON.stringify(r)).join('\n'))
 const pod=parsed.requests.find(r=>r.resource.resource==='pods')!.resource
 const related=relatedRequests(parsed.requests,pod,true)
 const kinds=related.map(entry=>`${entry.item.resource.resource}:${entry.kind}`)
 expect(kinds).toContain('pods:self')
 expect(kinds).toContain('replicasets:parent')
 expect(kinds).toContain('deployments:parent')
})

it('includes delete events that carry no ownerReferences by inferring the parent from the name',()=>{
 // 删除 Pod 时 requestObject / responseObject 均为空，ownerReferences 取不到，
 // 只能靠「web-7c89d-m8p6s 的前缀命中 replicasets/web-7c89d」推断归属。
 const podDelete=event({auditID:'pod-delete',verb:'delete',requestReceivedTimestamp:'2026-10-19T02:00:00Z',objectRef:{resource:'pods',namespace:'myapp',name:'web-7c89d-m8p6s',uid:'p1'},requestObject:null,responseObject:null})
 const parsed=parseAudit([deployment,replicaSet,podCreate,podDelete].map(r=>JSON.stringify(r)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true)
 const deleted=related.find(entry=>entry.item.verb==='delete')!
 expect(deleted).toBeDefined()
 expect(deleted.kind).toBe('inferred-child')
 expect(deleted.item.owners).toHaveLength(0)
})

it('marks rows linked by ownerReferences as plain children, not inferred',()=>{
 const podDelete=event({auditID:'pod-delete',verb:'delete',objectRef:{resource:'pods',namespace:'myapp',name:'web-7c89d-m8p6s',uid:'p1'},requestObject:null,responseObject:null})
 const parsed=parseAudit([deployment,replicaSet,podDelete].map(r=>JSON.stringify(r)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true)
 const rs=related.find(entry=>entry.item.resource.resource==='replicasets')!
 expect(rs.kind).toBe('child')
})

it('does not infer parents across namespaces',()=>{
 // myapp 的 Pod 名同样是 web-7c89d-m8p6s，但前�� RS 属于 other 命名空间，
 // 不能因为命名前缀相同就把 other 侧的 Deployment 串进 myapp 的链路。
 const depOther=event({auditID:'dep-other',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:00Z',objectRef:{resource:'deployments',namespace:'other',name:'web',uid:'d2'},responseObject:{metadata:{}}})
 const rsOther=event({auditID:'rs-other',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:01Z',objectRef:{resource:'replicasets',namespace:'other',name:'web-7c89d',uid:'r2'},responseObject:{metadata:{ownerReferences:[{uid:'d2'}]}}})
 const podOther=event({auditID:'pod-other',verb:'create',requestReceivedTimestamp:'2026-10-19T01:00:02Z',objectRef:{resource:'pods',namespace:'other',name:'web-7c89d-m8p6s',uid:'p2'},responseObject:{metadata:{ownerReferences:[{uid:'r2'}]}}})
 const parsed=parseAudit([deployment,depOther,rsOther,podOther].map(r=>JSON.stringify(r)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests.find(r=>r.resource.uid==='d1')!.resource,true)
 expect(related.map(entry=>entry.item.resource.uid)).toEqual(['d1'])
})

it('stops the chain when child resources are switched off',()=>{
 const parsed=parseAudit([deployment,replicaSet,podCreate].map(r=>JSON.stringify(r)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,false)
 expect(related).toHaveLength(1)
 expect(related[0].kind).toBe('self')
})

it('returns every row in chronological order regardless of file ordering',()=>{
 const parsed=parseAudit([podCreate,deployment,replicaSet].map(r=>JSON.stringify(r)).join('\n'))
 const times=relatedRequests(parsed.requests,parsed.requests.find(r=>r.resource.resource==='deployments')!.resource,true).map(entry=>entry.item.time)
 expect([...times].sort()).toEqual(times)
})

it('falls back to the Kubernetes name chain when an entire Metadata log has no owners',()=>{
 const noOwnerDeployment=event({auditID:'metadata-dep',level:'Metadata',objectRef:{resource:'deployments',namespace:'myapp',name:'tcab-order-1001',uid:''},requestObject:null,responseObject:null})
 const noOwnerRs=event({auditID:'metadata-rs',level:'Metadata',objectRef:{resource:'replicasets',namespace:'myapp',name:'tcab-order-1001-6c449fffb8',uid:''},requestObject:null,responseObject:null})
 const noOwnerPod=event({auditID:'metadata-pod',level:'Metadata',objectRef:{resource:'pods',namespace:'myapp',name:'tcab-order-1001-6c449fffb8-x7k2m',uid:''},requestObject:null,responseObject:null})
 const parsed=parseAudit([noOwnerDeployment,noOwnerRs,noOwnerPod].map(row=>JSON.stringify(row)).join('\n'))
 const target=parsed.requests.find(row=>row.resource.resource==='deployments')!.resource
 const related=relatedRequests(parsed.requests,target,true)
 expect(related.map(entry=>`${entry.item.resource.resource}:${entry.kind}`)).toEqual(['deployments:self','replicasets:inferred-child','pods:inferred-child'])
})

it('uses name fallback when an owner UID points outside the imported files',()=>{
 const noOwnerDeployment=event({auditID:'partial-dep',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d1'}})
 const unresolvedRs=event({auditID:'partial-rs',objectRef:{resource:'replicasets',namespace:'myapp',name:'web-abc123',uid:'r1'},responseObject:{metadata:{ownerReferences:[{uid:'not-imported'}]}}})
 const parsed=parseAudit([noOwnerDeployment,unresolvedRs].map(row=>JSON.stringify(row)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true)
 expect(related.map(entry=>entry.kind)).toEqual(['self','inferred-child'])
})

it('links a Pod directly to its Deployment when the time slice has no ReplicaSet row',()=>{
 const dep=event({auditID:'slice-dep',level:'Metadata',objectRef:{resource:'deployments',namespace:'myapp',name:'tcab-order-1001',uid:''},requestObject:null,responseObject:null})
 const pod=event({auditID:'slice-pod',level:'Metadata',objectRef:{resource:'pods',namespace:'myapp',name:'tcab-order-1001-6c449fffb8-x7k2m',uid:''},requestObject:null,responseObject:null})
 const parsed=parseAudit([dep,pod].map(row=>JSON.stringify(row)).join('\n'))
 const related=relatedRequests(parsed.requests,parsed.requests[0].resource,true)
 expect(related.map(entry=>`${entry.item.resource.resource}:${entry.kind}`)).toEqual(['deployments:self','pods:inferred-child'])
})
