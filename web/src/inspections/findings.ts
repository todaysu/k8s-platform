import type { FleetCapacity } from '../api/clusters'

export type InspectionSeverity = 'critical' | 'warning' | 'info'
export type InspectionCategory = 'availability' | 'nodes' | 'workloads' | 'capacity' | 'configuration' | 'monitoring'

export type InspectionCluster = {
  apiLatencyMs: number | null
  clusterType: string
  coreComponents: { ready: number; total: number } | null
  environment: string
  id: string
  instances: { ready: number; total: number } | null
  lastChecked: string
  metricsAvailable: boolean
  name: string
  nodes: { ready: number; total: number } | null
  status: 'healthy' | 'warning' | 'unreachable'
  technologyStack: string
}

export type InspectionFinding = {
  category: InspectionCategory
  checkedAt: string
  clusterId: string
  clusterName: string
  clusterType: string
  destination: 'cluster' | 'capacity'
  environment: string
  evidence: string[]
  explanation: string
  id: string
  recommendation: string
  resource: string
  ruleId: string
  severity: InspectionSeverity
  source: '集群健康' | '容量基线'
  technologyStack: string
  title: string
}

export const inspectionCategoryLabels: Record<InspectionCategory, string> = {
  availability: '可用性',
  nodes: '节点',
  workloads: '工作负载',
  capacity: '容量',
  configuration: '配置',
  monitoring: '监控',
}

export const inspectionSeverityLabels: Record<InspectionSeverity, string> = {
  critical: '严重',
  warning: '警告',
  info: '提示',
}

const severityOrder: Record<InspectionSeverity, number> = { critical: 0, warning: 1, info: 2 }

function finding(
  cluster: InspectionCluster,
  ruleId: string,
  values: Omit<InspectionFinding, 'checkedAt' | 'clusterId' | 'clusterName' | 'clusterType' | 'environment' | 'id' | 'ruleId' | 'technologyStack'>,
  checkedAt = cluster.lastChecked,
): InspectionFinding {
  return {
    ...values,
    checkedAt,
    clusterId: cluster.id,
    clusterName: cluster.name,
    clusterType: cluster.clusterType,
    environment: cluster.environment,
    id: `${ruleId}:${cluster.id}:${values.resource}`,
    ruleId,
    technologyStack: cluster.technologyStack,
  }
}

