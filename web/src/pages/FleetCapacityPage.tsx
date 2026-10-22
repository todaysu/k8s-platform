import { usePermission } from '../auth/context'
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  FormSelect,
  FormSelectOption,
  Grid,
  GridItem,
  Label,
  SearchInput,
  Spinner,
  Title,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core'
import {
  AngleRightIcon,
  CogIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  SyncAltIcon,
} from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useMemo, useState } from 'react'

import { fetchFleetCapacity, type FleetCapacity, type FleetCapacityItem } from '../api/clusters'
import CapacityClassificationDialog from '../components/CapacityClassificationDialog'
import './FleetCapacityPage.css'

type Props = { onOpenCluster: (clusterId: string) => void }
type Risk = 'critical' | 'warning' | 'healthy'

const formatCPU = (value: number) => value >= 1000 ? `${(value / 1000).toFixed(value % 1000 === 0 ? 0 : 1)} 核` : `${value} m`
const formatMemory = (value: number) => `${(value / 1024 / 1024 / 1024).toFixed(value >= 10 * 1024 ** 3 ? 1 : 2)} GiB`
const percentage = (value: number, total: number) => total <= 0 ? 0 : value / total * 100
const percentText = (value: number) => `${value.toFixed(1)}%`

function calculate(item: FleetCapacityItem) {
  const { capacity } = item
  const cpuReserve = capacity.cpu_allocatable_milli * capacity.safety_reserve_percent / 100
  const memoryReserve = capacity.memory_allocatable_bytes * capacity.safety_reserve_percent / 100
  const cpuRemaining = Math.max(0, capacity.cpu_allocatable_milli - capacity.platform.cpu_requests_milli - capacity.business.cpu_requests_milli - capacity.pending.cpu_requests_milli - cpuReserve)
  const memoryRemaining = Math.max(0, capacity.memory_allocatable_bytes - capacity.platform.memory_requests_bytes - capacity.business.memory_requests_bytes - capacity.pending.memory_requests_bytes - memoryReserve)
  const cpuHeadroom = percentage(cpuRemaining, capacity.cpu_allocatable_milli)
  const memoryHeadroom = percentage(memoryRemaining, capacity.memory_allocatable_bytes)
  const missingRequests = (capacity.business.pods_without_cpu_requests ?? 0) + (capacity.business.pods_without_memory_requests ?? 0) + (capacity.platform.pods_without_cpu_requests ?? 0) + (capacity.platform.pods_without_memory_requests ?? 0)
  const newPlatformNamespaces = (capacity.new_platform_namespaces ?? []).length
  const baselineAttention = capacity.comparison.status === 'drift' || capacity.comparison.status === 'rebaseline'
  const minimumHeadroom = Math.min(cpuHeadroom, memoryHeadroom)
  const risk: Risk = minimumHeadroom <= 5 ? 'critical' : minimumHeadroom <= 20 || missingRequests > 0 || newPlatformNamespaces > 0 || baselineAttention ? 'warning' : 'healthy'
  return { baselineAttention, cpuHeadroom, cpuRemaining, memoryHeadroom, memoryRemaining, missingRequests, risk, newPlatformNamespaces }
}

const riskMeta = {
  critical: { label: '高风险', status: 'danger' as const },
  warning: { label: '需关注', status: 'warning' as const },
  healthy: { label: '充足', status: 'success' as const },
}

type EnrichedCapacityItem = FleetCapacityItem & { summary: ReturnType<typeof calculate> }
type CapacityTotals = {
  attention: number
  baselineDrift: number
  baselineMissing: number
  cpuAllocatable: number
  cpuBusiness: number
  cpuPending: number
  cpuPlatform: number
  cpuRemaining: number
  cpuReserve: number
  memoryAllocatable: number
  memoryBusiness: number
  memoryPending: number
  memoryPlatform: number
  memoryRemaining: number
  memoryReserve: number
  missingRequests: number
}
type CapacityPool = {
  clusterType: string
  cpuHeadroom: number
  environment: string
  items: EnrichedCapacityItem[]
  key: string
  memoryHeadroom: number
  risk: Risk
  stackCounts: Record<string, number>
  technologyStacks: string[]
  totals: CapacityTotals
  newPlatformNamespaces: number
}

