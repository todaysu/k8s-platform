import { usePermission } from '../auth/context'
import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Grid,
  GridItem,
  Label,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Spinner,
  TextArea,
  Title,
} from '@patternfly/react-core'
import { CheckCircleIcon, ExclamationTriangleIcon, SyncAltIcon } from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useState } from 'react'

import {
  fetchClusterCapacity,
  keepCapacityBaseline,
  lockClusterCapacityBaseline,
  type CapacityValues,
  type ClusterCapacity,
} from '../api/clusters'
import './ClusterCapacityPage.css'

type Props = { clusterId: string }

const baselineMeta = {
  not_set: { color: 'grey' as const, label: '尚未建立基线', text: '锁定交付时的固定资源配置，后续即可识别平台开销漂移。' },
  stable: { color: 'green' as const, label: '基线稳定', text: '平台固定开销和集群可调度容量均在基线范围内。' },
  drift: { color: 'orange' as const, label: '固定开销漂移', text: '平台组件 Requests 相比交付基线变化超过 5%。' },
  rebaseline: { color: 'orange' as const, label: '建议重建基线', text: '集群可调度容量变化超过 5%，可能发生过扩缩容。' },
}

function percent(value: number, total: number) {
  return total > 0 ? value / total * 100 : 0
}

function bounded(value: number) {
  return Math.max(0, Math.min(100, value))
}

function formatCPU(milli: number) {
  const cores = milli / 1000
  return `${cores >= 10 || Number.isInteger(cores) ? cores.toFixed(cores >= 10 || Number.isInteger(cores) ? 0 : 1) : cores.toFixed(2)} 核`
}