function clusterFindings(cluster: InspectionCluster): InspectionFinding[] {
  if (cluster.status === 'unreachable') {
    return [finding(cluster, 'CLUSTER_UNREACHABLE', {
      category: 'availability',
      destination: 'cluster',
      evidence: ['最近一次健康采集无法连接 API Server，或凭据没有完成鉴权。'],
      explanation: '平台当前不能核验这套集群的节点、工作负载和组件状态。',
      recommendation: '先检查 API Server 网络连通性、Kubeconfig 凭据和证书有效期。',
      resource: 'Cluster',
      severity: 'critical',
      source: '集群健康',
      title: '集群连接失败',
    })]
  }

  const values: InspectionFinding[] = []
  if (cluster.nodes && cluster.nodes.ready < cluster.nodes.total) {
    const unavailable = cluster.nodes.total - cluster.nodes.ready
    values.push(finding(cluster, 'NODE_NOT_READY', {
      category: 'nodes',
      destination: 'cluster',
      evidence: [`节点 Ready ${cluster.nodes.ready} / ${cluster.nodes.total}`],
      explanation: '未就绪节点可能影响Pod调度、容量和业务副本分布。',
      recommendation: '进入节点列表，检查节点 Condition、磁盘、内存压力和 kubelet 状态。',
      resource: 'Node',
      severity: 'critical',
      source: '集群健康',
      title: `${unavailable} 个节点未就绪`,
    }))
  }
  if (cluster.instances && cluster.instances.ready < cluster.instances.total) {
    const unavailable = cluster.instances.total - cluster.instances.ready
    values.push(finding(cluster, 'INSTANCE_NOT_READY', {
      category: 'workloads',
      destination: 'cluster',
      evidence: [`实例 Ready ${cluster.instances.ready} / ${cluster.instances.total}`, `${unavailable} 个实例尚未就绪`],
      explanation: '至少一个Pod未达到Ready状态，业务副本数可能低于期望。',
      recommendation: '进入工作负载查看未就绪Pod，再结合当前状态、Event和日志继续排查。',
      resource: 'Pod',
      severity: 'warning',
      source: '集群健康',
      title: '实例未全部就绪',
    }))
  }
  if (cluster.coreComponents && cluster.coreComponents.ready < cluster.coreComponents.total) {
    const unavailable = cluster.coreComponents.total - cluster.coreComponents.ready
    values.push(finding(cluster, 'CORE_COMPONENT_NOT_READY', {
      category: 'availability',
      destination: 'cluster',
      evidence: [`核心组件正常 ${cluster.coreComponents.ready} / ${cluster.coreComponents.total}`],
      explanation: '平台命名空间中的关键组件存在未就绪实例。',
      recommendation: '优先检查异常组件的Pod、Event和日志，确认是否影响集群控制能力。',
      resource: 'Platform component',
      severity: 'critical',
      source: '集群健康',
      title: `${unavailable} 个核心组件异常`,
    }))
  }
  if (!cluster.metricsAvailable) {
    values.push(finding(cluster, 'METRICS_UNAVAILABLE', {
      category: 'monitoring',
      destination: 'cluster',
      evidence: ['最近一次检查未取得集群Prometheus指标。'],
      explanation: '资源对象仍可查看，但CPU、内存实际使用率和趋势判断可能不完整。',
      recommendation: '检查Prometheus Service、Service Proxy权限及查询接口是否可用。',
      resource: 'Prometheus',
      severity: 'info',
      source: '集群健康',
      title: '指标数据不可用',
    }))
  }
  if (cluster.clusterType === '未标记' || cluster.technologyStack === '未标记') {
    values.push(finding(cluster, 'CLUSTER_CLASSIFICATION_MISSING', {
      category: 'configuration',
      destination: 'cluster',
      evidence: [`集群类型：${cluster.clusterType}`, `技术栈：${cluster.technologyStack}`],
      explanation: '缺少分类的集群不能准确进入对应容量资源池。',
      recommendation: '进入集群概览补充G01/OCP类型和技术栈。',
      resource: 'Cluster classification',
      severity: 'warning',
      source: '集群健康',
      title: '集群分类尚未完成',
    }))
  }
  return values
}

