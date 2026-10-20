import ClusterKubectl from '../components/ClusterKubectl'
import { usePermission } from '../auth/context'
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Grid,
  GridItem,
  Label,
  FormSelect,
  FormSelectOption,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  Progress,
  SearchInput,
  Spinner,
  Tab,
  Tabs,
  TabTitleText,
  Title,
} from '@patternfly/react-core'
import {
  AngleRightIcon,
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  SearchIcon,
  SyncAltIcon,
} from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useMemo, useState } from 'react'

import {
  deleteManagedCluster,
  fetchCluster,
  fetchClusterNodes,
  updateClusterClassification,
  type ClusterNode,
  type ManagedCluster,
} from '../api/clusters'
import './ClusterOverviewPage.css'
import ClusterWorkloadsPage from './ClusterWorkloadsPage'
import ClusterEventsPage from './ClusterEventsPage'
import ClusterCapacityPage from './ClusterCapacityPage'

type ClusterHealth = 'healthy' | 'warning' | 'unreachable'
type DetailTab = 'overview' | 'capacity' | 'nodes' | 'workloads' | 'events' | 'terminal'

const healthMeta = {
  healthy: { color: 'green' as const, label: '健康' },
  warning: { color: 'orange' as const, label: '需要关注' },
  unreachable: { color: 'red' as const, label: '连接失败' },
}

function clusterHealth(cluster: ManagedCluster): ClusterHealth {
  if (cluster.connection_status !== 'connected') return 'unreachable'
  const resources = [
    [cluster.nodes_ready, cluster.nodes_total],
    [cluster.instances_ready, cluster.instances_total],
    [cluster.core_components_ready, cluster.core_components_total],
  ]
  return resources.some(([ready, total]) => ready !== null && total !== null && ready < total) ? 'warning' : 'healthy'
}

function ratio(ready: number | null, total: number | null) {
  return ready === null || total === null ? '采集不可用' : `${ready} / ${total}`
}

