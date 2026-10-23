import OperationsToolsPage from './OperationsToolsPage'
import FleetMonitoringPage from './FleetMonitoringPage'
import PlatformAccessPage from './PlatformAccessPage'
import SecurityBaselinePage from './SecurityBaselinePage'
import OperationalReportsPage from './OperationalReportsPage'
import { usePermission } from '../auth/context'
import { AccountMenu } from '../auth/Auth'
import {
  Alert,
  AlertActionCloseButton,
  AlertActionLink,
  AlertGroup,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Divider,
  FormSelect,
  FormSelectOption,
  Grid,
  GridItem,
  Label,
  Masthead,
  MastheadBrand,
  MastheadContent,
  MastheadLogo,
  MastheadMain,
  MastheadToggle,
  Nav,
  NavGroup,
  NavItem,
  NavList,
  Page,
  PageSection,
  PageSidebar,
  PageSidebarBody,
  PageToggleButton,
  Pagination,
  SearchInput,
  Title,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core'
import {
  AngleDoubleLeftIcon,
  AngleDoubleRightIcon,
  AngleRightIcon,
  BellIcon,
  BarsIcon,
  ChartLineIcon,
  ChartPieIcon,
  CheckCircleIcon,
  CubesIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  FileAltIcon,
  PlusIcon,
  SearchIcon,
  ServerIcon,
  ShieldAltIcon,
  SyncAltIcon,
  TachometerAltIcon,
} from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { fetchClusters, fetchFleetCapacity, type FleetCapacity, type GlobalResourceResult, type ManagedCluster } from '../api/clusters'
import { fetchPlatformSettings, type PlatformSettings } from '../api/settings'
import AddClusterDialog from '../components/AddClusterDialog'
import GlobalResourceSearch from '../components/GlobalResourceSearch'
import { PlatformBrand, PLATFORM_FULL_NAME } from '../components/PlatformBrand'
import { buildInspectionFindings, type InspectionFinding } from '../inspections/findings'
import ClusterOverviewPage from './ClusterOverviewPage'
import FleetCapacityPage from './FleetCapacityPage'
import FleetEventsPage from './FleetEventsPage'
import FleetInspectionsPage from './FleetInspectionsPage'
import './ClusterManagementPage.css'

type ClusterStatus = 'healthy' | 'warning' | 'unreachable'

type Cluster = {
  apiLatencyMs: number | null
  apiServer: string
  cpuUtilization: number | null
  coreComponents: { ready: number; total: number } | null
  environment: string
  clusterType: string
  id: string
  instances: { ready: number; total: number } | null
  kubernetesVersion: string
  lastChecked: string
  memoryUtilization: number | null
  metricsAvailable: boolean
  name: string
  nodes: { ready: number; total: number } | null
  status: ClusterStatus
  technologyStack: string
  warningCount: number | null
  workerCores: number | null
  workerNodes: number | null
}

function toDisplayCluster(cluster: ManagedCluster): Cluster {
  return {
    apiLatencyMs: cluster.api_latency_ms,
    apiServer: cluster.api_server,
    cpuUtilization: cluster.cpu_utilization,
    coreComponents: cluster.core_components_ready === null || cluster.core_components_total === null
      ? null
      : { ready: cluster.core_components_ready, total: cluster.core_components_total },
    environment: cluster.environment,
    clusterType: cluster.cluster_type || (cluster.technology_stack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(cluster.technology_stack) ? 'G01' : '未标记'),
    id: cluster.id,
    instances: cluster.instances_ready === null || cluster.instances_total === null
      ? null
      : { ready: cluster.instances_ready, total: cluster.instances_total },
    kubernetesVersion: cluster.kubernetes_version,
    lastChecked: new Date(cluster.last_checked_at).toLocaleString('zh-CN', { hour12: false }),
    memoryUtilization: cluster.memory_utilization,
    metricsAvailable: cluster.metrics_available,
    name: cluster.name,
    nodes: cluster.nodes_ready === null || cluster.nodes_total === null
      ? null
      : { ready: cluster.nodes_ready, total: cluster.nodes_total },
    status: cluster.connection_status !== 'connected'
      ? 'unreachable'
      : (
          (cluster.nodes_ready !== null && cluster.nodes_total !== null && cluster.nodes_ready < cluster.nodes_total)
          || (cluster.instances_ready !== null && cluster.instances_total !== null && cluster.instances_ready < cluster.instances_total)
          || (cluster.core_components_ready !== null && cluster.core_components_total !== null && cluster.core_components_ready < cluster.core_components_total)
        ) ? 'warning' : 'healthy',
    technologyStack: cluster.technology_stack || '未标记',
    warningCount: cluster.nodes_ready === null || cluster.nodes_total === null
      || cluster.instances_ready === null || cluster.instances_total === null
      || cluster.core_components_ready === null || cluster.core_components_total === null
      ? null
      : (cluster.nodes_total - cluster.nodes_ready)
        + (cluster.instances_total - cluster.instances_ready)
        + (cluster.core_components_total - cluster.core_components_ready),
    workerCores: cluster.worker_cores,
    workerNodes: cluster.worker_nodes,
  }
}

const statusPriority: Record<ClusterStatus, number> = {
  unreachable: 0,
  warning: 1,
  healthy: 2,
}

const statusMeta: Record<ClusterStatus, {
  icon: typeof CheckCircleIcon
  label: string
  status: 'success' | 'warning' | 'danger'
}> = {
  healthy: { icon: CheckCircleIcon, label: '健康', status: 'success' },
  warning: { icon: ExclamationTriangleIcon, label: '需要关注', status: 'warning' },
  unreachable: { icon: ExclamationCircleIcon, label: '连接失败', status: 'danger' },
}

type NavigationSection = 'overview' | 'clusters' | 'capacity' | 'events' | 'inspections' | 'reports' | 'security' | 'access' | 'tools' | 'monitoring'

const navItems = [
  { icon: TachometerAltIcon, id: 'overview' as const, label: '概览' },
  { icon: ServerIcon, id: 'clusters' as const, label: '集群' },
  { icon: ChartLineIcon, id: 'monitoring' as const, label: '监控' },
  { icon: ChartPieIcon, id: 'capacity' as const, label: '容量' },
  { icon: BellIcon, id: 'events' as const, label: '跨集群事件' },
  { icon: SearchIcon, id: 'inspections' as const, label: '巡检分析' },
  { icon: FileAltIcon, id: 'reports' as const, label: '运行报告' },
  { icon: ShieldAltIcon, id: 'security' as const, label: '安全扫描' },
  { icon: CubesIcon, id: 'tools' as const, label: '运维工具' },
]

const statusFilters = [
  { label: '全部状态', value: 'ALL' },
  { label: '健康', value: 'healthy' },
  { label: '需关注', value: 'warning' },
  { label: '连接失败', value: 'unreachable' },
] as const

function StatusLabel({ status }: { status: ClusterStatus }) {
  const meta = statusMeta[status]
  return <Label isCompact status={meta.status}>{meta.label}</Label>
}

type MetricCardProps = {
  active: boolean
  detail: string
  label: string
  onClick: () => void
  status: 'brand' | 'success' | 'warning' | 'danger'
  value: string
}

function MetricCard({ active, detail, label, onClick, status, value }: MetricCardProps) {
  return (
    <Card
      className={`ks-metric-card ks-metric-card--${status}`}
      component="button"
      isClickable
      isClicked={active}
      onClick={onClick}
    >
      <CardBody>
        <span className="ks-metric-card__label"><i aria-hidden="true" />{label}</span>
        <strong>{value}</strong>
        <small>{detail}</small>
      </CardBody>
    </Card>
  )
}

function AppMasthead({ onOpenSearch, refreshedAt }: { onOpenSearch: () => void; refreshedAt: string }) {
  return (
    <Masthead className="ks-masthead" display={{ default: 'inline' }}>
      <MastheadMain>
        <MastheadToggle>
          <PageToggleButton aria-label="展开或收起导航" isHamburgerButton>
            <BarsIcon />
          </PageToggleButton>
        </MastheadToggle>
        <MastheadBrand>
          <MastheadLogo className="ks-brand" href="#" aria-label={`${PLATFORM_FULL_NAME}首页`}>
            <PlatformBrand />
          </MastheadLogo>
        </MastheadBrand>
      </MastheadMain>
      <MastheadContent>
        <Toolbar className="ks-masthead-toolbar" isFullHeight>
          <ToolbarContent>
            <ToolbarItem className="ks-global-search-trigger ks-masthead-text-item">
              <Button icon={<SearchIcon />} onClick={onOpenSearch} variant="plain">跨集群搜索应用与资源</Button>
            </ToolbarItem>
            <ToolbarItem align={{ default: 'alignEnd' }} className="ks-masthead-text-item ks-refresh-time">
              最近刷新 {refreshedAt}
            </ToolbarItem>

          </ToolbarContent>
        </Toolbar>
      </MastheadContent>
    </Masthead>
  )
}

function AppSidebar({
  activeSection,
  isCollapsed,
  onNavigate,
  onToggle,
}: {
  activeSection: NavigationSection
  isCollapsed: boolean
  onNavigate: (section: NavigationSection) => void
  onToggle: () => void
}) {
  return (
    <PageSidebar className={isCollapsed ? 'ks-sidebar is-collapsed' : 'ks-sidebar'}>
      <PageSidebarBody isFilled className="ks-sidebar-navigation">
        <Nav aria-label="全局导航">
          <NavGroup title="多集群运维">
            <NavList>
              {navItems.map((item) => {
                const Icon = item.icon
                return (
                  <NavItem
                    icon={<Icon />}
                    isActive={'id' in item && item.id === activeSection}
                    itemId={item.label}
                    key={item.label}
                    onClick={'id' in item ? () => onNavigate(item.id!) : undefined}
                    preventDefault
                    to="#"
                  >
                    {[<span key="label" className="ks-nav-label">{item.label}</span>]}
                  </NavItem>
                )
              })}
            </NavList>
          </NavGroup>
        </Nav>
      </PageSidebarBody>
      <Button
        aria-label={isCollapsed ? '展开侧边栏' : '收起侧边栏'}
        className="ks-sidebar-collapse"
        icon={isCollapsed ? <AngleDoubleRightIcon /> : <AngleDoubleLeftIcon />}
        onClick={onToggle}
        variant="plain"
      />
      <PageSidebarBody isFilled={false} className="ks-sidebar-footer"><div className="ks-sidebar-account"><AccountMenu /></div></PageSidebarBody>
    </PageSidebar>
  )
}

function FleetHealthCard({ clusters, onViewAll }: { clusters: Cluster[]; onViewAll: () => void }) {
  const healthy = clusters.filter((cluster) => cluster.status === 'healthy').length
  const warning = clusters.filter((cluster) => cluster.status === 'warning').length
  const unreachable = clusters.filter((cluster) => cluster.status === 'unreachable').length
  const total = clusters.length
  const percentage = (value: number) => total === 0 ? 0 : (value / total) * 100
  const workerNodes = clusters.reduce((sum, cluster) => sum + (cluster.workerNodes ?? 0), 0)
  const workerCores = clusters.reduce((sum, cluster) => sum + (cluster.workerCores ?? 0), 0)
  const latencies = clusters
    .flatMap((cluster) => cluster.apiLatencyMs === null ? [] : [cluster.apiLatencyMs])
    .sort((left, right) => left - right)
  const p95Latency = latencies.length === 0 ? null : latencies[Math.ceil(latencies.length * 0.95) - 1]

  return (
    <Card className="ks-overview-card" isFullHeight>
      <CardHeader actions={{ actions: <Button isInline onClick={onViewAll} variant="link">查看全部集群</Button>, hasNoOffset: false }}>
        <CardTitle><Title headingLevel="h2" size="lg">Fleet 健康分布</Title></CardTitle>
      </CardHeader>
      <CardBody>
        <div className="ks-fleet-total"><strong>{total}</strong><span>套集群</span></div>
        <div aria-label={`${healthy} 个健康、${warning} 个需要关注、${unreachable} 个连接失败`} className="ks-health-bar">
          <span className="ks-health-bar__healthy" style={{ width: `${percentage(healthy)}%` }} />
          <span className="ks-health-bar__warning" style={{ width: `${percentage(warning)}%` }} />
          <span className="ks-health-bar__danger" style={{ width: `${percentage(unreachable)}%` }} />
        </div>
        <div className="ks-health-legend">
          <span><i className="is-healthy" />健康 {healthy}</span>
          <span><i className="is-warning" />需要关注 {warning}</span>
          <span><i className="is-danger" />连接失败 {unreachable}</span>
        </div>
        <Divider />
        <div className="ks-fleet-stats">
          <span><strong>{clusters.some((cluster) => cluster.workerNodes !== null) ? workerNodes : '—'}</strong><small>Worker 节点</small></span>
          <span><strong>{clusters.some((cluster) => cluster.workerCores !== null) ? workerCores : '—'}</strong><small>Worker 核数</small></span>
          <span><strong>{p95Latency === null ? '—' : `${p95Latency} ms`}</strong><small>API P95</small></span>
        </div>
      </CardBody>
    </Card>
  )
}

function ResourceHealthCard({ clusters, onViewResources }: { clusters: Cluster[]; onViewResources: () => void }) {
  const versions = [...new Set(clusters.map((cluster) => cluster.kubernetesVersion))]
  const aggregate = (selector: (cluster: Cluster) => { ready: number; total: number } | null) => {
    const values = clusters.flatMap((cluster) => {
      const value = selector(cluster)
      return value === null ? [] : [value]
    })
    if (values.length === 0) return null
    return values.reduce((total, value) => ({
      ready: total.ready + value.ready,
      total: total.total + value.total,
    }), { ready: 0, total: 0 })
  }
  const rows = [
    { label: '节点 Ready', value: aggregate((cluster) => cluster.nodes) },
    { label: '实例 Ready', value: aggregate((cluster) => cluster.instances) },
    { label: '核心组件正常', value: aggregate((cluster) => cluster.coreComponents) },
  ]
  return (
    <Card className="ks-overview-card" isFullHeight>
      <CardHeader actions={{ actions: <Button isInline onClick={onViewResources} variant="link">查看资源</Button>, hasNoOffset: false }}>
        <CardTitle><Title headingLevel="h2" size="lg">资源健康</Title></CardTitle>
      </CardHeader>
      <CardBody>
        <div className="ks-resource-health">
          {rows.map((row) => {
            const percent = row.value === null || row.value.total === 0 ? 0 : row.value.ready / row.value.total * 100
            return (
            <div className="ks-resource-row" key={row.label}>
              <div>
                <span>{row.label}</span>
                <strong>{row.value === null ? '采集不可用' : `${row.value.ready} / ${row.value.total}`}</strong>
              </div>
              <div aria-label={`${row.label} ${percent.toFixed(1)}%`} className="ks-progress-track">
                <span className={percent < 100 ? 'is-warning' : ''} style={{ width: `${percent}%` }} />
              </div>
            </div>
            )
          })}
        </div>
        <Divider />
        <div className="ks-version-summary">
          <span className="ks-version-summary__title">Kubernetes 版本</span>
          {versions.length === 0 && <span>暂无数据</span>}
          {versions.map((version) => (
            <Label isCompact key={version} variant="outline">
              {version} · {clusters.filter((cluster) => cluster.kubernetesVersion === version).length}
            </Label>
          ))}
        </div>
      </CardBody>
    </Card>
  )
}

function AttentionCard({ findings, onSelect, onViewAll }: {
  findings: InspectionFinding[]
  onSelect: (finding: InspectionFinding) => void
  onViewAll: () => void
}) {
  const actionable = findings.filter((finding) => finding.severity !== 'info')
  const visible = actionable.slice(0, 3)

  return (
    <Card className="ks-overview-card" isFullHeight>
      <CardHeader actions={{ actions: <Button isInline onClick={onViewAll} variant="link">查看全部问题 {actionable.length}</Button>, hasNoOffset: false }}>
        <CardTitle><Title headingLevel="h2" size="lg">需要关注</Title></CardTitle>
      </CardHeader>
      <CardBody className="ks-attention-list">
        {visible.map((finding) => {
          const Icon = finding.severity === 'critical' ? ExclamationCircleIcon : ExclamationTriangleIcon
          return (
            <div className="ks-attention-item" key={finding.id}>
              <Icon className={`ks-status-icon is-${finding.severity === 'critical' ? 'unreachable' : 'warning'}`} />
              <span>
                <strong>{finding.title}</strong>
                <small>{finding.clusterName} · {finding.evidence[0]}</small>
              </span>
              <Button isInline onClick={() => onSelect(finding)} variant="link">查看</Button>
            </div>
          )
        })}
        {visible.length === 0 && <p className="ks-card-subtitle">当前没有需要运维人员处理的问题。</p>}
      </CardBody>
    </Card>
  )
}

const capacityPercent = (value: number, total: number) => total > 0 ? value / total * 100 : 0

function CapacityOverviewCard({ capacity, error, isLoading, onRefresh, onViewCapacity }: {
  capacity: FleetCapacity | null
  error: string
  isLoading: boolean
  onRefresh: () => void
  onViewCapacity: () => void
}) {
  if (isLoading && !capacity) return <Card className="ks-capacity-overview"><CardBody className="ks-capacity-overview__loading"><span className="ks-capacity-overview__pulse" /><span>正在汇总资源池容量…</span></CardBody></Card>
  if (!capacity) return <Alert actionLinks={<AlertActionLink onClick={onRefresh}>重新读取</AlertActionLink>} isInline title="容量摘要暂时不可用" variant="warning">{error || '没有可用的集群容量数据。'}</Alert>

  const poolMap = new Map<string, { attention: boolean; clusterType: string; cpuRemaining: number; cpuTotal: number; environment: string; memoryRemaining: number; memoryTotal: number }>()
  for (const item of capacity.items) {
    const value = item.capacity
    const cpuReserve = value.cpu_allocatable_milli * value.safety_reserve_percent / 100
    const memoryReserve = value.memory_allocatable_bytes * value.safety_reserve_percent / 100
    const clusterType = item.cluster_type || (item.technology_stack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(item.technology_stack) ? 'G01' : '未标记')
    const key = `${item.environment}\u0000${clusterType}`
    const current = poolMap.get(key) ?? { attention: false, clusterType, cpuRemaining: 0, cpuTotal: 0, environment: item.environment, memoryRemaining: 0, memoryTotal: 0 }
    current.cpuTotal += value.cpu_allocatable_milli
    current.cpuRemaining += Math.max(0, value.cpu_allocatable_milli - value.platform.cpu_requests_milli - value.business.cpu_requests_milli - value.pending.cpu_requests_milli - cpuReserve)
    current.memoryTotal += value.memory_allocatable_bytes
    current.memoryRemaining += Math.max(0, value.memory_allocatable_bytes - value.platform.memory_requests_bytes - value.business.memory_requests_bytes - value.pending.memory_requests_bytes - memoryReserve)
    current.attention ||= (value.new_platform_namespaces ?? []).length > 0
      || (value.business.pods_without_cpu_requests ?? 0) > 0 || (value.business.pods_without_memory_requests ?? 0) > 0
      || value.comparison.status === 'drift' || value.comparison.status === 'rebaseline'
    poolMap.set(key, current)
  }
  const pools = [...poolMap.values()].map((pool) => ({ ...pool, cpuHeadroom: capacityPercent(pool.cpuRemaining, pool.cpuTotal), memoryHeadroom: capacityPercent(pool.memoryRemaining, pool.memoryTotal) }))
  const lowestCPU = pools.reduce((lowest, pool) => !lowest || pool.cpuHeadroom < lowest.cpuHeadroom ? pool : lowest, null as (typeof pools)[number] | null)
  const lowestMemory = pools.reduce((lowest, pool) => !lowest || pool.memoryHeadroom < lowest.memoryHeadroom ? pool : lowest, null as (typeof pools)[number] | null)
  const attentionPools = pools.filter((pool) => Math.min(pool.cpuHeadroom, pool.memoryHeadroom) <= 20 || pool.attention).length
  const risk = attentionPools > 0 || capacity.failures.length > 0
  const poolLabel = (pool: (typeof pools)[number] | null) => pool ? `${pool.environment} / ${pool.clusterType}` : '暂无数据'

  return <Card className={`ks-capacity-overview${risk ? ' is-attention' : ''}`}>
    <CardHeader actions={{ actions: <Button isInline onClick={onViewCapacity} variant="link">查看容量详情</Button>, hasNoOffset: false }}><CardTitle><Title headingLevel="h2" size="lg">资源池容量</Title></CardTitle></CardHeader>
    <CardBody>
      <div className="ks-capacity-overview__metrics">
        <div><span>容量资源池</span><strong>{pools.length}</strong></div>
        <div><span>最低 CPU 余量</span><strong>{lowestCPU ? `${lowestCPU.cpuHeadroom.toFixed(1)}%` : '—'}</strong><small>{poolLabel(lowestCPU)}</small></div>
        <div><span>最低内存余量</span><strong>{lowestMemory ? `${lowestMemory.memoryHeadroom.toFixed(1)}%` : '—'}</strong><small>{poolLabel(lowestMemory)}</small></div>
        <div className={attentionPools > 0 ? 'is-warning' : ''}><span>需关注资源池</span><strong>{attentionPools}</strong></div>
      </div>
      {(capacity.failures.length > 0 || error) && <div className="ks-capacity-overview__notice"><ExclamationTriangleIcon /><span>{capacity.failures.length > 0 ? `${capacity.failures.length} 套集群未纳入本次汇总` : '自动刷新失败，当前显示上一次成功数据'}</span></div>}
    </CardBody>
  </Card>
}

function clusterIDFromPath() {
  const match = window.location.pathname.match(/^\/clusters\/([^/]+)\/(?:overview|capacity|nodes|events|terminal|pods\/.*|workloads(?:\/.*)?)\/?$/)
  if (!match) return null
  try {
    return decodeURIComponent(match[1])
  } catch {
    return null
  }
}

function navigationSectionFromLocation(): NavigationSection {
 if (window.location.pathname === '/security' || window.location.pathname === '/tools/security-baseline') return 'security'
 if (window.location.pathname === '/tools' || window.location.pathname.startsWith('/tools/')) return 'tools'
 if (window.location.pathname === '/monitoring') return 'monitoring'
  if (window.location.pathname === '/settings' || window.location.pathname.startsWith('/settings/')) return 'access'
  if (window.location.pathname === '/capacity') return 'capacity'
  if (window.location.pathname === '/events') return 'events'
  if (window.location.pathname === '/inspections') return 'inspections'
  if (window.location.pathname === '/reports' || window.location.pathname.startsWith('/reports/')) return 'reports'
  return window.location.pathname.startsWith('/clusters') ? 'clusters' : 'overview'
}

function ClusterManagementPage() {
 const canConfigure = usePermission('configure')

  const loadingRef = useRef(false)
  const [activeClusterID, setActiveClusterID] = useState<string | null>(() => clusterIDFromPath())
  const [toolTitle, setToolTitle] = useState('运维工具')
  const [activeSection, setActiveSection] = useState<NavigationSection>(() => navigationSectionFromLocation())
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [fleetCapacity, setFleetCapacity] = useState<FleetCapacity | null>(null)
  const [capacityError, setCapacityError] = useState('')
  const [capacityLoading, setCapacityLoading] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [environment, setEnvironment] = useState('ALL')
  const [clusterType, setClusterType] = useState('ALL')
  const [isLoading, setIsLoading] = useState(true)
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false)
  const [globalSearchOpen, setGlobalSearchOpen] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)
  const [query, setQuery] = useState('')
  const [refreshedAt, setRefreshedAt] = useState('—')
  const [clusterRefreshSeconds, setClusterRefreshSeconds] = useState(30)

  const [status, setStatus] = useState<'ALL' | ClusterStatus>('ALL')
  const [technologyStack, setTechnologyStack] = useState('ALL')
  const [successNotice, setSuccessNotice] = useState<{ message: string; title: string } | null>(null)

  const loadClusters = useCallback(async () => {
    if (loadingRef.current) return
    loadingRef.current = true
    setIsLoading(true)
    setLoadError('')
    try {
      const managedClusters = await fetchClusters()
      setClusters(managedClusters.map(toDisplayCluster))
      setRefreshedAt(new Date().toLocaleTimeString('zh-CN', { hour12: false }))
    } catch (requestError) {
      setLoadError(requestError instanceof Error ? requestError.message : '读取集群列表失败，请稍后重试。')
    } finally {
      loadingRef.current = false
      setIsLoading(false)
    }
  }, [])

  const loadCapacity = useCallback(async () => {
    setCapacityLoading(true); setCapacityError('')
    try {
      const result = await fetchFleetCapacity()
      if (!Array.isArray(result.items) || !Array.isArray(result.failures)) throw new Error('平台返回了无法识别的容量摘要。')
      setFleetCapacity(result)
    }
    catch (requestError) { setCapacityError(requestError instanceof Error ? requestError.message : '读取容量摘要失败。') }
    finally { setCapacityLoading(false) }
  }, [])

  useEffect(() => {
    let mounted = true
    void fetchPlatformSettings().then((value) => { if (mounted) setClusterRefreshSeconds(value.runtime.auto_refresh_seconds) }).catch(() => undefined)
    const onSettingsSaved = (event: Event) => setClusterRefreshSeconds((event as CustomEvent<PlatformSettings>).detail.runtime.auto_refresh_seconds)
    window.addEventListener('platform-settings-saved', onSettingsSaved)
    return () => { mounted = false; window.removeEventListener('platform-settings-saved', onSettingsSaved) }
  }, [])

  useEffect(() => {
    const timeoutId = window.setTimeout(() => void loadClusters(), 0)
    const intervalId = window.setInterval(() => { if (!document.hidden) void loadClusters() }, clusterRefreshSeconds * 1000)
    return () => {
      window.clearTimeout(timeoutId)
      window.clearInterval(intervalId)
    }
  }, [clusterRefreshSeconds, loadClusters])

  useEffect(() => {
    if (activeSection !== 'overview' && activeSection !== 'inspections') return undefined
    const timeoutId = window.setTimeout(() => void loadCapacity(), 0)
    const intervalId = window.setInterval(() => { if (!document.hidden) void loadCapacity() }, 60_000)
    return () => { window.clearTimeout(timeoutId); window.clearInterval(intervalId) }
  }, [activeSection, loadCapacity])

  useEffect(() => {
    const handlePopState = () => {
      setActiveClusterID(clusterIDFromPath())
      setActiveSection(navigationSectionFromLocation())
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (!successNotice) return

    const timeoutId = window.setTimeout(() => setSuccessNotice(null), 4000)
    return () => window.clearTimeout(timeoutId)
  }, [successNotice])

  const visibleClusters = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return clusters
      .filter((cluster) => {
        const matchesQuery = !normalizedQuery
          || cluster.name.toLowerCase().includes(normalizedQuery)
          || cluster.apiServer.toLowerCase().includes(normalizedQuery)
        const matchesEnvironment = environment === 'ALL' || cluster.environment === environment
        const matchesClusterType = clusterType === 'ALL' || cluster.clusterType === clusterType
        const matchesStatus = status === 'ALL' || cluster.status === status
        const matchesTechnologyStack = technologyStack === 'ALL' || cluster.technologyStack === technologyStack
        return matchesQuery && matchesEnvironment && matchesClusterType && matchesStatus && matchesTechnologyStack
      })
      .sort((a, b) => statusPriority[a.status] - statusPriority[b.status])
  }, [clusterType, clusters, environment, query, status, technologyStack])

  const healthyCount = clusters.filter((cluster) => cluster.status === 'healthy').length
  const unreachableCount = clusters.filter((cluster) => cluster.status === 'unreachable').length
  const inspectionFindings = useMemo(() => buildInspectionFindings(clusters, fleetCapacity), [clusters, fleetCapacity])
  const actionableFindingCount = inspectionFindings.filter((finding) => finding.severity !== 'info').length
  const environments = ['ALL', ...new Set(clusters.map((cluster) => cluster.environment))]
  const clusterTypes = ['ALL', ...new Set(clusters.map((cluster) => cluster.clusterType))]
  const technologyStacks = ['ALL', ...new Set(clusters.map((cluster) => cluster.technologyStack))]
  const environmentSummary = [...new Set(clusters.map((cluster) => cluster.environment))]
    .map((item) => `${item} ${clusters.filter((cluster) => cluster.environment === item).length}`)
    .join(' · ') || '暂无集群'
  const maxPage = Math.max(1, Math.ceil(visibleClusters.length / perPage))
  const currentPage = Math.min(page, maxPage)
  const pagedClusters = visibleClusters.slice((currentPage - 1) * perPage, currentPage * perPage)

  const clearFilters = () => {
    setEnvironment('ALL')
    setClusterType('ALL')
    setQuery('')
    setStatus('ALL')
    setTechnologyStack('ALL')
    setPage(1)
  }

  const showClusterList = (nextStatus: 'ALL' | ClusterStatus = 'ALL') => {
    setStatus(nextStatus)
    setPage(1)
    if (window.location.pathname !== '/clusters') {
      window.history.pushState({ section: 'clusters' }, '', '/clusters')
    }
    setActiveClusterID(null)
    setActiveSection('clusters')
  }

  const openCluster = (clusterID: string) => {
    window.history.pushState({ clusterID }, '', `/clusters/${encodeURIComponent(clusterID)}/overview`)
    setActiveClusterID(clusterID)
    setActiveSection('clusters')
  }

  const openClusterCapacity = (clusterID: string) => {
    window.history.pushState({ clusterID }, '', `/clusters/${encodeURIComponent(clusterID)}/capacity`)
    setActiveClusterID(clusterID)
    setActiveSection('clusters')
  }

  const openFleet = () => {
    window.history.pushState({ section: 'clusters' }, '', '/clusters')
    setActiveClusterID(null)
    setActiveSection('clusters')
  }

  const openGlobalSearchResult = (result: GlobalResourceResult) => {
    const suffix = result.kind === 'Pod'
      ? ['pods', result.namespace, result.name]
      : ['workloads', result.kind, result.namespace, result.name]
    const path = [result.cluster_id, ...suffix].map((segment) => encodeURIComponent(segment)).join('/')
    window.history.pushState({ globalSearch: result.name }, '', `/clusters/${path}`)
    window.dispatchEvent(new PopStateEvent('popstate'))
    setGlobalSearchOpen(false)
    setActiveClusterID(result.cluster_id)
    setActiveSection('clusters')
  }

  const navigateFromSidebar = (section: NavigationSection) => {
 if (section === 'tools' || section === 'monitoring' || section === 'security' || section === 'reports') { window.history.pushState({section},'',`/${section}`); setActiveClusterID(null); setActiveSection(section); window.dispatchEvent(new PopStateEvent('popstate')); return }
    if (section === 'access') { window.history.pushState({ section }, '', '/settings/access'); setActiveClusterID(null); setActiveSection('access'); return }
    if (section === 'clusters') {
      showClusterList('ALL')
      return
    }
    if (section === 'capacity') {
      if (window.location.pathname !== '/capacity') window.history.pushState({ section: 'capacity' }, '', '/capacity')
      setActiveClusterID(null)
      setActiveSection('capacity')
      return
    }
    if (section === 'events') {
      if (window.location.pathname !== '/events') window.history.pushState({ section: 'events' }, '', '/events')
      setActiveClusterID(null)
      setActiveSection('events')
      return
    }
    if (section === 'inspections') {
      if (window.location.pathname !== '/inspections') window.history.pushState({ section: 'inspections' }, '', '/inspections')
      setActiveClusterID(null)
      setActiveSection('inspections')
      return
    }
    if (window.location.pathname !== '/') {
      window.history.pushState({ section: 'overview' }, '', '/')
    }
    setActiveClusterID(null)
    setActiveSection('overview')
  }

  const masthead = <AppMasthead onOpenSearch={() => setGlobalSearchOpen(true)} refreshedAt={refreshedAt} />
  const sidebar = (
    <AppSidebar
      activeSection={activeSection}
      isCollapsed={isSidebarCollapsed}
      onNavigate={navigateFromSidebar}
      onToggle={() => setIsSidebarCollapsed((current) => !current)}
    />
  )

  return (
    <Page
      className={`ks-page${isSidebarCollapsed ? ' ks-page--sidebar-collapsed' : ''}`}
      defaultManagedSidebarIsOpen
      isManagedSidebar
      masthead={masthead}
      sidebar={sidebar}
    >
      <GlobalResourceSearch onClose={() => setGlobalSearchOpen(false)} onOpenResult={openGlobalSearchResult} open={globalSearchOpen} />
      {activeClusterID ? (
        <ClusterOverviewPage
          key={activeClusterID}
          clusterId={activeClusterID}
          onBack={openFleet}
          onRemoved={(name) => {
            setClusters((current) => current.filter((cluster) => cluster.id !== activeClusterID))
            setSuccessNotice({ message: `${name} 已从平台解除纳管。`, title: '集群已移除' })
            window.history.replaceState({ section: 'clusters' }, '', '/clusters')
            setActiveClusterID(null)
            setActiveSection('clusters')
          }}
        />
      ) : (
        <>
            <PageSection className="ks-title-section" variant="secondary">
              <div className="ks-title-row">
                <div>
                  <Title headingLevel="h1" size="2xl">
                    {activeSection === 'overview' ? '多集群健康概览' : activeSection === 'capacity' ? '资源池容量' : activeSection === 'events' ? '跨集群事件' : activeSection === 'inspections' ? '主动巡检' : activeSection === 'reports' ? '运行报告' : activeSection === 'security' ? '安全扫描' : activeSection === 'access' ? '平台管理' : activeSection === 'tools' ? toolTitle : activeSection === 'monitoring' ? '监控' : '集群'}
                  </Title>

                </div>
                {(activeSection === 'overview' || activeSection === 'clusters' || activeSection === 'inspections') && (
                  <div className="ks-title-actions">
                    <Button icon={<SyncAltIcon />} isLoading={isLoading || (activeSection === 'inspections' && capacityLoading)} onClick={() => { void loadClusters(); if (activeSection === 'inspections') void loadCapacity() }} variant="secondary">{activeSection === 'inspections' ? '重新巡检' : '刷新状态'}</Button>
                    {activeSection === 'clusters' && canConfigure && (
                      <Button icon={<PlusIcon />} onClick={() => setDialogOpen(true)} variant="primary">添加集群</Button>
                    )}
                  </div>
                )}
              </div>
            </PageSection>

            <PageSection className="ks-content-section">
              {activeSection !== 'access' && activeSection !== 'tools' && activeSection !== 'security' && activeSection !== 'reports' && loadError && <Alert isInline title="集群列表读取失败" variant="danger">{loadError}</Alert>}
              {activeSection !== 'access' && activeSection !== 'tools' && activeSection !== 'security' && activeSection !== 'reports' && unreachableCount > 0 && (
                <Alert
                  actionLinks={<AlertActionLink onClick={() => showClusterList('unreachable')}>查看异常集群</AlertActionLink>}
                  isInline
                  title={`${unreachableCount} 个集群连接失败`}
                  variant="danger"
                >
                  请进入集群详情检查 API Server 连通性与凭据状态。
                </Alert>
              )}

              {activeSection === 'overview' ? <div className="ks-overview-layout">
              <Grid className="ks-metric-grid" hasGutter>
                <GridItem lg={3} md={6} span={12}>
                  <MetricCard active={status === 'ALL'} detail={environmentSummary} label="已纳管集群" onClick={() => showClusterList('ALL')} status="brand" value={String(clusters.length)} />
                </GridItem>
                <GridItem lg={3} md={6} span={12}>
                  <MetricCard active={status === 'healthy'} detail="节点、实例与核心组件正常" label="健康集群" onClick={() => showClusterList('healthy')} status="success" value={String(healthyCount)} />
                </GridItem>
                <GridItem lg={3} md={6} span={12}>
                  <MetricCard active={false} detail="由健康、容量和配置规则统一生成" label="需要关注" onClick={() => navigateFromSidebar('inspections')} status="warning" value={String(actionableFindingCount)} />
                </GridItem>
                <GridItem lg={3} md={6} span={12}>
                  <MetricCard active={status === 'unreachable'} detail="API Server 或采集权限异常" label="连接失败" onClick={() => showClusterList('unreachable')} status="danger" value={String(unreachableCount)} />
                </GridItem>
              </Grid>

              <Grid className="ks-overview-grid" hasGutter>
                <GridItem lg={4} span={12}><FleetHealthCard clusters={clusters} onViewAll={() => showClusterList('ALL')} /></GridItem>
                <GridItem lg={4} span={12}><ResourceHealthCard clusters={clusters} onViewResources={() => showClusterList('ALL')} /></GridItem>
                <GridItem lg={4} span={12}>
                  <AttentionCard
                    findings={inspectionFindings}
                    onSelect={(finding) => finding.destination === 'capacity' ? openClusterCapacity(finding.clusterId) : openCluster(finding.clusterId)}
                    onViewAll={() => navigateFromSidebar('inspections')}
                  />
                </GridItem>
              </Grid>
              <div className="ks-capacity-overview-wrap"><CapacityOverviewCard capacity={fleetCapacity} error={capacityError} isLoading={capacityLoading} onRefresh={() => void loadCapacity()} onViewCapacity={() => navigateFromSidebar('capacity')} /></div>
              </div> : activeSection === 'capacity' ? (
                <FleetCapacityPage onOpenCluster={openClusterCapacity} />
              ) : activeSection === 'tools' ? (
 <OperationsToolsPage onTitleChange={setToolTitle} clusters={clusters}/>
 ) : activeSection === 'security' ? (
 <SecurityBaselinePage />
 ) : activeSection === 'monitoring' ? (
 <FleetMonitoringPage clusters={clusters} onOpenCluster={openCluster} onRefresh={()=>void loadClusters()}/>
 ) : activeSection === 'access' ? (
                <PlatformAccessPage />
              ) : activeSection === 'events' ? (
                <FleetEventsPage clusters={clusters} />
              ) : activeSection === 'inspections' ? (
                <FleetInspectionsPage findings={inspectionFindings} onOpenCapacity={openClusterCapacity} onOpenCluster={openCluster} />
              ) : activeSection === 'reports' ? (
                <OperationalReportsPage clusters={clusters} />
              ) : (
              <Card className="ks-cluster-card" id="managed-clusters">
                <CardHeader>
                  <CardTitle>
                    <Title headingLevel="h2" size="lg">已纳管集群</Title>
                    <span className="ks-card-subtitle">{clusters.length} 套集群</span>
                  </CardTitle>
                </CardHeader>
                <Toolbar className="ks-cluster-toolbar" id="cluster-toolbar">
                  <ToolbarContent rowWrap={{ default: 'wrap' }}>
                    <ToolbarGroup className="ks-filter-group">
                      <ToolbarItem className="ks-filter-control ks-filter-control--search">
                        <span className="ks-filter-label">搜索</span>
                        <SearchInput
                          aria-label="搜索集群"
                          onChange={(_, value) => { setQuery(value); setPage(1) }}
                          onClear={() => { setQuery(''); setPage(1) }}
                          placeholder="按名称或 API 地址搜索"
                          value={query}
                        />
                      </ToolbarItem>
                      <ToolbarItem className="ks-filter-control">
                        <span className="ks-filter-label">环境</span>
                        <FormSelect
                          aria-label="环境筛选"
                          onChange={(_, value) => { setEnvironment(value); setPage(1) }}
                          value={environment}
                        >
                          {environments.map((item) => (
                            <FormSelectOption key={item} label={item === 'ALL' ? '全部环境' : item} value={item} />
                          ))}
                        </FormSelect>
                      </ToolbarItem>
                      <ToolbarItem className="ks-filter-control">
                        <span className="ks-filter-label">集群类型</span>
                        <FormSelect
                          aria-label="集群类型筛选"
                          onChange={(_, value) => { setClusterType(value); setPage(1) }}
                          value={clusterType}
                        >
                          {clusterTypes.map((item) => (
                            <FormSelectOption key={item} label={item === 'ALL' ? '全部类型' : item} value={item} />
                          ))}
                        </FormSelect>
                      </ToolbarItem>
                      <ToolbarItem className="ks-filter-control">
                        <span className="ks-filter-label">状态</span>
                        <FormSelect
                          aria-label="状态筛选"
                          onChange={(_, value) => { setStatus(value as 'ALL' | ClusterStatus); setPage(1) }}
                          value={status}
                        >
                          {statusFilters.map((item) => (
                            <FormSelectOption key={item.value} label={item.label} value={item.value} />
                          ))}
                        </FormSelect>
                      </ToolbarItem>
                      <ToolbarItem className="ks-filter-control ks-filter-control--technology">
                        <span className="ks-filter-label">技术栈</span>
                        <FormSelect
                          aria-label="技术栈筛选"
                          onChange={(_, value) => { setTechnologyStack(value); setPage(1) }}
                          value={technologyStack}
                        >
                          {technologyStacks.map((item) => (
                            <FormSelectOption key={item} label={item === 'ALL' ? '全部技术栈' : item} value={item} />
                          ))}
                        </FormSelect>
                      </ToolbarItem>
                    </ToolbarGroup>
                    <ToolbarItem align={{ default: 'alignEnd' }} className="ks-filter-meta">
                      <span className="ks-result-count">显示 {visibleClusters.length} / {clusters.length} 套</span>
                      {(query || environment !== 'ALL' || clusterType !== 'ALL' || status !== 'ALL' || technologyStack !== 'ALL') && (
                        <Button isInline onClick={clearFilters} variant="link">清除筛选</Button>
                      )}
                      <span className="ks-auto-refresh"><SyncAltIcon /> 每 {clusterRefreshSeconds} 秒刷新</span>
                    </ToolbarItem>
                  </ToolbarContent>
                </Toolbar>

                <div className="ks-table-scroll">
                  <Table aria-label="集群列表" gridBreakPoint="grid-lg" variant="compact">
                    <Thead>
                      <Tr>
                        <Th width={20}>集群</Th>
                        <Th>状态</Th>
                        <Th>节点</Th>
                        <Th>实例</Th>
                        <Th>CPU</Th>
                        <Th>内存</Th>
                        <Th>API 延迟</Th>
                        <Th>异常</Th>
                        <Th>最后检查</Th>
                        <Th screenReaderText="操作" />
                      </Tr>
                    </Thead>
                    <Tbody>
                      {pagedClusters.map((cluster) => (
                        <Tr isClickable key={cluster.id} onRowClick={() => openCluster(cluster.id)}>
                          <Td dataLabel="集群">
                            <Button className="ks-cluster-link" isInline onClick={(event) => { event.stopPropagation(); openCluster(cluster.id) }} variant="link">
                              {cluster.name}
                            </Button>
                            <span className="ks-cluster-meta">{cluster.environment} · {cluster.clusterType} · {cluster.technologyStack} · {cluster.kubernetesVersion}</span>
                          </Td>
                          <Td dataLabel="状态"><StatusLabel status={cluster.status} /></Td>
                          <Td dataLabel="节点">{cluster.nodes ? `${cluster.nodes.ready} / ${cluster.nodes.total}` : '—'}</Td>
                          <Td dataLabel="实例">{cluster.instances ? `${cluster.instances.ready} / ${cluster.instances.total}` : '—'}</Td>
                          <Td dataLabel="CPU">{cluster.cpuUtilization === null ? '—' : `${cluster.cpuUtilization.toFixed(1)}%`}</Td>
                          <Td dataLabel="内存">{cluster.memoryUtilization === null ? '—' : `${cluster.memoryUtilization.toFixed(1)}%`}</Td>
                          <Td dataLabel="API 延迟">{cluster.apiLatencyMs === null ? '—' : `${cluster.apiLatencyMs} ms`}</Td>
                          <Td dataLabel="异常">
                            <span className={`ks-event-count is-${cluster.status}`}>
                              {cluster.status === 'unreachable' ? '连接中断' : cluster.warningCount === null ? '—' : `${cluster.warningCount} 条`}
                            </span>
                          </Td>
                          <Td dataLabel="最后检查">{cluster.lastChecked}</Td>
                          <Td isActionCell>
                            <Button aria-label={`查看 ${cluster.name}`} icon={<AngleRightIcon />} onClick={(event) => { event.stopPropagation(); openCluster(cluster.id) }} variant="plain" />
                          </Td>
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </div>

                {visibleClusters.length === 0 && (
                  <div className="ks-empty-state">
                    <SearchIcon />
                    <Title headingLevel="h3" size="lg">{clusters.length === 0 ? '还没有纳管集群' : '没有匹配的集群'}</Title>
                    <p>{clusters.length === 0 ? '点击“添加集群”，上传 Kubeconfig 开始纳管。' : '调整搜索关键词或清除当前筛选条件。'}</p>
                    {clusters.length > 0 && <Button onClick={clearFilters} variant="link">清除筛选</Button>}
                  </div>
                )}

                <Pagination
                  isCompact
                  itemCount={visibleClusters.length}
                  onPerPageSelect={(_, nextPerPage) => { setPerPage(nextPerPage); setPage(1) }}
                  onSetPage={(_, nextPage) => setPage(nextPage)}
                  page={currentPage}
                  perPage={perPage}
                  titles={{ items: '集群', ofWord: '共', paginationAriaLabel: '集群分页' }}
                  variant="bottom"
                />
              </Card>
              )}
            </PageSection>
        </>
      )}
      {successNotice && (
        <AlertGroup isLiveRegion isToast>
          <Alert
            actionClose={<AlertActionCloseButton onClose={() => setSuccessNotice(null)} />}
            title={successNotice.title}
            variant="success"
          >
            {successNotice.message}
          </Alert>
        </AlertGroup>
      )}
      <AddClusterDialog
        environmentOptions={[...new Set(clusters.map((cluster) => cluster.environment))]}
        onClose={() => setDialogOpen(false)}
        onSuccess={(cluster) => {
          setSuccessNotice({ message: `${cluster.name} 已成功纳管。`, title: '集群纳管成功' })
          void loadClusters()
        }}
        open={activeSection === 'clusters' && dialogOpen}
      />
    </Page>
  )
}

export default ClusterManagementPage