const emptyTotals = (): CapacityTotals => ({ attention: 0, baselineDrift: 0, baselineMissing: 0, cpuAllocatable: 0, cpuBusiness: 0, cpuPending: 0, cpuPlatform: 0, cpuRemaining: 0, cpuReserve: 0, memoryAllocatable: 0, memoryBusiness: 0, memoryPending: 0, memoryPlatform: 0, memoryRemaining: 0, memoryReserve: 0, missingRequests: 0 })
const inferredClusterType = (clusterType: string | undefined, technologyStack: string) => clusterType || (technologyStack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(technologyStack) ? 'G01' : '未标记')
const poolKey = (environment: string, clusterType: string) => `${environment}\u0000${clusterType || '未标记'}`
const poolLabel = (pool: Pick<CapacityPool, 'environment' | 'clusterType'>) => `${pool.environment} / ${pool.clusterType || '未标记'}`

function aggregate(items: EnrichedCapacityItem[]) {
  return items.reduce((sum, item) => {
    const { capacity, summary } = item
    sum.cpuAllocatable += capacity.cpu_allocatable_milli
    sum.cpuBusiness += capacity.business.cpu_requests_milli
    sum.cpuPending += capacity.pending.cpu_requests_milli
    sum.cpuPlatform += capacity.platform.cpu_requests_milli
    sum.cpuRemaining += summary.cpuRemaining
    sum.cpuReserve += capacity.cpu_allocatable_milli * capacity.safety_reserve_percent / 100
    sum.memoryAllocatable += capacity.memory_allocatable_bytes
    sum.memoryBusiness += capacity.business.memory_requests_bytes
    sum.memoryPending += capacity.pending.memory_requests_bytes
    sum.memoryPlatform += capacity.platform.memory_requests_bytes
    sum.memoryRemaining += summary.memoryRemaining
    sum.memoryReserve += capacity.memory_allocatable_bytes * capacity.safety_reserve_percent / 100
    sum.missingRequests += summary.missingRequests
    if (summary.risk !== 'healthy') sum.attention += 1
    if (capacity.comparison.status === 'not_set') sum.baselineMissing += 1
    if (summary.baselineAttention) sum.baselineDrift += 1
    return sum
  }, emptyTotals())
}

function buildCapacityPools(items: EnrichedCapacityItem[]): CapacityPool[] {
  const groups = new Map<string, EnrichedCapacityItem[]>()
  for (const item of items) {
    const key = poolKey(item.environment, inferredClusterType(item.cluster_type, item.technology_stack))
    groups.set(key, [...(groups.get(key) ?? []), item])
  }
  return [...groups.entries()].map(([key, poolItems]) => {
    const totals = aggregate(poolItems)
    const cpuHeadroom = percentage(totals.cpuRemaining, totals.cpuAllocatable)
    const memoryHeadroom = percentage(totals.memoryRemaining, totals.memoryAllocatable)
    const minimumHeadroom = Math.min(cpuHeadroom, memoryHeadroom)
    const risk: Risk = minimumHeadroom <= 5 ? 'critical' : minimumHeadroom <= 20 || totals.attention > 0 ? 'warning' : 'healthy'
    const newPlatformNamespaces = new Set(poolItems.flatMap((item) => item.capacity.new_platform_namespaces ?? [])).size
    const stackCounts = poolItems.reduce<Record<string, number>>((counts, item) => {
      const stack = item.technology_stack || '未标记'
      counts[stack] = (counts[stack] ?? 0) + 1
      return counts
    }, {})
    const technologyStacks = Object.keys(stackCounts).sort()
    return { clusterType: inferredClusterType(poolItems[0].cluster_type, poolItems[0].technology_stack), cpuHeadroom, environment: poolItems[0].environment, items: poolItems, key, memoryHeadroom, risk, stackCounts, technologyStacks, totals, newPlatformNamespaces }
  }).sort((left, right) => left.environment.localeCompare(right.environment) || left.clusterType.localeCompare(right.clusterType))
}

