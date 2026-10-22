import {render,screen,waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,expect,it,vi} from 'vitest'
import OperationsToolsPage from './OperationsToolsPage'
import {auditExportCommand,type AuditExportFilters} from '../components/AuditExportDialog'
import {parseAudit} from '../tools/audit'
class AuditWorker {onmessage:null|((event:{data:unknown})=>void)=null;onerror=null;terminate=vi.fn();postMessage(text:string){queueMicrotask(()=>this.onmessage?.({data:{data:parseAudit(text)}}))}}
afterEach(()=>{vi.unstubAllGlobals();history.replaceState({},'','/')})
it('opens audit tool from the tools catalog',async()=>{history.replaceState({},'','/tools');render(<OperationsToolsPage clusters={[]}/>);await userEvent.click(screen.getAllByRole('button',{name:'打开工具'})[0]);expect(location.pathname).toBe('/tools/audit');expect(screen.getByLabelText('导入审计日志')).toBeInTheDocument()})
it('imports a log, selects its resource and shows failed requests without sensitive body',async()=>{
 vi.stubGlobal('Worker',AuditWorker);history.replaceState({},'','/tools/audit');render(<OperationsToolsPage clusters={[]}/>);const user=userEvent.setup()
 const text=JSON.stringify({auditID:'a',stage:'ResponseComplete',level:'RequestResponse',verb:'patch',requestReceivedTimestamp:'2026-10-19T00:00:00Z',user:{username:'deployer'},sourceIPs:['192.0.2.10'],objectRef:{resource:'deployments',name:'web',namespace:'myapp',uid:'uid1'},responseStatus:{code:403,reason:'Forbidden'},requestObject:{spec:{template:{spec:{containers:[{image:'nginx:1.27',env:[{value:'SECRET_VALUE'}]}]}}}}})
 const file=new File([text],'audit.jsonl',{type:'application/json'});Object.defineProperty(file,'text',{value:async()=>text})
 await user.upload(screen.getByLabelText('导入审计日志'),file)
 await waitFor(()=>expect(screen.getByLabelText('选择分析资源')).toBeInTheDocument());const select=screen.getByLabelText('选择分析资源') as HTMLSelectElement
 await user.selectOptions(select,select.options[1].value);expect(screen.getByText('失败 403')).toBeInTheDocument();expect(screen.getByText('deployer · 来源 IP：192.0.2.10 · Forbidden')).toBeInTheDocument();expect(screen.queryByText('SECRET_VALUE')).not.toBeInTheDocument()
 await user.click(screen.getByRole('button',{name:'清除数据'}));expect(screen.queryByLabelText('选择分析资源')).not.toBeInTheDocument()
})
it('filters imported requests by source IP, actor, action and result',async()=>{
 vi.stubGlobal('Worker',AuditWorker);history.replaceState({},'','/tools/audit');render(<OperationsToolsPage clusters={[]}/>);const user=userEvent.setup()
 const base={stage:'ResponseComplete',level:'Metadata',requestReceivedTimestamp:'2026-10-19T00:00:00Z',objectRef:{resource:'deployments',name:'web',namespace:'myapp',uid:'uid1'}}
 const rows=[
  {...base,auditID:'success',verb:'patch',user:{username:'release-bot'},sourceIPs:['192.0.2.10'],responseStatus:{code:200}},
  {...base,auditID:'failed',verb:'delete',user:{username:'ops-admin'},sourceIPs:['198.51.100.7'],responseStatus:{code:403,reason:'Forbidden'}},
  {...base,auditID:'read',verb:'get',user:{username:'viewer'},sourceIPs:['203.0.113.2'],responseStatus:{code:200}}
 ]
 const text=rows.map(row=>JSON.stringify(row)).join('\n');const file=new File([text],'audit.jsonl',{type:'application/json'});Object.defineProperty(file,'text',{value:async()=>text})
 await user.upload(screen.getByLabelText('导入审计日志'),file);await waitFor(()=>expect(screen.getByLabelText('选择分析资源')).toBeInTheDocument())
 const filters=screen.getByLabelText('导入后请求过滤'),resource=screen.getByLabelText('选择分析资源');expect(filters.compareDocumentPosition(resource)&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
 expect(screen.getByText('显示前 3 条')).toBeInTheDocument();await user.selectOptions(screen.getByLabelText('按来源 IP 筛选'),'198.51.100.7')
 expect(screen.getByText('显示前 1 条')).toBeInTheDocument();expect(screen.getByText('删除')).toBeInTheDocument();expect(screen.queryByText('修改')).not.toBeInTheDocument();expect(screen.getByRole('button',{name:'myapp / deployments/web'})).toBeInTheDocument()
 await user.selectOptions(screen.getByLabelText('按操作人筛选'),'ops-admin');await user.selectOptions(screen.getByLabelText('按动作筛选'),'delete');await user.selectOptions(screen.getByLabelText('按结果筛选'),'failed');expect(filters).toHaveTextContent('匹配 1 / 3 条请求')
 await user.click(screen.getByRole('button',{name:'清除请求过滤'}));expect(filters).toHaveTextContent('匹配 3 / 3 条请求')
})
it('builds a UTC node command for a half-open audit time range',()=>{
 const filters:AuditExportFilters={time:true,start:'2026-10-19T09:00',end:'2026-10-19T10:30',auditDirectory:'/var/log/kubernetes/audit',namespace:'payments',resource:'pods',name:'tcab-2026',sourceIp:'192.0.2.25',username:'ops-admin',verb:'create',result:'failed'}
 const command=auditExportCommand(filters)
 expect(command).toContain("AUDIT_DIR='/var/log/kubernetes/audit'")
 expect(command).toContain("RESOURCE='pods'")
 expect(command).toContain("RESOURCE_NAME='tcab-2026'")
 expect(command).toContain("SOURCE_IP='192.0.2.25'")
 expect(command).toContain('jq -Rc')
 expect(command).toContain('(fromjson?) as $record')
 expect(command).not.toContain('\n+')
 expect(command).toContain('fromjson?')
 expect(command).toContain('$timestamp >= $begin')
 expect(command).toContain('wc -c')
})
it('opens optional export filters in a dialog',async()=>{history.replaceState({},'','/tools/audit');render(<OperationsToolsPage clusters={[]}/>);expect(screen.queryByLabelText('节点导出命令')).not.toBeInTheDocument();await userEvent.click(screen.getByRole('button',{name:'生成导出命令'}));expect(screen.getByRole('dialog',{name:'生成审计日志导出命令'})).toBeInTheDocument();expect(screen.getByLabelText('审计导出开始时间')).toBeDisabled();expect((screen.getByLabelText('节点导出命令') as HTMLTextAreaElement).value).toContain('audit.log*')})
it('keeps ReplicaSet and Pod rows when exporting a Deployment name',()=>{
 const filters:AuditExportFilters={time:false,start:'',end:'',auditDirectory:'/var/log/kubernetes/audit',namespace:'payments',resource:'deployments',name:'tcab-order',sourceIp:'',username:'',verb:'',result:''}
 const command=auditExportCommand(filters)
 expect(command).toContain('["deployments","replicasets","pods"]')
 expect(command).toContain('resource_scope($record.objectRef.resource')
})
it('combines two selected files into one analysis',async()=>{
 vi.stubGlobal('Worker',AuditWorker);history.replaceState({},'','/tools/audit');render(<OperationsToolsPage clusters={[]}/>);const user=userEvent.setup()
 const first=JSON.stringify({auditID:'same',stage:'RequestReceived',level:'Metadata',verb:'create',requestReceivedTimestamp:'2026-10-19T00:00:00Z',user:{username:'deployer'},objectRef:{resource:'pods',name:'demo',namespace:'apps',uid:'pod-1'}})
 const second=JSON.stringify({auditID:'same',stage:'ResponseComplete',level:'Metadata',verb:'create',requestReceivedTimestamp:'2026-10-19T00:00:00Z',stageTimestamp:'2026-10-19T00:00:01Z',user:{username:'deployer'},objectRef:{resource:'pods',name:'demo',namespace:'apps',uid:'pod-1'},responseStatus:{code:201}})
 const files=[new File([first],'audit.log',{type:'application/json'}),new File([second],'audit.log.1',{type:'application/json'})];Object.defineProperty(files[0],'text',{value:async()=>first});Object.defineProperty(files[1],'text',{value:async()=>second})
 await user.upload(screen.getByLabelText('导入审计日志'),files);await waitFor(()=>expect(screen.getByText(/2 个文件/)).toBeInTheDocument());expect(screen.getByText('合并后的 API 请求').parentElement).toHaveTextContent('1')
})
it('opens the audit data export dialog with the current filtered result',async()=>{
 vi.stubGlobal('Worker',AuditWorker);history.replaceState({},'','/tools/audit');render(<OperationsToolsPage clusters={[]}/>);const user=userEvent.setup()
 const row=JSON.stringify({auditID:'export',stage:'ResponseComplete',level:'Metadata',verb:'patch',requestReceivedTimestamp:'2026-10-21T00:00:00Z',user:{username:'deployer'},sourceIPs:['192.0.2.10'],objectRef:{resource:'deployments',name:'web',namespace:'payments',uid:'uid-1'},responseStatus:{code:200}})
 const file=new File([row],'audit.jsonl',{type:'application/json'});Object.defineProperty(file,'text',{value:async()=>row})
 await user.upload(screen.getByLabelText('导入审计日志'),file);await waitFor(()=>expect(screen.getByRole('button',{name:'导出数据（1）'})).toBeInTheDocument())
 await user.click(screen.getByRole('button',{name:'导出数据（1）'}))
 expect(screen.getByRole('dialog',{name:'导出审计分析数据'})).toBeInTheDocument()
 expect(screen.getByText('1 条')).toBeInTheDocument()
 expect(screen.getByRole('radio',{name:/CSV/})).toBeChecked()
 await user.click(screen.getByRole('radio',{name:/JSON/}))
 expect(screen.getByRole('checkbox',{name:/包含证据字段/})).toBeChecked()
})
