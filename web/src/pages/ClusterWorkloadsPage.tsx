import { usePermission } from '../auth/context'
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Checkbox,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  FormSelect,
  FormSelectOption,
  Grid,
  GridItem,
  Label,
  SearchInput,
  Spinner,
  Tab,
  Tabs,
  TabTitleText,
  TextInput,
  Title,
} from '@patternfly/react-core'
import {
  AngleRightIcon,
  CheckCircleIcon,
  CubesIcon,
  ExclamationCircleIcon,
  InProgressIcon,
  SearchIcon,
  SyncAltIcon,
} from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'

import {
  fetchClusterPod,
  fetchPodLogs,
  fetchClusterWorkload,
  fetchClusterWorkloadYAML,
  fetchClusterPods,
  fetchClusterWorkloads,
  type ClusterPod,
  type ClusterPodDetail,
  type PodLogSnapshot,
  type ClusterWorkload,
  type ClusterWorkloadDetail,
  type WorkloadPod,
  type WorkloadYAML,
} from '../api/clusters'
import './ClusterWorkloadsPage.css'

const PodTerminal = lazy(() => import('../components/PodTerminal'))

type LogTokenKind = 'debug' | 'error' | 'info' | 'ip' | 'timestamp' | 'warning'

const logTokenPattern = /(?<timestamp>\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b)|(?<error>\b(?:critical|crit|error|err|fatal|panic)\b)|(?<warning>\b(?:warn|warning)\b)|(?<info>\b(?:info|notice)\b)|(?<debug>\b(?:debug|trace)\b)|(?<ipv4>\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b)|(?<ipv6>\[[0-9a-f:]+\]|(?<![\w:])(?:[0-9a-f]{1,4}:){2,7}[0-9a-f]{0,4}(?![\w:]))/gi
const errorLevelPattern = /\b(?:critical|crit|error|err|fatal|panic)\b/i
const warningLevelPattern = /\b(?:warn|warning)\b/i

function tokenKind(match: RegExpMatchArray): LogTokenKind {
  if (match.groups?.error) return 'error'
  if (match.groups?.warning) return 'warning'
  if (match.groups?.info) return 'info'
  if (match.groups?.debug) return 'debug'
  if (match.groups?.timestamp) return 'timestamp'
  return 'ip'
}

function highlightedLogLine(line: string, lineIndex: number) {
  const parts: React.ReactNode[] = []
  let cursor = 0

  for (const match of line.matchAll(logTokenPattern)) {
    const start = match.index
    if (start > cursor) parts.push(line.slice(cursor, start))
    const kind = tokenKind(match)
    parts.push(<mark className={`ks-log-token is-${kind}`} key={`${lineIndex}-${start}`}>{match[0]}</mark>)
    cursor = start + match[0].length
  }
  if (cursor < line.length) parts.push(line.slice(cursor))

  const severity = errorLevelPattern.test(line) ? 'error' : warningLevelPattern.test(line) ? 'warning' : 'normal'
  return <span className={`ks-log-line is-${severity}`} key={lineIndex}>{parts.length > 0 ? parts : line || ' '}</span>
}

function HighlightedLog({ text }: { text: string }) {
  return <pre>{text.split('\n').map(highlightedLogLine)}</pre>
}

type WorkloadRoute = {
  kind: string
  name: string
  namespace: string
}

type PodRoute = {
  name: string
  namespace: string
}

type LogTimeRange = 'tail' | '15m' | '1h' | '6h' | 'custom'

const logRangeMinutes: Record<Exclude<LogTimeRange, 'tail' | 'custom'>, number> = {
  '15m': 15,
  '1h': 60,
  '6h': 360,
}