function percent(value: number | null) {
  return value === null ? 0 : Math.min(100, Math.max(0, value))
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function detailTabFromPath(): DetailTab {
  if (window.location.pathname.endsWith('/terminal')) return 'terminal'
  if (window.location.pathname.includes('/workloads') || window.location.pathname.includes('/pods/')) return 'workloads'
  if (window.location.pathname.endsWith('/events')) return 'events'
  if (window.location.pathname.endsWith('/capacity')) return 'capacity'
  return window.location.pathname.endsWith('/nodes') ? 'nodes' : 'overview'
}

function formatMemory(bytes: number) {
  if (!bytes) return '—'
  const gibibytes = bytes / (1024 ** 3)
  return `${gibibytes >= 10 ? gibibytes.toFixed(0) : gibibytes.toFixed(1)} GiB`
}

function formatCPU(cores: number) {
  if (!cores) return '—'
  return Number.isInteger(cores) ? String(cores) : cores.toFixed(1)
}

function nodeStatusLabel(node: ClusterNode) {
  if (node.status === 'Ready' && node.schedulable) return <Label color="green">Ready</Label>
  if (node.status === 'Ready') return <Label color="orange">Ready · 不可调度</Label>
  if (node.status === 'NotReady') return <Label color="red">NotReady</Label>
  return <Label color="grey">Unknown</Label>
}

type Props = {
  clusterId: string
  onBack: () => void
  onRemoved: (name: string) => void
}

export default function ClusterOverviewPage({ clusterId, onBack, onRemoved }: Props) {
 const canConfigure = usePermission('configure')

  const [activeTab, setActiveTab] = useState<DetailTab>(() => detailTabFromPath())
  const [cluster, setCluster] = useState<ManagedCluster | null>(null)
  const [deleteError, setDeleteError] = useState('')
  const [error, setError] = useState('')
  const [isDeleteOpen, setIsDeleteOpen] = useState(false)
  const [isDeleting, setIsDeleting] = useState(false)
  const [isClassificationOpen, setIsClassificationOpen] = useState(false)
  const [isClassificationSaving, setIsClassificationSaving] = useState(false)
  const [classificationError, setClassificationError] = useState('')
  const [classificationType, setClassificationType] = useState('')
  const [classificationStack, setClassificationStack] = useState('')
  const [isLoading, setIsLoading] = useState(true)
  const [isNodesLoading, setIsNodesLoading] = useState(false)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [nodesError, setNodesError] = useState('')
  const [nodesQuery, setNodesQuery] = useState('')

  const load = useCallback(async () => {
    setIsLoading(true)
    setError('')
    try {
      setCluster(await fetchCluster(clusterId))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '读取集群详情失败，请稍后重试。')
    } finally {
      setIsLoading(false)
    }
  }, [clusterId])

  const loadNodes = useCallback(async () => {
    setIsNodesLoading(true)
    setNodesError('')
    try {
      setNodes(await fetchClusterNodes(clusterId))
    } catch (requestError) {
      setNodesError(requestError instanceof Error ? requestError.message : '读取节点列表失败，请稍后重试。')
    } finally {
      setIsNodesLoading(false)
    }
  }, [clusterId])

  useEffect(() => {
    const timeout = window.setTimeout(() => void load(), 0)
    const interval = window.setInterval(() => void load(), 30_000)
    return () => {
      window.clearTimeout(timeout)
      window.clearInterval(interval)
    }
  }, [load])

  useEffect(() => {
    const handlePopState = () => setActiveTab(detailTabFromPath())
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useEffect(() => {
    if (activeTab !== 'nodes') return
    const timeout = window.setTimeout(() => void loadNodes(), 0)
    const interval = window.setInterval(() => void loadNodes(), 30_000)
    return () => {
      window.clearTimeout(timeout)
      window.clearInterval(interval)
    }
  }, [activeTab, loadNodes])

  const health = useMemo(() => cluster ? clusterHealth(cluster) : 'healthy', [cluster])
  const issues = useMemo(() => {
    if (!cluster) return []
    const values: Array<{ action?: string; detail: string; target?: DetailTab | 'workloads_attention' | 'system_pods'; title: string }> = []
    if (cluster.connection_status !== 'connected') values.push({
      detail: '请检查 API Server 网络、证书与纳管凭据。',
      title: 'Kubernetes API Server 无法连接',
    })
    if (cluster.nodes_ready !== null && cluster.nodes_total !== null && cluster.nodes_ready < cluster.nodes_total) {
      values.push({ action: '查看节点', detail: '优先检查节点 Ready 条件、网络与 kubelet 状态。', target: 'nodes', title: `${cluster.nodes_total - cluster.nodes_ready} 个节点未就绪` })
    }
    if (cluster.instances_ready !== null && cluster.instances_total !== null && cluster.instances_ready < cluster.instances_total) {
      values.push({ action: '查看受影响工作负载', detail: '已按副本未就绪、发布未完成或异常状态筛选，可继续下钻到 Pod。', target: 'workloads_attention', title: `${cluster.instances_total - cluster.instances_ready} 个实例未就绪` })
    }
    if (cluster.core_components_ready !== null && cluster.core_components_total !== null && cluster.core_components_ready < cluster.core_components_total) {
      values.push({ action: '查看系统 Pod', detail: '已筛选 kube-system 命名空间内未就绪的 Pod。', target: 'system_pods', title: `${cluster.core_components_total - cluster.core_components_ready} 个核心组件异常` })
    }
    if (!cluster.metrics_available) values.push({ detail: '请确认 Prometheus Service 可访问且查询指标存在。', title: 'CPU 与内存指标不可用' })
    return values
  }, [cluster])

  const remove = async () => {
    if (!cluster || isDeleting) return
    setDeleteError('')
    setIsDeleting(true)
    try {
      await deleteManagedCluster(cluster.id)
      onRemoved(cluster.name)
    } catch (requestError) {
      setDeleteError(requestError instanceof Error ? requestError.message : '解除集群纳管失败，请稍后重试。')
      setIsDeleting(false)
    }
  }

  const openClassification = () => {
    const currentType = cluster?.cluster_type || (cluster?.technology_stack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(cluster?.technology_stack ?? '') ? 'G01' : '')
    setClassificationType(currentType)
    setClassificationStack(cluster?.technology_stack === '未标记' ? '' : cluster?.technology_stack ?? '')
    setClassificationError('')
    setIsClassificationOpen(true)
  }

  const saveClassification = async () => {
    if (!cluster) return
    setIsClassificationSaving(true)
    setClassificationError('')
    try {
      setCluster(await updateClusterClassification(cluster.id, classificationType, classificationStack))
      setIsClassificationOpen(false)
    } catch (requestError) {
      setClassificationError(requestError instanceof Error ? requestError.message : '保存集群分类失败。')
    } finally {
      setIsClassificationSaving(false)
    }
  }

  const openTab = (tab: DetailTab) => {
    if (tab === activeTab) return
    window.history.pushState({ clusterID: clusterId, tab }, '', `/clusters/${encodeURIComponent(clusterId)}/${tab}`)
    setActiveTab(tab)
  }

  const openIssueTarget = (target: DetailTab | 'workloads_attention' | 'system_pods') => {
    const tab: DetailTab = target === 'workloads_attention' || target === 'system_pods' ? 'workloads' : target
    const query = target === 'workloads_attention'
      ? '?attention=1'
      : target === 'system_pods'
        ? '?view=pods&attention=1&namespace=kube-system'
        : ''
    window.history.pushState({ clusterID: clusterId, tab }, '', `/clusters/${encodeURIComponent(clusterId)}/${tab}${query}`)
    setActiveTab(tab)
  }

  const visibleNodes = useMemo(() => {
    const normalized = nodesQuery.trim().toLowerCase()
    if (!normalized) return nodes
    return nodes.filter((node) => [
      node.name,
      node.status,
      node.roles.join(' '),
      node.region,
      node.zone,
      node.instance_type,
      node.kubernetes_version,
    ].some((value) => value.toLowerCase().includes(normalized)))
  }, [nodes, nodesQuery])

  if (isLoading && !cluster) {
    return <PageSection className="ks-cluster-detail-loading"><Spinner aria-label="正在加载集群详情" size="xl" /><span>正在读取集群健康状态…</span></PageSection>
  }

  if (!cluster) {
    return (
      <PageSection>
        <Alert actionLinks={<Button isInline onClick={onBack} variant="link">返回集群列表</Button>} title="无法打开集群概览" variant="danger">
          {error || '该集群不存在或已经解除纳管。'}
        </Alert>
      </PageSection>
    )
  }

  const status = healthMeta[health]
  const healthCards = [
    { detail: cluster.api_latency_ms === null ? '延迟不可用' : `${cluster.api_latency_ms} ms`, label: 'API Server', value: cluster.connection_status === 'connected' ? '已连接' : '连接失败' },
    { detail: 'Ready', label: '节点', value: ratio(cluster.nodes_ready, cluster.nodes_total) },
    { detail: 'Ready', label: '实例', value: ratio(cluster.instances_ready, cluster.instances_total), warning: cluster.instances_ready !== null && cluster.instances_total !== null && cluster.instances_ready < cluster.instances_total },
    { detail: '正常', label: '核心组件', value: ratio(cluster.core_components_ready, cluster.core_components_total) },
  ]

  return (
    <>
      <PageSection className="ks-cluster-detail-header" variant="secondary">
        <nav aria-label="面包屑" className="ks-detail-breadcrumb">
          <Button isInline onClick={onBack} variant="link">集群</Button><AngleRightIcon /><span>{cluster.name}</span>
        </nav>
        <div className="ks-cluster-detail-title-row">
          <div>
            <div className="ks-cluster-detail-heading">
              <Title headingLevel="h1" size="2xl">{cluster.name}</Title>
              <Label color={status.color} icon={health === 'healthy' ? <CheckCircleIcon /> : health === 'warning' ? <ExclamationTriangleIcon /> : <ExclamationCircleIcon />}>{status.label}</Label>
              <Label color="blue">{cluster.cluster_type || (cluster.technology_stack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(cluster.technology_stack) ? 'G01' : '未标记')}</Label>
              <Label color="purple">{cluster.technology_stack || '未标记'}</Label>
              <Label variant="outline">{cluster.environment}</Label>
            </div>
            <p>最近检查：{formatTime(cluster.last_checked_at)} · 每 30 秒自动刷新</p>
          </div>
          <div className="ks-cluster-detail-actions">
            <Button
              icon={<SyncAltIcon />}
              isLoading={activeTab === 'nodes' ? isNodesLoading : isLoading}
              onClick={() => void (activeTab === 'nodes' ? loadNodes() : load())}
              variant="secondary"
            >
              刷新状态
            </Button>
            <Button isDisabled={!canConfigure} onClick={openClassification} variant="secondary">编辑分类</Button>
            <Button isDisabled={!canConfigure} onClick={() => { setDeleteError(''); setIsDeleteOpen(true) }} variant="danger">移除集群</Button>
          </div>
        </div>
        <Tabs
          activeKey={activeTab}
          aria-label="集群详情"
          className="ks-cluster-detail-tabs"
          onSelect={(_, eventKey) => openTab(eventKey as DetailTab)}
        >
          <Tab eventKey="overview" title={<TabTitleText>概览</TabTitleText>} />
          <Tab eventKey="capacity" title={<TabTitleText>容量</TabTitleText>} />
          <Tab eventKey="nodes" title={<TabTitleText>节点</TabTitleText>} />
          <Tab eventKey="workloads" title={<TabTitleText>工作负载</TabTitleText>} />
          <Tab eventKey="events" title={<TabTitleText>事件</TabTitleText>} />
          <Tab eventKey="terminal" isDisabled={!canConfigure} title={<TabTitleText>终端</TabTitleText>} />
        </Tabs>
      </PageSection>

      <PageSection className="ks-cluster-detail-content">
        {error && <Alert isInline title="刷新失败，当前显示上一次成功数据" variant="warning">{error}</Alert>}
        {health === 'unreachable' && <Alert isInline title="集群连接失败" variant="danger">请检查 API Server 网络、证书和访问凭据。</Alert>}

        {activeTab === 'overview' ? <>
        <Grid className="ks-cluster-health-grid" hasGutter>
          {healthCards.map((card) => (
            <GridItem key={card.label} lg={3} md={6} span={12}>
              <Card className={`ks-cluster-health-card${card.warning ? ' is-warning' : ''}`} isFullHeight>
                <CardBody><span>{card.label}</span><strong>{card.value}</strong><small>{card.detail}</small></CardBody>
              </Card>
            </GridItem>
          ))}
        </Grid>

        <Grid className="ks-cluster-detail-grid" hasGutter>
          <GridItem lg={6} span={12}>
            <Card isFullHeight>
              <CardHeader><CardTitle><Title headingLevel="h2" size="lg">资源使用</Title></CardTitle></CardHeader>
              <CardBody className="ks-cluster-resource-usage">
                <div><span>CPU 使用率</span><strong>{cluster.cpu_utilization === null ? '不可用' : `${cluster.cpu_utilization.toFixed(1)}%`}</strong></div>
                <Progress aria-label="CPU 使用率" value={percent(cluster.cpu_utilization)} />
                <div><span>内存使用率</span><strong>{cluster.memory_utilization === null ? '不可用' : `${cluster.memory_utilization.toFixed(1)}%`}</strong></div>
                <Progress aria-label="内存使用率" value={percent(cluster.memory_utilization)} />
                {!cluster.metrics_available && <p>当前未从集群 Prometheus 获取到 CPU 与内存指标。</p>}
                <div className="ks-capacity-summary"><span><strong>{cluster.worker_nodes ?? '—'}</strong>Worker 节点</span><span><strong>{cluster.worker_cores ?? '—'}</strong>Worker 核数</span></div>
              </CardBody>
            </Card>
          </GridItem>
          <GridItem lg={6} span={12}>
            <Card isFullHeight>
              <CardHeader><CardTitle><Title headingLevel="h2" size="lg">需要关注</Title></CardTitle></CardHeader>
              <CardBody>
                {issues.length === 0 ? <div className="ks-no-issues"><CheckCircleIcon /><strong>当前未发现健康异常</strong><span>节点、实例和核心组件均处于就绪状态。</span></div> : (
                  <ul className="ks-cluster-issue-list">{issues.map((issue) => <li key={issue.title}>
                    <ExclamationTriangleIcon />
                    <div><strong>{issue.title}</strong><span>{issue.detail}</span>{issue.action && issue.target && <Button isInline onClick={() => openIssueTarget(issue.target!)} variant="link">{issue.action}<AngleRightIcon /></Button>}</div>
                  </li>)}</ul>
                )}
              </CardBody>
            </Card>
          </GridItem>
          <GridItem span={12}>
            <Card>
              <CardHeader><CardTitle><Title headingLevel="h2" size="lg">基本信息</Title></CardTitle></CardHeader>
              <CardBody>
                <DescriptionList className="ks-cluster-basic-info" isHorizontal>
                  <DescriptionListGroup><DescriptionListTerm>API Server</DescriptionListTerm><DescriptionListDescription><code>{cluster.api_server}</code></DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>Kubernetes 版本</DescriptionListTerm><DescriptionListDescription>{cluster.kubernetes_version}</DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>技术栈</DescriptionListTerm><DescriptionListDescription>{cluster.technology_stack || '未标记'}</DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>集群类型</DescriptionListTerm><DescriptionListDescription>{cluster.cluster_type || (cluster.technology_stack === 'OCP' ? 'OCP' : ['TCS', 'CMBK8S'].includes(cluster.technology_stack) ? 'G01' : '未标记')}</DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>默认 Context</DescriptionListTerm><DescriptionListDescription>{cluster.context_name}</DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>认证方式</DescriptionListTerm><DescriptionListDescription>{cluster.authentication_type}</DescriptionListDescription></DescriptionListGroup>
                  <DescriptionListGroup><DescriptionListTerm>纳管时间</DescriptionListTerm><DescriptionListDescription>{formatTime(cluster.created_at)}</DescriptionListDescription></DescriptionListGroup>
                </DescriptionList>
              </CardBody>
            </Card>
          </GridItem>
        </Grid>
        </> : activeTab === 'capacity' ? <ClusterCapacityPage clusterId={clusterId} /> : activeTab === 'nodes' ? (
          <Card className="ks-cluster-nodes-card">
            <CardHeader>
              <CardTitle>
                <Title headingLevel="h2" size="lg">节点</Title>
                <span className="ks-cluster-nodes-subtitle">
                  {nodes.length} 个节点 · {nodes.filter((node) => node.status === 'Ready').length} 个 Ready
                </span>
              </CardTitle>
            </CardHeader>
            <div className="ks-cluster-nodes-toolbar">
              <SearchInput
                aria-label="搜索节点"
                onChange={(_, value) => setNodesQuery(value)}
                onClear={() => setNodesQuery('')}
                placeholder="按名称、角色、区域或版本搜索"
                value={nodesQuery}
              />
              <span>每 30 秒自动刷新</span>
            </div>
            {nodesError && (
              <Alert
                actionLinks={<Button isInline onClick={() => void loadNodes()} variant="link">重新读取</Button>}
                isInline
                title="节点数据读取失败"
                variant="danger"
              >
                {nodesError}
              </Alert>
            )}
            {isNodesLoading && nodes.length === 0 ? (
              <div className="ks-cluster-nodes-loading"><Spinner aria-label="正在读取节点" /><span>正在读取节点…</span></div>
            ) : (
              <div className="ks-cluster-nodes-table-scroll">
                <Table aria-label="节点列表" gridBreakPoint="grid-lg" variant="compact">
                  <Thead>
                    <Tr>
                      <Th width={20}>名称</Th>
                      <Th>状态</Th>
                      <Th>角色</Th>
                      <Th>区域</Th>
                      <Th>可用区</Th>
                      <Th>实例类型</Th>
                      <Th>CPU</Th>
                      <Th>内存</Th>
                      <Th>Kubernetes版本</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {visibleNodes.map((node) => (
                      <Tr key={node.name}>
                        <Td dataLabel="名称"><strong>{node.name}</strong></Td>
                        <Td dataLabel="状态">{nodeStatusLabel(node)}</Td>
                        <Td dataLabel="角色">{node.roles.join(', ') || '—'}</Td>
                        <Td dataLabel="区域">{node.region || '—'}</Td>
                        <Td dataLabel="可用区">{node.zone || '—'}</Td>
                        <Td dataLabel="实例类型">{node.instance_type || '—'}</Td>
                        <Td dataLabel="CPU">{formatCPU(node.cpu_cores)} 核</Td>
                        <Td dataLabel="内存">{formatMemory(node.memory_bytes)}</Td>
                        <Td dataLabel="Kubernetes版本">{node.kubernetes_version || '—'}</Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
                {visibleNodes.length === 0 && !nodesError && (
                  <div className="ks-cluster-nodes-empty">
                    <SearchIcon />
                    <strong>{nodes.length === 0 ? '没有发现节点' : '没有匹配的节点'}</strong>
                    <span>{nodes.length === 0 ? 'Kubernetes API 没有返回节点。' : '请调整搜索关键词。'}</span>
                  </div>
                )}
              </div>
            )}
          </Card>
        ) : activeTab === 'workloads' ? <ClusterWorkloadsPage clusterId={clusterId} /> : activeTab === 'terminal' ? <ClusterKubectl key={clusterId} clusterId={clusterId} clusterName={cluster.name} /> : <ClusterEventsPage clusterId={clusterId} />}
      </PageSection>

      <Modal aria-labelledby="remove-cluster-title" isOpen={isDeleteOpen} onClose={() => { if (!isDeleting) setIsDeleteOpen(false) }} variant="small">
        <ModalHeader description="此操作只会解除平台纳管，不会删除 Kubernetes 集群或其中的任何业务资源。" labelId="remove-cluster-title" title="确认移除集群" titleIconVariant="warning" />
        <ModalBody>
          {deleteError && <Alert isInline title="移除失败" variant="danger">{deleteError}</Alert>}
          <p>即将移除集群 <strong>{cluster.name}</strong>，平台保存的加密连接凭据也会一并删除。</p>
        </ModalBody>
        <ModalFooter>
          <Button isLoading={isDeleting} onClick={() => void remove()} variant="danger">确认移除</Button>
          <Button isDisabled={isDeleting} onClick={() => setIsDeleteOpen(false)} variant="link">取消</Button>
        </ModalFooter>
      </Modal>
      <Modal aria-labelledby="cluster-classification-title" isOpen={isClassificationOpen} onClose={() => { if (!isClassificationSaving) setIsClassificationOpen(false) }} variant="small">
        <ModalHeader description="分类决定该集群进入哪个容量资源池。" labelId="cluster-classification-title" title="编辑集群分类" />
        <ModalBody className="ks-cluster-classification-form">
          <label htmlFor="detail-cluster-type">集群类型</label>
          <FormSelect aria-label="详情集群类型" id="detail-cluster-type" onChange={(_, value) => { setClassificationType(value); setClassificationStack(value === 'OCP' ? 'OCP' : '') }} value={classificationType}>
            <FormSelectOption isPlaceholder label="请选择集群类型" value="" />
            <FormSelectOption label="G01" value="G01" />
            <FormSelectOption label="OCP" value="OCP" />
          </FormSelect>
          <label htmlFor="detail-technology-stack">技术栈</label>
          <FormSelect aria-label="详情技术栈" id="detail-technology-stack" isDisabled={!classificationType || classificationType === 'OCP'} onChange={(_, value) => setClassificationStack(value)} value={classificationStack}>
            <FormSelectOption isPlaceholder label={classificationType ? '请选择技术栈' : '请先选择集群类型'} value="" />
            {classificationType === 'G01' && <FormSelectOption label="TCS" value="TCS" />}
            {classificationType === 'G01' && <FormSelectOption label="CMBK8S" value="CMBK8S" />}
            {classificationType === 'OCP' && <FormSelectOption label="OCP" value="OCP" />}
          </FormSelect>
          {classificationError && <Alert isInline title="保存失败" variant="danger">{classificationError}</Alert>}
        </ModalBody>
        <ModalFooter>
          <Button isDisabled={!classificationType || !classificationStack || isClassificationSaving} isLoading={isClassificationSaving} onClick={() => void saveClassification()} variant="primary">保存分类</Button>
          <Button onClick={() => setIsClassificationOpen(false)} variant="link">取消</Button>
        </ModalFooter>
      </Modal>
    </>
  )
}
