import {render,screen,waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,expect,it,vi} from 'vitest'
import FleetMonitoringPage from './FleetMonitoringPage'
const clusters=[{id:'a',name:'test01',environment:'test',status:'healthy',cpuUtilization:0,memoryUtilization:0,metricsAvailable:true,nodes:{ready:1,total:1},lastChecked:''},{id:'b',name:'test02',environment:'test',status:'healthy',cpuUtilization:null,memoryUtilization:null,metricsAvailable:false,nodes:{ready:1,total:1},lastChecked:''}]
afterEach(()=>{vi.unstubAllGlobals();history.replaceState({},'','/')})
it('queries cluster history, shows missing metrics and applies Pod scope',async()=>{history.replaceState({},'','/monitoring?cluster=a&view=pods');const fetcher=vi.fn(async(url:RequestInfo|URL)=>({ok:true,json:async()=>String(url).endsWith('/pods')?{data:[{namespace:'myapp',name:'web'}]}:{data:{source:'monitoring/prometheus',start:'2026-10-19T00:00:00Z',end:'2026-10-19T01:00:00Z',step:30,collected_at:'2026-10-19T01:00:00Z',panels:[{id:'cpu',title:'Pod CPU',unit:'cores',hint:'缺失说明',status:'missing',message:'未采集',series:[]}]}}}));vi.stubGlobal('fetch',fetcher);render(<FleetMonitoringPage clusters={clusters} onOpenCluster={()=>{}} onRefresh={()=>{}}/>);await screen.findByText('数据不足，无法判断健康');const user=userEvent.setup();await user.type(screen.getByLabelText('监控命名空间'),'myapp');await user.type(screen.getByLabelText('监控 Pod'),'web');await user.click(screen.getByRole('button',{name:'应用筛选'}));await waitFor(()=>expect(fetcher.mock.calls.some(([url])=>String(url).includes('namespace=myapp')&&String(url).includes('pod=web'))).toBe(true));await user.selectOptions(screen.getByLabelText('监控集群'),'b');await waitFor(()=>expect(fetcher.mock.calls.some(([url])=>String(url).includes('/clusters/b/monitoring'))).toBe(true));expect(screen.getByLabelText('监控命名空间')).toHaveValue('')})
it('prefers a cluster with metrics and identifies a selected cluster without Prometheus',async()=>{history.replaceState({},'','/monitoring');const fetcher=vi.fn(async(url:RequestInfo|URL)=>String(url).includes('/clusters/b/monitoring')?{ok:false,json:async()=>({error:{code:'MONITORING_NOT_CONFIGURED',message:'该集群未发现 Prometheus 数据源。'}})}:{ok:true,json:async()=>({data:{source:'monitoring/prometheus',start:'2026-10-19T00:00:00Z',end:'2026-10-19T01:00:00Z',step:30,collected_at:'2026-10-19T01:00:00Z',panels:[]}})});vi.stubGlobal('fetch',fetcher);render(<FleetMonitoringPage clusters={[clusters[1],clusters[0]]} onOpenCluster={()=>{}} onRefresh={()=>{}}/>);await waitFor(()=>expect(screen.getByLabelText('监控集群')).toHaveValue('a'));expect(fetcher.mock.calls.some(([url])=>String(url).includes('/clusters/b/monitoring'))).toBe(false);await userEvent.selectOptions(screen.getByLabelText('监控集群'),'b');const notice=await screen.findByText(/test02：该集群未发现 Prometheus/);expect(notice.closest('.pf-v6-c-alert')).toHaveClass('pf-m-info')})
it('applies absolute times and supports dashboard fullscreen',async()=>{history.replaceState({},'','/monitoring?cluster=a&hours=custom&from=2026-10-18T00%3A00%3A00Z&to=2026-10-18T01%3A00%3A00Z');const fetcher=vi.fn(async(_url:RequestInfo|URL)=>({ok:true,json:async()=>({data:{source:'prom',start:'2026-10-18T00:00:00Z',end:'2026-10-18T01:00:00Z',step:30,collected_at:'2026-10-18T01:00:00Z',panels:[]}})}));vi.stubGlobal('fetch',fetcher);const user=userEvent.setup();render(<FleetMonitoringPage clusters={clusters} onOpenCluster={()=>{}} onRefresh={()=>{}}/>);await waitFor(()=>expect(fetcher).toHaveBeenCalled());expect(String(fetcher.mock.calls[0]?.[0])).toContain('start=2026-10-18T00%3A00%3A00.000Z');expect(screen.getByLabelText('监控自动刷新')).toBeDisabled();await user.click(screen.getByRole('button',{name:'按时间查询'}));await user.click(screen.getByRole('button',{name:'看板全屏'}));expect(screen.getByRole('dialog',{name:'监控看板全屏'})).toBeInTheDocument();await user.keyboard('{Escape}');expect(screen.queryByRole('dialog')).not.toBeInTheDocument()})

// 节点性能视图面板较多，必须能按主机收窄，并按类别分组。节点列表来自
// Kubernetes API，选择后由后端收窄 Prometheus 查询，避免加载全部节点历史。
it('groups node panels and queries a selected node on the server',async()=>{
 history.replaceState({},'','/monitoring?cluster=a&view=nodes')
 const panel=(id:string,title:string)=>({id,title,unit:'%',hint:'说明',status:'available',series:['node-a','node-b'].map(name=>({labels:{node:name,instance:'10.0.0.1:9100'},points:[{time:1,value:10},{time:2,value:20}]}))})
 const fetcher=vi.fn(async(url:RequestInfo|URL)=>String(url).endsWith('/nodes')?{ok:true,json:async()=>({data:[{name:'node-a'},{name:'node-b'}]})}:{ok:true,json:async()=>({data:{source:'monitoring/prometheus',start:'2026-10-19T00:00:00Z',end:'2026-10-19T01:00:00Z',step:30,collected_at:'2026-10-19T01:00:00Z',panels:[panel('node_cpu_usage','节点 CPU 使用率'),panel('node_disk_read','节点磁盘读取吞吐量'),panel('node_network_receive','节点网络接收速率')]}})})
 vi.stubGlobal('fetch',fetcher)
 render(<FleetMonitoringPage clusters={clusters} onOpenCluster={()=>{}} onRefresh={()=>{}}/>)
 await screen.findByText('资源水位')
 expect(screen.getByText('磁盘 I/O')).toBeInTheDocument()
 expect(screen.getByText('网络')).toBeInTheDocument()
 // 图例是 button，下拉 option 是 option，用 role 区分二者
 expect(screen.getAllByRole('button',{name:/node-a/}).length).toBeGreaterThan(0)
 const user=userEvent.setup()
 await user.selectOptions(screen.getByLabelText('监控节点'),'node-b')
 await waitFor(()=>expect(fetcher.mock.calls.some(([url])=>String(url).includes('/monitoring?')&&String(url).includes('node=node-b'))).toBe(true))
 expect(new URLSearchParams(location.search).get('node')).toBe('node-b')
})