function localDateTimeValue(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

type Props = {
  clusterId: string
}

const statusMeta = {
  degraded: { color: 'red' as const, icon: <ExclamationCircleIcon />, label: '异常' },
  healthy: { color: 'green' as const, icon: <CheckCircleIcon />, label: '正常' },
  progressing: { color: 'blue' as const, icon: <InProgressIcon />, label: '进行中' },
  unknown: { color: 'grey' as const, icon: <ExclamationCircleIcon />, label: '未知' },
}

function routeFromPath(clusterId: string): WorkloadRoute | null {
  const prefix = `/clusters/${encodeURIComponent(clusterId)}/workloads/`
  if (!window.location.pathname.startsWith(prefix)) return null
  const values = window.location.pathname.slice(prefix.length).split('/')
  if (values.length !== 3 || values.some((value) => !value)) return null
  try {
    return { kind: decodeURIComponent(values[0]), namespace: decodeURIComponent(values[1]), name: decodeURIComponent(values[2]) }
  } catch {
    return null
  }
}

function podRouteFromPath(clusterId: string): PodRoute | null {
  const prefix = `/clusters/${encodeURIComponent(clusterId)}/pods/`
  if (!window.location.pathname.startsWith(prefix)) return null
  const values = window.location.pathname.slice(prefix.length).split('/')
  if (values.length !== 2 || values.some((value) => !value)) return null
  try {
    return { namespace: decodeURIComponent(values[0]), name: decodeURIComponent(values[1]) }
  } catch {
    return null
  }
}

function formatTime(value: string | null) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function formatAge(value: string) {
  const createdAt = new Date(value).getTime()
  if (Number.isNaN(createdAt)) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - createdAt) / 1000))
  if (seconds < 3600) return `${Math.max(1, Math.floor(seconds / 60))} 分钟`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时`
  return `${Math.floor(seconds / 86400)} 天`
}

function workloadStatus(workload: ClusterWorkload) {
  const meta = statusMeta[workload.status] ?? statusMeta.unknown
  return <Label color={meta.color} icon={meta.icon}>{meta.label}</Label>
}

function workloadKind(kind: ClusterWorkload['kind']) {
  return kind
}

function podStatus(pod: WorkloadPod) {
  if (pod.phase === 'Running' && pod.ready_containers === pod.total_containers) return <Label color="green">Running</Label>
  if (pod.phase === 'Failed') return <Label color="red">{pod.status_reason || 'Failed'}</Label>
  if (pod.phase === 'Succeeded') return <Label color="blue">Succeeded</Label>
  if (pod.phase === 'Pending' || pod.phase === 'Running') return <Label color="orange">{pod.status_reason || pod.phase}</Label>
  return <Label color="grey">{pod.status_reason || pod.phase || 'Unknown'}</Label>
}

function isPodReady(pod: WorkloadPod) {
  return pod.phase === 'Running' && pod.ready_containers === pod.total_containers
}

export default function ClusterWorkloadsPage({ clusterId }: Props) {
 const canExec = usePermission('exec')

  const initialFilters = new URLSearchParams(window.location.search)
  const [detailRoute, setDetailRoute] = useState<WorkloadRoute | null>(() => routeFromPath(clusterId))
  const [detail, setDetail] = useState<ClusterWorkloadDetail | null>(null)
  const [workloadSection, setWorkloadSection] = useState<'details' | 'yaml'>('details')
  const [workloadYAML, setWorkloadYAML] = useState<WorkloadYAML | null>(null)
  const [yamlError, setYAMLError] = useState('')
  const [yamlLoading, setYAMLLoading] = useState(false)
  const [yamlCopied, setYAMLCopied] = useState(false)
  const [podDetailRoute, setPodDetailRoute] = useState<PodRoute | null>(() => podRouteFromPath(clusterId))
  const [podDetail, setPodDetail] = useState<ClusterPodDetail | null>(null)
  const [podSection, setPodSection] = useState<'details' | 'logs' | 'terminal'>(() => new URLSearchParams(window.location.search).get('section') === 'logs' ? 'logs' : 'details')
  const [logContainer, setLogContainer] = useState('')
  const [logTailLines, setLogTailLines] = useState('200')
  const [logPrevious, setLogPrevious] = useState(() => new URLSearchParams(window.location.search).get('previous') === 'true')
  const [logTimeRange, setLogTimeRange] = useState<LogTimeRange>('tail')
  const [customLogSince, setCustomLogSince] = useState('')
  const [customLogUntil, setCustomLogUntil] = useState('')
  const [podLogs, setPodLogs] = useState<PodLogSnapshot | null>(null)
  const [logsError, setLogsError] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [error, setError] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [kind, setKind] = useState('ALL')
  const [namespace, setNamespace] = useState(() => initialFilters.get('namespace') || 'ALL')
  const [query, setQuery] = useState('')
  const [resourceView, setResourceView] = useState<'controllers' | 'pods'>(() => (
    initialFilters.get('view') === 'pods' ? 'pods' : 'controllers'
  ))
  const [status, setStatus] = useState(() => (
    initialFilters.get('attention') === '1' ? 'ATTENTION' : 'ALL'
  ))
  const [pods, setPods] = useState<ClusterPod[]>([])
  const [workloads, setWorkloads] = useState<ClusterWorkload[]>([])

  const loadList = useCallback(async () => {
    setIsLoading(true)
    setError('')
    try {
      const [nextWorkloads, nextPods] = await Promise.all([
        fetchClusterWorkloads(clusterId),
        fetchClusterPods(clusterId),
      ])
      setWorkloads(nextWorkloads)
      setPods(nextPods)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '读取工作负载失败，请稍后重试。')
    } finally {
      setIsLoading(false)
    }
  }, [clusterId])

  const loadDetail = useCallback(async (route: WorkloadRoute) => {
    setIsLoading(true)
    setError('')
    try {
      setDetail(await fetchClusterWorkload(clusterId, route.kind, route.namespace, route.name))
    } catch (requestError) {
      setDetail(null)
      setError(requestError instanceof Error ? requestError.message : '读取工作负载详情失败，请稍后重试。')
    } finally {
      setIsLoading(false)
    }
  }, [clusterId])

  const loadPodDetail = useCallback(async (route: PodRoute) => {
    setIsLoading(true)
    setError('')
    try {
      const nextPod = await fetchClusterPod(clusterId, route.namespace, route.name)
      setPodDetail(nextPod)
      setLogContainer((current) => nextPod.containers.some((container) => container.name === current) ? current : nextPod.containers[0]?.name ?? '')
    } catch (requestError) {
      setPodDetail(null)
      setError(requestError instanceof Error ? requestError.message : '读取 Pod 详情失败，请稍后重试。')
    } finally {
      setIsLoading(false)
    }
  }, [clusterId])

  const loadWorkloadYAML = useCallback(async () => {
    if (!detailRoute) return
    setYAMLLoading(true)
    setYAMLError('')
    try {
      setWorkloadYAML(await fetchClusterWorkloadYAML(clusterId, detailRoute.kind, detailRoute.namespace, detailRoute.name))
    } catch (requestError) {
      setWorkloadYAML(null)
      setYAMLError(requestError instanceof Error ? requestError.message : '读取工作负载 YAML 失败。')
    } finally {
      setYAMLLoading(false)
    }
  }, [clusterId, detailRoute])

  const loadPodLogs = useCallback(async () => {
    if (!podDetailRoute || !logContainer) return
    let since: string | undefined
    let until: string | undefined
    if (logTimeRange === 'custom') {
      const sinceDate = new Date(customLogSince)
      const untilDate = new Date(customLogUntil)
      if (!customLogSince || !customLogUntil || Number.isNaN(sinceDate.getTime()) || Number.isNaN(untilDate.getTime()) || sinceDate >= untilDate) {
        setPodLogs(null)
        setLogsError('请选择有效的开始和结束时间，且开始时间必须早于结束时间。')
        return
      }
      since = sinceDate.toISOString()
      until = untilDate.toISOString()
    } else if (logTimeRange !== 'tail') {
      since = new Date(Date.now() - logRangeMinutes[logTimeRange] * 60_000).toISOString()
    }
    setLogsLoading(true)
    setLogsError('')
    try {
      setPodLogs(await fetchPodLogs(
        clusterId,
        podDetailRoute.namespace,
        podDetailRoute.name,
        logContainer,
        Number(logTailLines),
        logPrevious,
        since,
        until,
      ))
    } catch (requestError) {
      setPodLogs(null)
      setLogsError(requestError instanceof Error ? requestError.message : '读取容器日志失败，请稍后重试。')
    } finally {
      setLogsLoading(false)
    }
  }, [clusterId, customLogSince, customLogUntil, logContainer, logPrevious, logTailLines, logTimeRange, podDetailRoute])

  const selectLogTimeRange = (value: string) => {
    const nextRange = value as LogTimeRange
    if (nextRange === 'custom' && (!customLogSince || !customLogUntil)) {
      const now = new Date()
      setCustomLogSince(localDateTimeValue(new Date(now.getTime() - 60 * 60_000)))
      setCustomLogUntil(localDateTimeValue(now))
    }
    setLogTimeRange(nextRange)
  }

  useEffect(() => {
    const load = () => podDetailRoute ? loadPodDetail(podDetailRoute) : detailRoute ? loadDetail(detailRoute) : loadList()
    const timeout = window.setTimeout(() => void load(), 0)
    const interval = window.setInterval(() => void load(), 30_000)
    return () => {
      window.clearTimeout(timeout)
      window.clearInterval(interval)
    }
  }, [detailRoute, loadDetail, loadList, loadPodDetail, podDetailRoute])

  useEffect(() => {
    if (podSection !== 'logs' || !logContainer) return
    const timeout = window.setTimeout(() => void loadPodLogs(), 0)
    return () => window.clearTimeout(timeout)
  }, [loadPodLogs, logContainer, podSection])

  useEffect(() => {
    if (workloadSection !== 'yaml' || !detailRoute || workloadYAML) return
    const timeout = window.setTimeout(() => void loadWorkloadYAML(), 0)
    return () => window.clearTimeout(timeout)
  }, [detailRoute, loadWorkloadYAML, workloadSection, workloadYAML])

  useEffect(() => {
    const handlePopState = () => {
      const nextRoute = routeFromPath(clusterId)
      const nextPodRoute = podRouteFromPath(clusterId)
      setDetailRoute(nextRoute)
      setPodDetailRoute(nextPodRoute)
      setWorkloadSection('details')
      setPodSection(new URLSearchParams(window.location.search).get('section') === 'logs' ? 'logs' : 'details')
      if (!nextRoute) setDetail(null)
      if (!nextPodRoute) setPodDetail(null)
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [clusterId])

  const namespaces = useMemo(() => [...new Set(
    (resourceView === 'controllers' ? workloads : pods).map((item) => item.namespace),
  )].sort(), [pods, resourceView, workloads])
  const visibleWorkloads = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return workloads.filter((workload) => (
      (kind === 'ALL' || workload.kind === kind)
      && (namespace === 'ALL' || workload.namespace === namespace)
      && (status === 'ALL' || (status === 'ATTENTION' ? workload.status !== 'healthy' : workload.status === status))
      && (!normalized || [workload.name, workload.namespace, workload.kind, ...workload.images].some((value) => value.toLowerCase().includes(normalized)))
    ))
  }, [kind, namespace, query, status, workloads])

  const visiblePods = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return pods.filter((pod) => (
      (namespace === 'ALL' || pod.namespace === namespace)
      && (status === 'ALL' || (status === 'ATTENTION' ? !isPodReady(pod) : pod.phase === status))
      && (!normalized || [pod.name, pod.namespace, pod.owner_kind, pod.owner_name, pod.node_name, ...pod.images]
        .some((value) => value.toLowerCase().includes(normalized)))
    ))
  }, [namespace, pods, query, status])

  const selectResourceView = (view: 'controllers' | 'pods') => {
    setResourceView(view)
    const nextURL = view === 'pods'
      ? `/clusters/${encodeURIComponent(clusterId)}/workloads?view=pods`
      : `/clusters/${encodeURIComponent(clusterId)}/workloads`
    window.history.replaceState({ resourceView: view }, '', nextURL)
    setQuery('')
    setNamespace('ALL')
    setKind('ALL')
    setStatus('ALL')
  }

  const openDetail = (workload: ClusterWorkload) => {
    const route = { kind: workload.kind, name: workload.name, namespace: workload.namespace }
    const suffix = [route.kind, route.namespace, route.name].map((value) => encodeURIComponent(value)).join('/')
    window.history.pushState({ workload: route }, '', `/clusters/${encodeURIComponent(clusterId)}/workloads/${suffix}`)
    setDetailRoute(route)
    setPodDetailRoute(null)
    setWorkloadSection('details')
    setWorkloadYAML(null)
    setYAMLError('')
  }

  const openPodDetail = (pod: { name: string; namespace: string }) => {
    const suffix = [pod.namespace, pod.name].map((value) => encodeURIComponent(value)).join('/')
    window.history.pushState({ pod }, '', `/clusters/${encodeURIComponent(clusterId)}/pods/${suffix}`)
    setPodDetailRoute({ name: pod.name, namespace: pod.namespace })
    setDetailRoute(null)
    setDetail(null)
    setPodSection('details')
    setPodLogs(null)
    setLogsError('')
  }

  const openOwnerDetail = (pod: ClusterPodDetail) => {
    if (!['Deployment', 'StatefulSet', 'DaemonSet'].includes(pod.owner_kind) || !pod.owner_name) return
    const route = { kind: pod.owner_kind, namespace: pod.namespace, name: pod.owner_name }
    const suffix = [route.kind, route.namespace, route.name].map((value) => encodeURIComponent(value)).join('/')
    window.history.pushState({ workload: route }, '', `/clusters/${encodeURIComponent(clusterId)}/workloads/${suffix}`)
    setDetailRoute(route)
    setPodDetailRoute(null)
    setPodDetail(null)
    setWorkloadSection('details')
    setWorkloadYAML(null)
    setYAMLError('')
  }

  const closeDetail = () => {
    window.history.pushState({ tab: 'workloads' }, '', `/clusters/${encodeURIComponent(clusterId)}/workloads`)
    setDetailRoute(null)
    setDetail(null)
    setWorkloadSection('details')
    setWorkloadYAML(null)
    setYAMLError('')
  }

  const copyWorkloadYAML = async () => {
    if (!workloadYAML) return
    try {
      await navigator.clipboard.writeText(workloadYAML.yaml)
      setYAMLCopied(true)
      window.setTimeout(() => setYAMLCopied(false), 2000)
    } catch {
      setYAMLError('浏览器未允许访问剪贴板，请手动选中 YAML 复制。')
    }
  }

  const closePodDetail = () => {
    window.history.pushState({ tab: 'workloads', view: 'pods' }, '', `/clusters/${encodeURIComponent(clusterId)}/workloads?view=pods`)
    setPodDetailRoute(null)
    setPodDetail(null)
    setResourceView('pods')
    setStatus('ALL')
    setPodSection('details')
    setPodLogs(null)
  }

  if (podDetailRoute) {
    return (
      <div className="ks-workload-detail">
        <div className="ks-workload-detail-toolbar">
          <Button isInline onClick={closePodDetail} variant="link">Pods</Button>
          <AngleRightIcon />
          <span>{podDetailRoute.name}</span>
          <Button icon={<SyncAltIcon />} isLoading={isLoading} onClick={() => void loadPodDetail(podDetailRoute)} variant="secondary">刷新</Button>
        </div>
        {error && <Alert actionLinks={<Button isInline onClick={() => void loadPodDetail(podDetailRoute)} variant="link">重新读取</Button>} isInline title="Pod 详情读取失败" variant="danger">{error}</Alert>}
        {isLoading && !podDetail ? <div className="ks-workloads-loading"><Spinner aria-label="正在读取 Pod 详情" /><span>正在读取 Pod 详情…</span></div> : podDetail && <>
          <Tabs activeKey={podSection} aria-label="Pod 详情功能" className="ks-pod-detail-tabs" onSelect={(_, key) => setPodSection(key as 'details' | 'logs' | 'terminal')}>
            <Tab eventKey="details" title={<TabTitleText>详情</TabTitleText>} />
            <Tab eventKey="logs" title={<TabTitleText>日志</TabTitleText>} />
            <Tab isDisabled={!canExec} eventKey="terminal" title={<TabTitleText>终端</TabTitleText>} />
          </Tabs>
          {podSection === 'details' ? <>
          <Card className="ks-workload-detail-card">
            <CardHeader>
              <CardTitle>
                <div className="ks-workload-detail-heading">
                  <div><span>Pod</span><Title headingLevel="h2" size="xl">{podDetail.name}</Title></div>
                  {podStatus(podDetail)}
                </div>
                <p>{isPodReady(podDetail) ? 'Pod 已就绪。' : `${podDetail.status_reason || podDetail.phase || '状态未知'} · 建议检查容器状态与 Owner 工作负载。`}</p>
              </CardTitle>
            </CardHeader>
            <CardBody>
              <Grid hasGutter>
                <GridItem lg={8} span={12}>
                  <DescriptionList isHorizontal>
                    <DescriptionListGroup><DescriptionListTerm>命名空间</DescriptionListTerm><DescriptionListDescription>{podDetail.namespace}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>Owner</DescriptionListTerm><DescriptionListDescription>{podDetail.owner_name ? <Button className="ks-workload-link" isDisabled={!['Deployment', 'StatefulSet', 'DaemonSet'].includes(podDetail.owner_kind)} isInline onClick={() => openOwnerDetail(podDetail)} variant="link">{podDetail.owner_kind} / {podDetail.owner_name}</Button> : '—（无控制器）'}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>节点</DescriptionListTerm><DescriptionListDescription>{podDetail.node_name || '—'}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>Pod IP / Host IP</DescriptionListTerm><DescriptionListDescription>{podDetail.pod_ip || '—'} / {podDetail.host_ip || '—'}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>Service Account</DescriptionListTerm><DescriptionListDescription>{podDetail.service_account || 'default'}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>QoS Class</DescriptionListTerm><DescriptionListDescription>{podDetail.qos_class || '—'}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>创建 / 启动时间</DescriptionListTerm><DescriptionListDescription>{formatTime(podDetail.created_at)} / {formatTime(podDetail.started_at)}</DescriptionListDescription></DescriptionListGroup>
                  </DescriptionList>
                </GridItem>
                <GridItem lg={4} span={12}>
                  <div className="ks-workload-labels"><strong>标签</strong><div>{Object.entries(podDetail.labels).length > 0 ? Object.entries(podDetail.labels).map(([key, value]) => <Label key={key} variant="outline">{key}={value}</Label>) : <span>没有标签</span>}</div></div>
                </GridItem>
              </Grid>
            </CardBody>
          </Card>
          <Card className="ks-workload-pods-card">
            <CardHeader><CardTitle><Title headingLevel="h2" size="lg">容器</Title><span>{podDetail.ready_containers} / {podDetail.total_containers} 个 Ready · 累计重启 {podDetail.restart_count} 次</span></CardTitle></CardHeader>
            <div className="ks-workloads-table-scroll">
              <Table aria-label="Pod 容器列表" gridBreakPoint="grid-lg" variant="compact">
                <Thead><Tr><Th width={25}>容器</Th><Th>状态</Th><Th>Ready</Th><Th>重启</Th><Th width={40}>镜像</Th></Tr></Thead>
                <Tbody>{podDetail.containers.map((container) => <Tr key={container.name}>
                  <Td dataLabel="容器"><strong>{container.name}</strong></Td>
                  <Td dataLabel="状态"><Label color={container.ready ? 'green' : 'orange'}>{container.status_reason || container.state}</Label></Td>
                  <Td dataLabel="Ready">{container.ready ? '是' : '否'}</Td><Td dataLabel="重启">{container.restart_count}</Td>
                  <Td dataLabel="镜像"><code className="ks-workload-image">{container.image}</code></Td>
                </Tr>)}</Tbody>
              </Table>
            </div>
          </Card>
          <Card className="ks-pod-conditions-card">
            <CardHeader><CardTitle><Title headingLevel="h2" size="lg">条件</Title></CardTitle></CardHeader>
            <div className="ks-workloads-table-scroll">
              <Table aria-label="Pod 条件列表" gridBreakPoint="grid-lg" variant="compact">
                <Thead><Tr><Th width={20}>类型</Th><Th>状态</Th><Th>原因</Th><Th width={40}>说明</Th><Th>最后变更</Th></Tr></Thead>
                <Tbody>{podDetail.conditions.map((condition) => <Tr key={condition.type}>
                  <Td dataLabel="类型"><strong>{condition.type}</strong></Td><Td dataLabel="状态">{condition.status}</Td>
                  <Td dataLabel="原因">{condition.reason || '—'}</Td><Td dataLabel="说明">{condition.message || '—'}</Td><Td dataLabel="最后变更">{formatTime(condition.last_transition_time)}</Td>
                </Tr>)}</Tbody>
              </Table>
            </div>
          </Card>
          </> : podSection === 'logs' ? <Card className="ks-pod-logs-card">
            <CardHeader><CardTitle><Title headingLevel="h2" size="lg">容器日志</Title></CardTitle></CardHeader>
            <div className="ks-pod-logs-toolbar">
              <FormSelect aria-label="选择日志容器" onChange={(_, value) => setLogContainer(value)} value={logContainer}>
                {podDetail.containers.map((container) => <FormSelectOption key={container.name} label={container.name} value={container.name} />)}
              </FormSelect>
              <FormSelect aria-label="选择日志时间范围" onChange={(_, value) => selectLogTimeRange(value)} value={logTimeRange}>
                <FormSelectOption label="最新日志" value="tail" />
                <FormSelectOption label="最近 15 分钟" value="15m" />
                <FormSelectOption label="最近 1 小时" value="1h" />
                <FormSelectOption label="最近 6 小时" value="6h" />
                <FormSelectOption label="自定义时间" value="custom" />
              </FormSelect>
              <FormSelect aria-label="选择日志行数" isDisabled={logTimeRange !== 'tail'} onChange={(_, value) => setLogTailLines(value)} value={logTailLines}>
                <FormSelectOption label="最近 100 行" value="100" /><FormSelectOption label="最近 200 行" value="200" /><FormSelectOption label="最近 500 行" value="500" /><FormSelectOption label="最近 1000 行" value="1000" />
              </FormSelect>
              <Checkbox aria-label="查看上一次容器日志" id="pod-logs-previous" isChecked={logPrevious} label="上一次容器" onChange={(_, checked) => setLogPrevious(checked)} />
              <Button icon={<SearchIcon />} isLoading={logsLoading} onClick={() => void loadPodLogs()} variant="secondary">查询日志</Button>
            </div>
            {logTimeRange === 'custom' && <div className="ks-pod-log-custom-range">
              <label htmlFor="pod-log-since">开始时间<TextInput aria-label="日志开始时间" id="pod-log-since" onChange={(_, value) => setCustomLogSince(value)} type="datetime-local" value={customLogSince} /></label>
              <span>至</span>
              <label htmlFor="pod-log-until">结束时间<TextInput aria-label="日志结束时间" id="pod-log-until" onChange={(_, value) => setCustomLogUntil(value)} type="datetime-local" value={customLogUntil} /></label>
              
            </div>}
            {logsError && <Alert actionLinks={<Button isInline onClick={() => void loadPodLogs()} variant="link">重新读取</Button>} isInline title="日志读取失败" variant="danger">{logsError}</Alert>}
            {podLogs?.truncated && <Alert isInline title="日志已达 1 MiB 读取上限" variant="warning">当前只显示本次请求范围内的日志，请减少行数或使用集中式日志系统。</Alert>}
            <div className="ks-pod-log-legend" aria-label="日志高亮图例">
              <span><i className="is-error" />错误 / 严重</span>
              <span><i className="is-warning" />警告</span>
              <span><i className="is-ip" />IP 地址</span>
              <span><i className="is-info" />信息</span>
              <span><i className="is-timestamp" />时间</span>
            </div>
            <div className="ks-pod-log-output" aria-label="Pod 日志内容">
              {logsLoading && !podLogs ? <div className="ks-workloads-loading"><Spinner aria-label="正在读取容器日志" /><span>正在读取容器日志…</span></div> : <HighlightedLog text={podLogs?.text || '当前容器没有可显示的日志。'} />}
            </div>
          </Card> : !canExec ? <Alert isInline variant="info" title="只读账号不能使用容器终端" /> : <Suspense fallback={<div className="ks-workloads-loading"><Spinner aria-label="正在加载终端" /><span>正在加载终端…</span></div>}><PodTerminal clusterId={clusterId} containers={podDetail.containers.map((container) => container.name)} namespace={podDetail.namespace} podName={podDetail.name} running={podDetail.phase === 'Running'} /></Suspense>}
        </>}
      </div>
    )
  }

  if (detailRoute) {
    return (
      <div className="ks-workload-detail">
        <div className="ks-workload-detail-toolbar">
          <Button isInline onClick={closeDetail} variant="link">工作负载</Button>
          <AngleRightIcon />
          <span>{detailRoute.name}</span>
          <Button icon={<SyncAltIcon />} isLoading={isLoading} onClick={() => void loadDetail(detailRoute)} variant="secondary">刷新</Button>
        </div>
        {error && <Alert actionLinks={<Button isInline onClick={() => void loadDetail(detailRoute)} variant="link">重新读取</Button>} isInline title="工作负载详情读取失败" variant="danger">{error}</Alert>}
        {isLoading && !detail ? <div className="ks-workloads-loading"><Spinner aria-label="正在读取工作负载详情" /><span>正在读取工作负载详情…</span></div> : detail && <>
          <Tabs activeKey={workloadSection} aria-label="工作负载详情功能" className="ks-pod-detail-tabs" onSelect={(_, key) => setWorkloadSection(key as 'details' | 'yaml')}>
            <Tab eventKey="details" title={<TabTitleText>详情</TabTitleText>} />
            <Tab eventKey="yaml" title={<TabTitleText>YAML</TabTitleText>} />
          </Tabs>
          {workloadSection === 'details' ? <>
          <Card className="ks-workload-detail-card">
            <CardHeader>
              <CardTitle>
                <div className="ks-workload-detail-heading">
                  <div><span>{workloadKind(detail.kind)}</span><Title headingLevel="h2" size="xl">{detail.name}</Title></div>
                  {workloadStatus(detail)}
                </div>
                <p>{detail.status_reason}</p>
              </CardTitle>
            </CardHeader>
            <CardBody>
              <Grid hasGutter>
                <GridItem lg={8} span={12}>
                  <DescriptionList isHorizontal>
                    <DescriptionListGroup><DescriptionListTerm>命名空间</DescriptionListTerm><DescriptionListDescription>{detail.namespace}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>副本</DescriptionListTerm><DescriptionListDescription>{detail.ready} Ready / {detail.desired} Desired / {detail.available} Available</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>镜像</DescriptionListTerm><DescriptionListDescription>{detail.images.map((image) => <code className="ks-workload-image" key={image}>{image}</code>)}</DescriptionListDescription></DescriptionListGroup>
                    <DescriptionListGroup><DescriptionListTerm>创建时间</DescriptionListTerm><DescriptionListDescription>{formatTime(detail.created_at)}</DescriptionListDescription></DescriptionListGroup>
                  </DescriptionList>
                </GridItem>
                <GridItem lg={4} span={12}>
                  <div className="ks-workload-labels">
                    <strong>标签</strong>
                    <div>{Object.entries(detail.labels).length > 0 ? Object.entries(detail.labels).map(([key, value]) => <Label key={key} variant="outline">{key}={value}</Label>) : <span>没有标签</span>}</div>
                  </div>
                </GridItem>
              </Grid>
            </CardBody>
          </Card>
          <Card className="ks-workload-pods-card">
            <CardHeader><CardTitle><Title headingLevel="h2" size="lg">实例</Title><span>{detail.pods_ready} / {detail.pods_total} 个 Pod Ready</span></CardTitle></CardHeader>
            <div className="ks-workloads-table-scroll">
              <Table aria-label="工作负载实例列表" gridBreakPoint="grid-lg" variant="compact">
                <Thead><Tr><Th width={25}>Pod</Th><Th>状态</Th><Th>容器</Th><Th>重启</Th><Th>节点</Th><Th>Pod IP</Th><Th>启动时间</Th></Tr></Thead>
                <Tbody>{detail.pods.map((pod) => <Tr isClickable key={pod.name} onRowClick={() => openPodDetail({ name: pod.name, namespace: detail.namespace })}>
                  <Td dataLabel="Pod"><Button className="ks-workload-link" isInline onClick={(event) => { event.stopPropagation(); openPodDetail({ name: pod.name, namespace: detail.namespace }) }} variant="link">{pod.name}</Button></Td><Td dataLabel="状态">{podStatus(pod)}</Td>
                  <Td dataLabel="容器">{pod.ready_containers} / {pod.total_containers}</Td><Td dataLabel="重启">{pod.restart_count}</Td>
                  <Td dataLabel="节点">{pod.node_name || '—'}</Td><Td dataLabel="Pod IP">{pod.pod_ip || '—'}</Td><Td dataLabel="启动时间">{formatTime(pod.started_at)}</Td>
                </Tr>)}</Tbody>
              </Table>
              {detail.pods.length === 0 && <div className="ks-workloads-empty"><CubesIcon /><strong>没有发现所属 Pod</strong><span>控制器当前没有已创建的实例。</span></div>}
            </div>
          </Card>
          </> : <Card className="ks-workload-yaml-card">
            <CardHeader>
              <CardTitle><Title headingLevel="h2" size="lg">实时 YAML</Title></CardTitle>
              <Button isDisabled={!workloadYAML} onClick={() => void copyWorkloadYAML()} variant="secondary">{yamlCopied ? '已复制' : '复制 YAML'}</Button>
            </CardHeader>
            {yamlError && <Alert actionLinks={<Button isInline onClick={() => void loadWorkloadYAML()} variant="link">重新读取</Button>} isInline title="YAML 读取失败" variant="danger">{yamlError}</Alert>}
            {yamlLoading && !workloadYAML ? <div className="ks-workloads-loading"><Spinner aria-label="正在读取 YAML" /><span>正在读取 YAML…</span></div> : <pre className="ks-workload-yaml" tabIndex={0}><code>{workloadYAML?.yaml || ''}</code></pre>}
          </Card>}
        </>}
      </div>
    )
  }

  const summaries = resourceView === 'controllers' ? [
    { label: '控制器', status: 'brand', value: workloads.length },
    { label: '正常', status: 'healthy', value: workloads.filter((workload) => workload.status === 'healthy').length },
    { label: '进行中', status: 'progressing', value: workloads.filter((workload) => workload.status === 'progressing').length },
    { label: '异常', status: 'degraded', value: workloads.filter((workload) => workload.status === 'degraded').length },
  ] : [
    { label: 'Pods', status: 'brand', value: pods.length },
    { label: 'Ready', status: 'healthy', value: pods.filter(isPodReady).length },
    { label: '未就绪', status: 'progressing', value: pods.filter((pod) => !isPodReady(pod) && pod.phase !== 'Failed').length },
    { label: '失败', status: 'degraded', value: pods.filter((pod) => pod.phase === 'Failed').length },
  ]

  const hasFilters = query !== '' || namespace !== 'ALL' || kind !== 'ALL' || status !== 'ALL'
  const clearFilters = () => {
    setQuery('')
    setNamespace('ALL')
    setKind('ALL')
    setStatus('ALL')
    window.history.replaceState({ tab: 'workloads' }, '', `/clusters/${encodeURIComponent(clusterId)}/workloads${resourceView === 'pods' ? '?view=pods' : ''}`)
  }

  return (
    <div className="ks-workloads-page">
      {status === 'ATTENTION' && (
        <Alert
          actionLinks={<Button isInline onClick={clearFilters} variant="link">显示全部</Button>}
          isInline
          title={resourceView === 'controllers' ? '正在查看受影响工作负载' : '正在查看未就绪 Pod'}
          variant="warning"
        >
          {resourceView === 'controllers'
            ? '仅显示副本未就绪、发布未完成或状态异常的控制器，点击名称可继续查看所属 Pod。'
            : `仅显示${namespace === 'ALL' ? '' : ` ${namespace} 命名空间内`}未就绪或失败的 Pod。`}
        </Alert>
      )}
      <Grid className="ks-workloads-summary" hasGutter>{summaries.map((summary) => <GridItem key={summary.label} lg={3} md={6} span={12}>
        <Card className={`ks-workloads-summary-card is-${summary.status}`} isFullHeight><CardBody><span>{summary.label}</span><strong>{summary.value}</strong></CardBody></Card>
      </GridItem>)}</Grid>
      <Card className="ks-workloads-card">
        <CardHeader><CardTitle><Title headingLevel="h2" size="lg">工作负载</Title></CardTitle></CardHeader>
        <Tabs activeKey={resourceView} aria-label="工作负载资源类型" className="ks-workloads-resource-tabs" onSelect={(_, key) => selectResourceView(key as 'controllers' | 'pods')}>
          <Tab eventKey="controllers" title={<TabTitleText>控制器 ({workloads.length})</TabTitleText>} />
          <Tab eventKey="pods" title={<TabTitleText>Pods ({pods.length})</TabTitleText>} />
        </Tabs>
        <div className="ks-workloads-toolbar">
          <SearchInput aria-label={resourceView === 'controllers' ? '搜索工作负载' : '搜索 Pod'} onChange={(_, value) => setQuery(value)} onClear={() => setQuery('')} placeholder={resourceView === 'controllers' ? '按名称或镜像搜索' : '按名称、Owner、节点或镜像搜索'} value={query} />
          <FormSelect aria-label="筛选命名空间" onChange={(_, value) => setNamespace(value)} value={namespace}><FormSelectOption label="全部命名空间" value="ALL" />{namespaces.map((value) => <FormSelectOption key={value} label={value} value={value} />)}</FormSelect>
          {resourceView === 'controllers' && <FormSelect aria-label="筛选类型" onChange={(_, value) => setKind(value)} value={kind}><FormSelectOption label="全部类型" value="ALL" /><FormSelectOption label="Deployment" value="Deployment" /><FormSelectOption label="StatefulSet" value="StatefulSet" /><FormSelectOption label="DaemonSet" value="DaemonSet" /></FormSelect>}
          <FormSelect aria-label="筛选状态" onChange={(_, value) => setStatus(value)} value={status}>{resourceView === 'controllers' ? <><FormSelectOption label="全部状态" value="ALL" /><FormSelectOption label="需要关注" value="ATTENTION" /><FormSelectOption label="正常" value="healthy" /><FormSelectOption label="进行中" value="progressing" /><FormSelectOption label="异常" value="degraded" /></> : <><FormSelectOption label="全部状态" value="ALL" /><FormSelectOption label="未就绪（建议关注）" value="ATTENTION" /><FormSelectOption label="Running" value="Running" /><FormSelectOption label="Pending" value="Pending" /><FormSelectOption label="Failed" value="Failed" /><FormSelectOption label="Succeeded" value="Succeeded" /></>}</FormSelect>
          <Button aria-label="刷新工作负载" icon={<SyncAltIcon />} isLoading={isLoading} onClick={() => void loadList()} variant="plain" />
        </div>
        {error && <Alert actionLinks={<Button isInline onClick={() => void loadList()} variant="link">重新读取</Button>} isInline title="工作负载读取失败" variant="danger">{error}</Alert>}
        {isLoading && workloads.length === 0 && pods.length === 0 ? <div className="ks-workloads-loading"><Spinner aria-label="正在读取工作负载" /><span>正在读取工作负载…</span></div> : <div className="ks-workloads-table-scroll">
          {resourceView === 'controllers' ? <>
          <Table aria-label="工作负载列表" gridBreakPoint="grid-lg" variant="compact">
            <Thead><Tr><Th width={25}>名称</Th><Th>命名空间</Th><Th>类型</Th><Th>状态</Th><Th>副本</Th><Th>Pod</Th><Th>重启</Th><Th width={20}>镜像</Th><Th>创建时间</Th><Th screenReaderText="操作" /></Tr></Thead>
            <Tbody>{visibleWorkloads.map((workload) => <Tr isClickable key={`${workload.kind}/${workload.namespace}/${workload.name}`} onRowClick={() => openDetail(workload)}>
              <Td dataLabel="名称"><Button className="ks-workload-link" isInline onClick={(event) => { event.stopPropagation(); openDetail(workload) }} variant="link">{workload.name}</Button><small>{workload.status_reason}</small></Td>
              <Td dataLabel="命名空间">{workload.namespace}</Td><Td dataLabel="类型">{workloadKind(workload.kind)}</Td><Td dataLabel="状态">{workloadStatus(workload)}</Td>
              <Td dataLabel="副本">{workload.ready} / {workload.desired}</Td><Td dataLabel="Pod">{workload.pods_ready} / {workload.pods_total}</Td><Td dataLabel="重启">{workload.restart_count}</Td>
              <Td dataLabel="镜像"><span className="ks-workload-image-cell" title={workload.images.join(', ')}>{workload.images.join(', ') || '—'}</span></Td><Td dataLabel="创建时间">{formatAge(workload.created_at)}</Td>
              <Td><Button aria-label={`查看 ${workload.name}`} icon={<AngleRightIcon />} onClick={(event) => { event.stopPropagation(); openDetail(workload) }} variant="plain" /></Td>
            </Tr>)}</Tbody>
          </Table>
          {visibleWorkloads.length === 0 && !error && <div className="ks-workloads-empty">{hasFilters ? <SearchIcon /> : <CubesIcon />}<strong>{hasFilters ? '没有匹配的工作负载' : '当前集群没有控制器'}</strong><span>{hasFilters ? '请调整搜索或筛选条件。' : 'Kubernetes API 没有返回 Deployment、StatefulSet 或 DaemonSet。'}</span>{hasFilters && <Button isInline onClick={clearFilters} variant="link">清除筛选</Button>}</div>}
          </> : <>
            <Table aria-label="Pod 列表" gridBreakPoint="grid-lg" variant="compact">
              <Thead><Tr><Th width={25}>名称</Th><Th>命名空间</Th><Th>状态</Th><Th>容器</Th><Th>重启</Th><Th width={20}>Owner</Th><Th>节点</Th><Th>Pod IP</Th><Th>存活时间</Th><Th screenReaderText="操作" /></Tr></Thead>
              <Tbody>{visiblePods.map((pod) => <Tr isClickable key={`${pod.namespace}/${pod.name}`} onRowClick={() => openPodDetail(pod)}>
                <Td dataLabel="名称"><Button className="ks-workload-link" isInline onClick={(event) => { event.stopPropagation(); openPodDetail(pod) }} variant="link">{pod.name}</Button><small>{pod.status_reason}</small></Td>
                <Td dataLabel="命名空间">{pod.namespace}</Td><Td dataLabel="状态">{podStatus(pod)}</Td>
                <Td dataLabel="容器">{pod.ready_containers} / {pod.total_containers}</Td><Td dataLabel="重启">{pod.restart_count}</Td>
                <Td dataLabel="Owner">{pod.owner_name ? <><strong>{pod.owner_name}</strong><small>{pod.owner_kind}</small></> : '—'}</Td>
                <Td dataLabel="节点">{pod.node_name || '—'}</Td><Td dataLabel="Pod IP">{pod.pod_ip || '—'}</Td><Td dataLabel="存活时间">{formatAge(pod.created_at)}</Td>
                <Td><Button aria-label={`查看 ${pod.name}`} icon={<AngleRightIcon />} onClick={(event) => { event.stopPropagation(); openPodDetail(pod) }} variant="plain" /></Td>
              </Tr>)}</Tbody>
            </Table>
            {visiblePods.length === 0 && !error && <div className="ks-workloads-empty">{hasFilters ? <SearchIcon /> : <CubesIcon />}<strong>{hasFilters ? '没有匹配的 Pod' : '当前集群没有 Pod'}</strong><span>{hasFilters ? '请调整搜索或筛选条件。' : 'Kubernetes API 没有返回 Pod。'}</span>{hasFilters && <Button isInline onClick={clearFilters} variant="link">清除筛选</Button>}</div>}
          </>}
        </div>}
      </Card>
    </div>
  )
}