function Distribution({ allocatable, business, format, pending, platform, reserve, title }: {
  allocatable: number
  business: number
  format: (value: number) => string
  pending: number
  platform: number
  reserve: number
  title: string
}) {
  const remaining = Math.max(0, allocatable - platform - business - pending - reserve)
  const segments = [
    { className: 'is-platform', label: '平台固定', value: platform },
    { className: 'is-business', label: '业务申请', value: business },
    { className: 'is-pending', label: '待调度', value: pending },
    { className: 'is-reserve', label: '安全预留', value: reserve },
    { className: 'is-remaining', label: '可分配余量', value: remaining },
  ]
  return <Card className="ks-fleet-capacity-resource" isFullHeight>
    <CardHeader><CardTitle><Title headingLevel="h2" size="lg">{title}</Title></CardTitle></CardHeader>
    <CardBody>
      <div className="ks-fleet-capacity-total"><span>资源池总量</span><strong>{format(allocatable)}</strong></div>
      <div aria-label={`${title}资源池分布`} className="ks-fleet-capacity-stack">
        {segments.map((segment) => <span className={segment.className} key={segment.label} style={{ width: `${percentage(segment.value, allocatable)}%` }} />)}
      </div>
      <div className="ks-fleet-capacity-legend">
        {segments.map((segment) => <div key={segment.label}><i className={segment.className} /><span>{segment.label}</span><strong>{format(segment.value)}</strong><small>{percentText(percentage(segment.value, allocatable))}</small></div>)}
      </div>
    </CardBody>
  </Card>
}

