import {parseAudit,relatedRequests} from './src/tools/audit.ts'
const event=(extra={})=>({auditID:'a',stage:'ResponseComplete',level:'Metadata',verb:'patch',requestReceivedTimestamp:'2026-10-19T01:00:00Z',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d1'},responseStatus:{code:200},...extra})
const rows=[event(),event({auditID:'b',objectRef:{resource:'replicasets',namespace:'myapp',name:'web-1',uid:'r1'},responseObject:{metadata:{ownerReferences:[{uid:'d1'}]}}}),event({auditID:'c',objectRef:{resource:'deployments',namespace:'myapp',name:'web',uid:'d2'}})]
const p=parseAudit(rows.map(e=>JSON.stringify(e)).join('\n'))
console.log('全部记录:', p.requests.map(r=>`${r.verb} ${r.resource.resource}/${r.resource.name} uid=${r.resource.uid} owners=[${r.owners}]`))
const rel=relatedRequests(p.requests,p.requests[0].resource,true)
console.log('\n以 deployments/web(uid=d1) 为目标:')
rel.forEach(r=>console.log('  ', r.kind, '→', r.item.resource.resource+'/'+r.item.resource.name, 'uid='+r.item.resource.uid))