function capacityFindings(clusters: InspectionCluster[], capacity: FleetCapacity): InspectionFinding[] {
  const byID = new Map(clusters.map((cluster) => [cluster.id, cluster]))
  const values: InspectionFinding[] = []

  for (const item of capacity.items) {
    const cluster = byID.get(item.cluster_id)
    if (!cluster) continue
    const snapshot = item.capacity
    const cpuReserve = snapshot.cpu_allocatable_milli * snapshot.safety_reserve_percent / 100
    const memoryReserve = snapshot.memory_allocatable_bytes * snapshot.safety_reserve_percent / 100
    const cpuRemaining = snapshot.cpu_allocatable_milli - snapshot.platform.cpu_requests_milli - snapshot.business.cpu_requests_milli - snapshot.pending.cpu_requests_milli - cpuReserve
    const memoryRemaining = snapshot.memory_allocatable_bytes - snapshot.platform.memory_requests_bytes - snapshot.business.memory_requests_bytes - snapshot.pending.memory_requests_bytes - memoryReserve
    const cpuHeadroom = snapshot.cpu_allocatable_milli > 0 ? cpuRemaining / snapshot.cpu_allocatable_milli * 100 : 0
    const memoryHeadroom = snapshot.memory_allocatable_bytes > 0 ? memoryRemaining / snapshot.memory_allocatable_bytes * 100 : 0
    const lowestHeadroom = Math.min(cpuHeadroom, memoryHeadroom)
    const checkedAt = new Date(snapshot.collected_at).toLocaleString('zh-CN', { hour12: false })

    if (lowestHeadroom <= 20) {
      values.push(finding(cluster, 'CAPACITY_LOW', {
        category: 'capacity',
        destination: 'capacity',
        evidence: [`CPU安全余量 ${cpuHeadroom.toFixed(1)}%`, `内存安全余量 ${memoryHeadroom.toFixed(1)}%`, `已扣除 ${snapshot.safety_reserve_percent}% 安全预留`],
        explanation: '在扣除平台、业务、Pending和安全预留后，至少一种资源的剩余比例已进入关注区间。',
        recommendation: '进入容量页确认增长来源，并评估扩容或调整新业务落点。',
        resource: 'Capacity pool',
        severity: lowestHeadroom <= 10 ? 'critical' : 'warning',
        source: '容量基线',
        title: '可分配容量偏低',
      }, checkedAt))
    }
    if (snapshot.comparison.status === 'drift' || snapshot.comparison.status === 'rebaseline') {
      values.push(finding(cluster, 'PLATFORM_BASELINE_DRIFT', {
        category: 'capacity',
        destination: 'capacity',
        evidence: [`平台CPU Requests变化 ${snapshot.comparison.platform_cpu_request_change_percent.toFixed(1)}%`, `平台内存 Requests变化 ${snapshot.comparison.platform_memory_request_change_percent.toFixed(1)}%`],
        explanation: '平台固定组件申请量已偏离交付时锁定的稳态基线。',
        recommendation: '检查组件变更来源，确认合理后建立新基线，否则记录原因并继续观察。',
        resource: 'Platform baseline',
        severity: 'warning',
        source: '容量基线',
        title: '平台容量基线发生漂移',
      }, checkedAt))
    }
    const missingCPU = snapshot.business.pods_without_cpu_requests ?? 0
    const missingMemory = snapshot.business.pods_without_memory_requests ?? 0
    if (missingCPU > 0 || missingMemory > 0) {
      values.push(finding(cluster, 'BUSINESS_REQUESTS_MISSING', {
        category: 'configuration',
        destination: 'capacity',
        evidence: [`${missingCPU} 个业务Pod未设置CPU Requests`, `${missingMemory} 个业务Pod未设置内存Requests`],
        explanation: '缺失Requests会降低调度和容量计算的可靠性。',
        recommendation: '从容量Namespace明细定位工作负载，再补充合理的Requests。',
        resource: 'Business workloads',
        severity: 'warning',
        source: '容量基线',
        title: '业务Pod缺少资源申请',
      }, checkedAt))
    }
    if ((snapshot.new_platform_namespaces ?? []).length > 0) {
      values.push(finding(cluster, 'NEW_PLATFORM_NAMESPACE', {
        category: 'configuration',
        destination: 'capacity',
        evidence: (snapshot.new_platform_namespaces ?? []).map((namespace) => `新增Namespace：${namespace}`),
        explanation: '发现不在当前技术栈容量分类规则中的平台Namespace。',
        recommendation: '确认Namespace归属并更新分类规则，避免后续容量归属错误。',
        resource: 'Namespace',
        severity: 'warning',
        source: '容量基线',
        title: '发现新的平台Namespace',
      }, checkedAt))
    }
  }

  for (const failure of capacity.failures) {
    const cluster = byID.get(failure.cluster_id)
    if (!cluster || cluster.status === 'unreachable') continue
    values.push(finding(cluster, 'CAPACITY_UNVERIFIED', {
      category: 'capacity',
      destination: 'capacity',
      evidence: [`容量采集失败：${failure.code}`],
      explanation: '本次没有取得这套集群的容量快照，不能把旧数据当作当前容量。',
      recommendation: '检查Kubernetes API权限和资源查询，再重试容量采集。',
      resource: 'Capacity snapshot',
      severity: 'warning',
      source: '容量基线',
      title: '容量状态无法核验',
    }))
  }
  return values
}

export function buildInspectionFindings(clusters: InspectionCluster[], capacity: FleetCapacity | null): InspectionFinding[] {
  const values = clusters.flatMap(clusterFindings)
  if (capacity) values.push(...capacityFindings(clusters, capacity))
  return values.sort((left, right) => severityOrder[left.severity] - severityOrder[right.severity]
    || left.clusterName.localeCompare(right.clusterName, 'zh-CN')
    || left.ruleId.localeCompare(right.ruleId))
}