export default function FleetCapacityPage({ onOpenCluster }: Props) {
 const canConfigure = usePermission('configure')

  const [data, setData] = useState<FleetCapacity | null>(null)
  const [classificationOpen, setClassificationOpen] = useState(() => new URLSearchParams(window.location.search).get('classification') === '1')
  const [clusterType, setClusterType] = useState('ALL')
  const [environment, setEnvironment] = useState('ALL')
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [risk, setRisk] = useState<'ALL' | Risk>('ALL')
  const [selectedPoolKey, setSelectedPoolKey] = useState('')
  const [success, setSuccess] = useState('')

  const closeClassification = () => {
    setClassificationOpen(false)
    if (new URLSearchParams(window.location.search).get('classification') === '1') {
      window.history.replaceState(window.history.state, '', '/capacity')
    }
  }

  const load = useCallback(async () => {
    setError('')
    setIsLoading(true)
    try { setData(await fetchFleetCapacity()) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '读取资源池容量失败。') }
    finally { setIsLoading(false) }
  }, [])

  useEffect(() => {
    const timeout = window.setTimeout(() => void load(), 0)
    const interval = window.setInterval(() => { if (!document.hidden) void load() }, 60_000)
    return () => { window.clearTimeout(timeout); window.clearInterval(interval) }
  }, [load])

  const enriched = useMemo(() => (data?.items ?? []).map((item) => ({ ...item, summary: calculate(item) })), [data])
  const environments = ['ALL', ...new Set(enriched.map((item) => item.environment))]
  const clusterTypes = ['ALL', ...new Set(enriched.map((item) => inferredClusterType(item.cluster_type, item.technology_stack)))]
  const scopedItems = useMemo(() => enriched.filter((item) => (environment === 'ALL' || item.environment === environment)
    && (clusterType === 'ALL' || inferredClusterType(item.cluster_type, item.technology_stack) === clusterType)), [clusterType, enriched, environment])
  const pools = useMemo(() => buildCapacityPools(scopedItems), [scopedItems])
  const activePool = pools.find((pool) => pool.key === selectedPoolKey) ?? pools[0] ?? null
  const visible = useMemo(() => (activePool?.items ?? []).filter((item) => {
    const matchesQuery = !query.trim() || item.cluster_name.toLowerCase().includes(query.trim().toLowerCase())
    return matchesQuery && (risk === 'ALL' || item.summary.risk === risk)
  }).sort((left, right) => {
    const order: Record<Risk, number> = { critical: 0, warning: 1, healthy: 2 }
    return order[left.summary.risk] - order[right.summary.risk]
      || Math.min(left.summary.cpuHeadroom, left.summary.memoryHeadroom) - Math.min(right.summary.cpuHeadroom, right.summary.memoryHeadroom)
  }), [activePool, query, risk])
  const totals = activePool?.totals ?? emptyTotals()
  const newPlatformNamespaces = activePool?.newPlatformNamespaces ?? 0

  if (isLoading && !data) return <div className="ks-capacity-loading"><Spinner aria-label="正在计算资源池容量" /><span>正在汇总各集群可调度容量…</span></div>
  if (!data) return <Alert actionLinks={<Button isInline onClick={() => void load()} variant="link">重新读取</Button>} isInline title="资源池容量读取失败" variant="danger">{error}</Alert>

  const clearFilters = () => { setQuery(''); setEnvironment('ALL'); setClusterType('ALL'); setRisk('ALL'); setSelectedPoolKey('') }
  return <div className="ks-fleet-capacity-page">
    {success && <Alert actionClose={<Button aria-label="关闭成功提示" onClick={() => setSuccess('')} variant="plain" />} isInline title={success} variant="success" />}
    {error && <Alert isInline title="刷新失败，当前显示上一次成功数据" variant="warning">{error}</Alert>}
    {data.failures.length > 0 && <Alert isInline title={`${data.failures.length} 套集群未纳入本次容量汇总`} variant="warning">这些集群的 API Server 或凭据不可用，对应环境与集群类型资源池的余量可能被低估：{data.failures.map((item) => item.cluster_name).join('、')}</Alert>}


    <Card className="ks-capacity-pools-card">
      <Toolbar>
        <ToolbarContent rowWrap={{ default: 'wrap' }}><ToolbarGroup className="ks-filter-group">
          <ToolbarItem className="ks-filter-control"><span className="ks-filter-label">环境</span><FormSelect aria-label="资源池环境筛选" onChange={(_, value) => { setEnvironment(value); setSelectedPoolKey('') }} value={environment}>{environments.map((item) => <FormSelectOption key={item} label={item === 'ALL' ? '全部环境' : item} value={item} />)}</FormSelect></ToolbarItem>
          <ToolbarItem className="ks-filter-control"><span className="ks-filter-label">集群类型</span><FormSelect aria-label="资源池集群类型筛选" onChange={(_, value) => { setClusterType(value); setSelectedPoolKey('') }} value={clusterType}>{clusterTypes.map((item) => <FormSelectOption key={item} label={item === 'ALL' ? '全部类型' : item} value={item} />)}</FormSelect></ToolbarItem>
        </ToolbarGroup><ToolbarItem align={{ default: 'alignEnd' }} className="ks-filter-meta"><span className="ks-result-count">{pools.length} 个资源池</span><Button icon={<CogIcon />} isDisabled={!canConfigure} onClick={() => setClassificationOpen(true)} variant="link">业务命名空间配置</Button><Button icon={<SyncAltIcon />} isLoading={isLoading} onClick={() => void load()} variant="secondary">刷新容量</Button></ToolbarItem></ToolbarContent>
      </Toolbar>
      <div className="ks-table-scroll"><Table aria-label="容量资源池列表" gridBreakPoint="grid-lg" variant="compact"><Thead><Tr><Th>资源池</Th><Th>池内构成</Th><Th>集群</Th><Th>CPU 余量</Th><Th>内存余量</Th><Th>业务申请</Th><Th>状态</Th><Th screenReaderText="操作" /></Tr></Thead><Tbody>
        {pools.map((pool) => <Tr className={activePool?.key === pool.key ? 'is-selected' : ''} isClickable key={pool.key} onRowClick={() => setSelectedPoolKey(pool.key)}>
          <Td dataLabel="资源池"><Button isInline onClick={(event) => { event.stopPropagation(); setSelectedPoolKey(pool.key) }} variant="link">{poolLabel(pool)}</Button>{pool.clusterType === '未标记' && <Label color="orange" isCompact>未分类</Label>}</Td>
          <Td dataLabel="池内构成"><div className="ks-capacity-stack-labels">{pool.technologyStacks.map((stack) => <Label color={stack === 'TCS' ? 'purple' : stack === 'CMBK8S' ? 'blue' : 'grey'} isCompact key={stack}>{stack} {pool.stackCounts[stack]} 套</Label>)}</div></Td>
          <Td dataLabel="集群">{pool.items.length} 套</Td>
          <Td dataLabel="CPU 余量"><strong>{percentText(pool.cpuHeadroom)}</strong><small>{formatCPU(pool.totals.cpuRemaining)}</small></Td>
          <Td dataLabel="内存余量"><strong>{percentText(pool.memoryHeadroom)}</strong><small>{formatMemory(pool.totals.memoryRemaining)}</small></Td>
          <Td dataLabel="业务申请"><strong>{formatCPU(pool.totals.cpuBusiness)}</strong><small>{formatMemory(pool.totals.memoryBusiness)}</small></Td>
          <Td dataLabel="状态"><Label isCompact status={riskMeta[pool.risk].status}>{riskMeta[pool.risk].label}</Label></Td>
          <Td isActionCell><Button aria-label={`查看 ${poolLabel(pool)} 资源池`} icon={<AngleRightIcon />} onClick={(event) => { event.stopPropagation(); setSelectedPoolKey(pool.key) }} variant="plain" /></Td>
        </Tr>)}
      </Tbody></Table></div>
      {pools.length === 0 && <div className="ks-empty-state"><Title headingLevel="h3" size="lg">没有匹配的容量资源池</Title><p>调整环境或集群类型筛选条件。</p><Button onClick={clearFilters} variant="link">清除筛选</Button></div>}
    </Card>

    {activePool && <>
      <div className="ks-active-pool-heading"><div><span>当前资源池</span><strong>{poolLabel(activePool)}</strong></div><div className="ks-capacity-stack-labels">{activePool.technologyStacks.map((stack) => <Label color={stack === 'TCS' ? 'purple' : stack === 'CMBK8S' ? 'blue' : 'grey'} isCompact key={stack}>{stack} {activePool.stackCounts[stack]} 套</Label>)}</div><Label isCompact status={riskMeta[activePool.risk].status}>{riskMeta[activePool.risk].label}</Label><span>{activePool.items.length} 套集群</span></div>
      <Grid className="ks-fleet-capacity-kpis" hasGutter>
        <GridItem lg={3} md={6} span={12}><Card><CardBody><span>资源池集群</span><strong>{activePool.items.length}</strong><small>{poolLabel(activePool)}</small></CardBody></Card></GridItem>
        <GridItem lg={3} md={6} span={12}><Card><CardBody><span>可分配 CPU</span><strong>{formatCPU(totals.cpuRemaining)}</strong><small>占本资源池 {percentText(percentage(totals.cpuRemaining, totals.cpuAllocatable))}</small></CardBody></Card></GridItem>
        <GridItem lg={3} md={6} span={12}><Card><CardBody><span>可分配内存</span><strong>{formatMemory(totals.memoryRemaining)}</strong><small>占本资源池 {percentText(percentage(totals.memoryRemaining, totals.memoryAllocatable))}</small></CardBody></Card></GridItem>
        <GridItem lg={3} md={6} span={12}><Card><CardBody><span>业务已申请</span><strong>{formatCPU(totals.cpuBusiness)}</strong><small>{formatMemory(totals.memoryBusiness)} 内存</small></CardBody></Card></GridItem>
      </Grid>

      <Grid hasGutter>
        <GridItem lg={6} span={12}><Distribution allocatable={totals.cpuAllocatable} business={totals.cpuBusiness} format={formatCPU} pending={totals.cpuPending} platform={totals.cpuPlatform} reserve={totals.cpuReserve} title={`${poolLabel(activePool)} CPU`} /></GridItem>
        <GridItem lg={6} span={12}><Distribution allocatable={totals.memoryAllocatable} business={totals.memoryBusiness} format={formatMemory} pending={totals.memoryPending} platform={totals.memoryPlatform} reserve={totals.memoryReserve} title={`${poolLabel(activePool)} 内存`} /></GridItem>
      </Grid>

      <Grid hasGutter>
        <GridItem lg={4} span={12}><Card className="ks-fleet-capacity-finding"><CardBody><ExclamationTriangleIcon /><div><strong>{totals.missingRequests} 项 Requests 缺失</strong></div></CardBody></Card></GridItem>
        <GridItem lg={4} span={12}><Card className="ks-fleet-capacity-finding"><CardBody><ExclamationCircleIcon /><div><strong>{totals.baselineDrift} 套基线漂移</strong></div></CardBody></Card></GridItem>
        <GridItem lg={4} span={12}><Card className="ks-fleet-capacity-finding"><CardBody><ExclamationTriangleIcon /><div><strong>{newPlatformNamespaces} 个交付后新增平台 Namespace</strong></div></CardBody></Card></GridItem>
      </Grid>
    </>}

    <Card className="ks-fleet-capacity-table-card">
      <CardHeader><CardTitle><Title headingLevel="h2" size="lg">集群容量风险</Title></CardTitle></CardHeader>
      <Toolbar>
        <ToolbarContent rowWrap={{ default: 'wrap' }}><ToolbarGroup className="ks-filter-group">
          <ToolbarItem className="ks-filter-control ks-filter-control--search"><span className="ks-filter-label">搜索</span><SearchInput aria-label="搜索容量集群" onChange={(_, value) => setQuery(value)} onClear={() => setQuery('')} placeholder="按集群名称搜索" value={query} /></ToolbarItem>
          <ToolbarItem className="ks-filter-control"><span className="ks-filter-label">容量风险</span><FormSelect aria-label="容量风险筛选" onChange={(_, value) => setRisk(value as 'ALL' | Risk)} value={risk}><FormSelectOption label="全部风险" value="ALL" /><FormSelectOption label="高风险" value="critical" /><FormSelectOption label="需关注" value="warning" /><FormSelectOption label="充足" value="healthy" /></FormSelect></ToolbarItem>
        </ToolbarGroup><ToolbarItem align={{ default: 'alignEnd' }} className="ks-filter-meta"><span className="ks-result-count">显示 {visible.length} / {activePool?.items.length ?? 0} 套</span>{(query || risk !== 'ALL') && <Button isInline onClick={() => { setQuery(''); setRisk('ALL') }} variant="link">清除列表筛选</Button>}</ToolbarItem></ToolbarContent>
      </Toolbar>
      <div className="ks-table-scroll"><Table aria-label="集群容量列表" gridBreakPoint="grid-lg" variant="compact"><Thead><Tr><Th>集群</Th><Th>风险</Th><Th>CPU 余量</Th><Th>内存余量</Th><Th>业务申请</Th><Th>平台占比</Th><Th>基线</Th><Th>数据质量</Th><Th screenReaderText="操作" /></Tr></Thead><Tbody>
        {visible.map((item) => {
          const { capacity, summary } = item
          const platformShare = Math.max(percentage(capacity.platform.cpu_requests_milli, capacity.cpu_allocatable_milli), percentage(capacity.platform.memory_requests_bytes, capacity.memory_allocatable_bytes))
          return <Tr isClickable key={item.cluster_id} onRowClick={() => onOpenCluster(item.cluster_id)}>
            <Td dataLabel="集群"><Button isInline onClick={(event) => { event.stopPropagation(); onOpenCluster(item.cluster_id) }} variant="link">{item.cluster_name}</Button><small>{item.environment} · {inferredClusterType(item.cluster_type, item.technology_stack)} · {item.technology_stack} · {capacity.worker_nodes} 节点</small></Td>
            <Td dataLabel="风险"><Label isCompact status={riskMeta[summary.risk].status}>{riskMeta[summary.risk].label}</Label></Td>
            <Td dataLabel="CPU 余量"><strong>{percentText(summary.cpuHeadroom)}</strong><small>{formatCPU(summary.cpuRemaining)}</small></Td>
            <Td dataLabel="内存余量"><strong>{percentText(summary.memoryHeadroom)}</strong><small>{formatMemory(summary.memoryRemaining)}</small></Td>
            <Td dataLabel="业务申请"><strong>{formatCPU(capacity.business.cpu_requests_milli)}</strong><small>{formatMemory(capacity.business.memory_requests_bytes)}</small></Td><Td dataLabel="平台占比">{percentText(platformShare)}</Td>
            <Td dataLabel="基线">{capacity.comparison.status === 'stable' ? <Label isCompact color="green">稳定</Label> : capacity.comparison.status === 'not_set' ? <Label isCompact color="grey">未建立</Label> : <Label isCompact color="orange">已漂移</Label>}</Td>
            <Td dataLabel="数据质量">{summary.newPlatformNamespaces > 0 ? <Label isCompact color="orange">{summary.newPlatformNamespaces} 个新增平台 NS</Label> : summary.missingRequests > 0 ? <Label isCompact color="orange">{summary.missingRequests} 项缺失</Label> : <Label isCompact color="green">完整</Label>}</Td>
            <Td isActionCell><Button aria-label={`查看 ${item.cluster_name} 容量`} icon={<AngleRightIcon />} onClick={(event) => { event.stopPropagation(); onOpenCluster(item.cluster_id) }} variant="plain" /></Td>
          </Tr>
        })}
      </Tbody></Table></div>
      {visible.length === 0 && <div className="ks-empty-state"><Title headingLevel="h3" size="lg">没有匹配的容量数据</Title><p>调整筛选条件，或检查集群容量采集状态。</p><Button onClick={clearFilters} variant="link">清除筛选</Button></div>}
    </Card>
    <CapacityClassificationDialog onClose={closeClassification} onSaved={() => { setSuccess('业务命名空间清单已保存。'); void load() }} open={classificationOpen} />
  </div>
}