function formatMemory(bytes: number) {
  const gib = bytes / (1024 ** 3)
  return `${gib >= 10 ? gib.toFixed(0) : gib.toFixed(1)} GiB`
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function changeText(value: number) {
  if (Math.abs(value) < 0.05) return '无变化'
  return `${value > 0 ? '+' : ''}${value.toFixed(1)}%`
}

type ResourcePanelProps = {
  actualAvailable: boolean
  allocatable: number
  business: CapacityValues
  format: (value: number) => string
  kind: 'cpu' | 'memory'
  pending: CapacityValues
  platform: CapacityValues
  safetyPercent: number
  title: string
}

function ResourcePanel({ actualAvailable, allocatable, business, format, kind, pending, platform, safetyPercent, title }: ResourcePanelProps) {
  const requestKey = kind === 'cpu' ? 'cpu_requests_milli' : 'memory_requests_bytes'
  const limitKey = kind === 'cpu' ? 'cpu_limits_milli' : 'memory_limits_bytes'
  const usageKey = kind === 'cpu' ? 'cpu_usage_milli' : 'memory_usage_bytes'
  const platformRequest = platform[requestKey]
  const businessRequest = business[requestKey]
  const pendingRequest = pending[requestKey]
  const reserved = allocatable * safetyPercent / 100
  const remaining = Math.max(0, allocatable - platformRequest - businessRequest - pendingRequest - reserved)
  const requested = platformRequest + businessRequest + pendingRequest
  const isOverTarget = requested + reserved > allocatable

  return (
    <Card className="ks-capacity-resource-card" isFullHeight>
      <CardHeader>
        <CardTitle><Title headingLevel="h2" size="lg">{title}</Title><span>可调度总量 {format(allocatable)}</span></CardTitle>
        <Label color={isOverTarget ? 'orange' : 'green'}>{isOverTarget ? '余量不足' : `剩余 ${percent(remaining, allocatable).toFixed(1)}%`}</Label>
      </CardHeader>
      <CardBody>
        <div aria-label={`${title}容量分布`} className="ks-capacity-stack">
          <span className="is-platform" style={{ width: `${bounded(percent(platformRequest, allocatable))}%` }} />
          <span className="is-business" style={{ width: `${bounded(percent(businessRequest, allocatable))}%` }} />
          <span className="is-pending" style={{ width: `${bounded(percent(pendingRequest, allocatable))}%` }} />
          <span className="is-reserve" style={{ width: `${bounded(safetyPercent)}%` }} />
          <span className="is-remaining" style={{ flex: 1 }} />
        </div>
        <div className="ks-capacity-legend">
          <div><i className="is-platform" /><span>平台固定</span><strong>{format(platformRequest)} · {percent(platformRequest, allocatable).toFixed(1)}%</strong></div>
          <div><i className="is-business" /><span>业务申请</span><strong>{format(businessRequest)} · {percent(businessRequest, allocatable).toFixed(1)}%</strong></div>
          <div><i className="is-pending" /><span>待调度</span><strong>{format(pendingRequest)} · {percent(pendingRequest, allocatable).toFixed(1)}%</strong></div>
          <div><i className="is-reserve" /><span>安全预留</span><strong>{format(reserved)} · {safetyPercent.toFixed(0)}%</strong></div>
          <div><i className="is-remaining" /><span>可继续分配</span><strong>{format(remaining)} · {percent(remaining, allocatable).toFixed(1)}%</strong></div>
        </div>
        <div className="ks-capacity-usage-row">
          <div><span>平台当前使用</span><strong>{actualAvailable && platform[usageKey] !== null ? `${format(platform[usageKey] ?? 0)} · ${percent(platform[usageKey] ?? 0, allocatable).toFixed(1)}%` : 'Prometheus 数据不可用'}</strong></div>
          <div><span>业务当前使用</span><strong>{actualAvailable && business[usageKey] !== null ? `${format(business[usageKey] ?? 0)} · ${percent(business[usageKey] ?? 0, allocatable).toFixed(1)}%` : 'Prometheus 数据不可用'}</strong></div>
          <div><span>业务 Limits</span><strong>{format(business[limitKey])} · {percent(business[limitKey], allocatable).toFixed(1)}%</strong></div>
        </div>
        {pendingRequest > 0 && <Alert isInline isPlain title={`另有 ${format(pendingRequest)} Pending 需求尚未获得节点资源`} variant="warning" />}
      </CardBody>
    </Card>
  )
}

export default function ClusterCapacityPage({ clusterId }: Props) {
 const canConfigure = usePermission('configure')

  const [capacity, setCapacity] = useState<ClusterCapacity | null>(null)
  const [error, setError] = useState('')
  const [isBaselineOpen, setIsBaselineOpen] = useState(false)
  const [isLocking, setIsLocking] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [isDecisionOpen, setIsDecisionOpen] = useState(false)
  const [isKeeping, setIsKeeping] = useState(false)
  const [keepReason, setKeepReason] = useState('')
  const [reasonError, setReasonError] = useState('')

  const load = useCallback(async (refresh = false) => {
    setError('')
    try {
      setCapacity(await fetchClusterCapacity(clusterId, refresh))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '读取集群容量失败。')
    } finally {
      setIsLoading(false)
    }
  }, [clusterId])

  useEffect(() => {
    const timeout = window.setTimeout(() => void load(), 0)
    const interval = window.setInterval(() => { if (!document.hidden) void load() }, 60_000)
    return () => { window.clearTimeout(timeout); window.clearInterval(interval) }
  }, [load])

  const lockBaseline = async () => {
    setIsLocking(true)
    setError('')
    try {
      await lockClusterCapacityBaseline(clusterId)
      setIsBaselineOpen(false)
      await load(true)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '建立容量基线失败。')
    } finally {
      setIsLocking(false)
    }
  }

  const keepBaseline = async () => {
    if (!keepReason.trim()) { setReasonError('暂不更新基线时必须填写原因。'); return }
    setIsKeeping(true); setReasonError(''); setError('')
    try {
      await keepCapacityBaseline(clusterId, keepReason.trim())
      setIsDecisionOpen(false); setKeepReason('')
      await load(true)
    } catch (requestError) {
      setReasonError(requestError instanceof Error ? requestError.message : '保存基线决策失败。')
    } finally { setIsKeeping(false) }
  }

  if (isLoading && !capacity) return <div className="ks-capacity-loading"><Spinner aria-label="正在计算集群容量" /><span>正在读取节点容量与工作负载资源声明…</span></div>
  if (!capacity) return <Alert actionLinks={<Button isInline onClick={() => void load(true)} variant="link">重新读取</Button>} isInline title="容量数据读取失败" variant="danger">{error}</Alert>

  const meta = baselineMeta[capacity.comparison.status]
  return (
    <div className="ks-capacity-page">
      {error && <Alert isInline title="刷新失败，当前显示上一次成功数据" variant="warning">{error}</Alert>}
      {capacity.baseline_review.status === 'pending' && <Alert actionLinks={<><Button isDisabled={!canConfigure} isInline onClick={() => setIsBaselineOpen(true)} variant="link">生成新基线</Button><Button isDisabled={!canConfigure} isInline onClick={() => setIsDecisionOpen(true)} variant="link">暂不更新</Button></>} isInline title="平台组件范围已变化，需要确认交付基线" variant="warning">当前平台组件 Namespace 与交付基线不同。请确认新范围后生成基线；若暂时沿用旧基线，需要记录原因。</Alert>}
      {capacity.baseline_review.status === 'kept' && <Alert isInline title="本规则版本暂时沿用旧基线" variant="info">记录原因：{capacity.baseline_review.reason || '未填写'}</Alert>}
      <Card className="ks-capacity-baseline-card">
        <CardBody>
          <div className="ks-capacity-baseline-main">
            <div className={`ks-capacity-baseline-icon is-${capacity.comparison.status}`}>{capacity.comparison.status === 'stable' ? <CheckCircleIcon /> : <ExclamationTriangleIcon />}</div>
            <div><span>交付容量基线</span><div><Title headingLevel="h2" size="xl">{meta.label}</Title><Label color={meta.color}>{capacity.baseline ? `版本 ${capacity.baseline.id}` : '未锁定'}</Label></div><p>{meta.text}</p></div>
          </div>
          <div className="ks-capacity-baseline-actions">
            <span>{capacity.baseline ? `最近锁定：${formatTime(capacity.baseline.captured_at)}` : `当前采集：${formatTime(capacity.collected_at)}`}</span>
            <Button isDisabled={!canConfigure} icon={<SyncAltIcon />} onClick={() => setIsBaselineOpen(true)} variant={capacity.baseline ? 'secondary' : 'primary'}>{capacity.baseline ? '生成新基线' : '锁定当前为基线'}</Button>
          </div>
        </CardBody>
      </Card>

      <div className="ks-capacity-context"><span><strong>{capacity.worker_nodes}</strong> 个可调度节点</span><span>平台组件 Namespace：<strong>{capacity.system_namespaces.join('、') || '未配置'}</strong></span><span>分类规则：<strong>{capacity.classification_version_id ? `版本 ${capacity.classification_version_id}` : '默认规则'}</strong></span><span>安全预留：<strong>{capacity.safety_reserve_percent}%</strong></span></div>

      {!capacity.metrics_available && <Alert isInline title="实际使用数据不可用" variant="info">Requests、Limits 和剩余容量仍来自 Kubernetes API，可正常用于调度容量判断；请检查集群 Prometheus 的 Service 与查询权限。</Alert>}
      {((capacity.business.pods_without_cpu_requests ?? 0) > 0 || (capacity.business.pods_without_memory_requests ?? 0) > 0) && (
        <Alert isInline title="业务资源声明不完整，剩余容量可能被高估" variant="warning">
          {(capacity.business.pods_without_cpu_requests ?? 0)} 个业务 Pod 未配置 CPU Requests，{(capacity.business.pods_without_memory_requests ?? 0)} 个业务 Pod 未配置内存 Requests。建议先补齐资源申请，再依据本页决定还能部署多少实例。
        </Alert>
      )}
      {(capacity.new_platform_namespaces ?? []).length > 0 && <Alert isInline title="发现交付后新增的平台 Namespace" variant="warning">{capacity.new_platform_namespaces!.join('、')}。已按平台组件统计；请核对变更，确认后可手动生成新基线，当前交付基线未改变。</Alert>}

      <Grid hasGutter>
        <GridItem lg={6} span={12}><ResourcePanel actualAvailable={capacity.metrics_available} allocatable={capacity.cpu_allocatable_milli} business={capacity.business} format={formatCPU} kind="cpu" pending={capacity.pending} platform={capacity.platform} safetyPercent={capacity.safety_reserve_percent} title="CPU 容量" /></GridItem>
        <GridItem lg={6} span={12}><ResourcePanel actualAvailable={capacity.metrics_available} allocatable={capacity.memory_allocatable_bytes} business={capacity.business} format={formatMemory} kind="memory" pending={capacity.pending} platform={capacity.platform} safetyPercent={capacity.safety_reserve_percent} title="内存容量" /></GridItem>
      </Grid>

      {capacity.baseline && <Card>
        <CardHeader><CardTitle><Title headingLevel="h2" size="lg">与交付基线对比</Title></CardTitle></CardHeader>
        <CardBody className="ks-capacity-drift-grid">
          <div><span>平台 CPU Requests</span><strong>{changeText(capacity.comparison.platform_cpu_request_change_percent)}</strong></div>
          <div><span>平台内存 Requests</span><strong>{changeText(capacity.comparison.platform_memory_request_change_percent)}</strong></div>
          <div><span>CPU Allocatable</span><strong>{changeText(capacity.comparison.cpu_allocatable_change_percent)}</strong></div>
          <div><span>内存 Allocatable</span><strong>{changeText(capacity.comparison.memory_allocatable_change_percent)}</strong></div>
        </CardBody>
      </Card>}

      <Card>
        <CardHeader><CardTitle><Title headingLevel="h2" size="lg">命名空间资源分布</Title></CardTitle></CardHeader>
        <div className="ks-capacity-table-scroll"><Table aria-label="命名空间容量列表" gridBreakPoint="grid-lg" variant="compact">
          <Thead><Tr><Th>命名空间</Th><Th>分类</Th><Th>Pod</Th><Th>CPU Requests</Th><Th>CPU Limits</Th><Th>内存 Requests</Th><Th>内存 Limits</Th><Th>配置情况</Th></Tr></Thead>
          <Tbody>{capacity.namespaces.map((item) => <Tr key={item.namespace}>
            <Td dataLabel="命名空间"><strong>{item.namespace}</strong>{(capacity.new_platform_namespaces ?? []).includes(item.namespace) && <Label className="ks-capacity-new-label" color="orange" isCompact>新增</Label>}</Td><Td dataLabel="分类"><Label color={item.class === 'platform' ? 'blue' : item.class === 'business' ? 'purple' : 'orange'}>{item.class === 'business' ? '业务' : '平台组件'}</Label></Td><Td dataLabel="Pod">{item.pods}</Td><Td dataLabel="CPU Requests">{formatCPU(item.cpu_requests_milli)}</Td><Td dataLabel="CPU Limits">{formatCPU(item.cpu_limits_milli)}</Td><Td dataLabel="内存 Requests">{formatMemory(item.memory_requests_bytes)}</Td><Td dataLabel="内存 Limits">{formatMemory(item.memory_limits_bytes)}</Td><Td dataLabel="配置情况">{(item.pods_without_cpu_requests ?? 0) > 0 || (item.pods_without_memory_requests ?? 0) > 0 ? <Label color="orange">Requests 不完整</Label> : <Label color="green">完整</Label>}</Td>
          </Tr>)}</Tbody>
        </Table></div>
      </Card>

      <Modal aria-labelledby="capacity-baseline-title" isOpen={isBaselineOpen} onClose={() => { if (!isLocking) setIsBaselineOpen(false) }} variant="small">
        <ModalHeader description="基线版本不可变，建立新版本不会删除历史记录。" labelId="capacity-baseline-title" title={capacity.baseline ? '生成新的容量基线' : '锁定当前容量基线'} titleIconVariant="warning" />
        <ModalBody><p>将记录当前可调度容量、系统命名空间，以及平台组件的 CPU/内存 Requests 和 Limits。请确认当前集群处于交付稳态。</p></ModalBody>
        <ModalFooter><Button isLoading={isLocking} onClick={() => void lockBaseline()} variant="primary">确认锁定</Button><Button isDisabled={isLocking} onClick={() => setIsBaselineOpen(false)} variant="link">取消</Button></ModalFooter>
      </Modal>
      <Modal aria-labelledby="capacity-baseline-decision-title" isOpen={isDecisionOpen} onClose={() => { if (!isKeeping) setIsDecisionOpen(false) }} variant="small">
        <ModalHeader description="该记录只保存在运维平台，不会修改集群资源。" labelId="capacity-baseline-decision-title" title="暂不更新容量基线" titleIconVariant="warning" />
        <ModalBody><p>请说明为何当前分类变化不需要生成新基线，方便后续运维人员判断这是不是遗漏。</p>{reasonError && <Alert isInline title="无法保存" variant="danger">{reasonError}</Alert>}<TextArea aria-label="暂不更新基线的原因" className="ks-capacity-reason" onChange={(_, value) => { setKeepReason(value); if (value.trim()) setReasonError('') }} placeholder="例如：本次只补录已有系统 Namespace，平台组件 Requests 未发生变化" resizeOrientation="vertical" value={keepReason} /></ModalBody>
        <ModalFooter><Button isLoading={isKeeping} onClick={() => void keepBaseline()} variant="primary">保存原因并沿用旧基线</Button><Button isDisabled={isKeeping} onClick={() => setIsDecisionOpen(false)} variant="link">取消</Button></ModalFooter>
      </Modal>
    </div>
  )
}
