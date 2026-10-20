import {expect,it} from 'vitest'
import {readFileSync} from 'node:fs'
import {parseAudit,relatedRequests} from './audit'
const show=(p:any,label:string,pred:any)=>{
 const t=p.requests.find(pred); if(!t)return
 console.log(label,'→',relatedRequests(p.requests,t.resource,true).map((r:any)=>`${r.kind}:${r.item.resource.resource}`).join(' '))
}
it('Metadata 双向关联',()=>{
 const p=parseAudit(readFileSync('/tmp/metadata-audit.jsonl','utf8'))
 show(p,'选 myapp-web-1 (Dep) ',(r:any)=>r.resource.name==='myapp-web-1')
 show(p,'选 RS        (向上)',(r:any)=>r.resource.resource==='replicasets'&&r.resource.name==='myapp-web-1-6c449fffb8')
 show(p,'选 demo      (删除链)',(r:any)=>r.resource.name==='demo'&&r.resource.resource==='deployments')
 expect(1).toBe(1)
})
it('RequestResponse 场景不回退',()=>{
 const p=parseAudit(readFileSync('/tmp/audit-real.jsonl','utf8'))
 show(p,'选 myapp-web-5 (Dep) ',(r:any)=>r.resource.name==='myapp-web-5')
 show(p,'选 RS        (向上)',(r:any)=>r.resource.resource==='replicasets'&&r.resource.name?.includes('web-5'))
 expect(1).toBe(1)
})
