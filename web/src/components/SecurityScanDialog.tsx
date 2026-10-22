import { useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Checkbox,
  Form,
  FormGroup,
  FormSelect,
  FormSelectOption,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  SearchInput,
} from '@patternfly/react-core'
import type { ClusterSecurityBaseline, SecurityBenchmark } from '../api/clusters'
import './SecurityScanDialog.css'

type Props = {
  benchmarks: SecurityBenchmark[]
  clusters: ClusterSecurityBaseline[]
  initialClusterID?: string
  onClose: () => void
  onScan: (clusterIDs: string[], benchmark: string) => Promise<void>
}

const isBusy = (item?: ClusterSecurityBaseline) => item?.scan?.status === 'queued' || item?.scan?.status === 'running'

const scanStateLabel = (item: ClusterSecurityBaseline) => {
  if (!item.scan) return '未扫描'
  if (item.scan.status === 'queued') return '排队中'
  if (item.scan.status === 'running') return '扫描中'
  if (item.scan.status === 'failed') return '上次失败'
  if (item.scan.report.summary.failed > 0 || item.scan.report.summary.warnings > 0) return '需要关注'
  return '已通过'
}

export default function SecurityScanDialog({ benchmarks, clusters, initialClusterID, onClose, onScan }: Props) {
  const [query, setQuery] = useState('')
  const [benchmark, setBenchmark] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(() => {
    const initial = clusters.find(item => item.cluster_id === initialClusterID)
    return new Set(initial && !isBusy(initial) ? [initial.cluster_id] : [])
  })

  const visible = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    if (!keyword) return clusters
    return clusters.filter(item => `${item.cluster_name} ${item.environment} ${item.technology_stack}`.toLowerCase().includes(keyword))
  }, [clusters, query])
  const selectable = visible.filter(item => !isBusy(item))
  const allVisibleSelected = selectable.length > 0 && selectable.every(item => selected.has(item.cluster_id))

  const toggle = (id: string, checked: boolean) => {
    setSelected(current => {
      const next = new Set(current)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
    setError('')
  }

  const toggleVisible = (checked: boolean) => {
    setSelected(current => {
      const next = new Set(current)
      for (const item of selectable) {
        if (checked) next.add(item.cluster_id)
        else next.delete(item.cluster_id)
      }
      return next
    })
    setError('')
  }

  const submit = async () => {
    if (selected.size === 0 || submitting) return
    setSubmitting(true)
    setError('')
    try {
      await onScan([...selected], benchmark)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '提交扫描任务失败，请稍后重试。')
      setSubmitting(false)
    }
  }

  return <Modal aria-labelledby="manual-security-scan-title" isOpen onClose={onClose} variant="medium">
    <ModalHeader
      description="选择集群后立即执行一次手动扫描。任务在平台后台排队运行，关闭窗口不会中断。"
      labelId="manual-security-scan-title"
      title="手动执行扫描"
    />
    <ModalBody>
      <Form>
        <Alert isInline title="完整扫描：kube-bench 节点 + 集群侧只读 API" variant="danger">
          平台会为每个节点创建临时 kube-bench Job，并使用 hostPID、root 用户和只读 hostPath 挂载读取宿主机配置；同时通过只读 Kubernetes API 检查 Pod 安全上下文、RBAC 通配权限、Service 暴露面和 Namespace 网络策略。不读取 Secret/ConfigMap 正文，不修改节点文件，也不会安装常驻 Agent。OCP 还需要管理员允许相应 SCC，任务结束后自动回收。
        </Alert>

        <FormGroup fieldId="security-scan-benchmark" label="扫描标准版本">
          <FormSelect aria-label="选择扫描标准版本" value={benchmark} onChange={(_, value) => setBenchmark(value)}>
            <FormSelectOption label="自动识别（由 kube-bench 选择内置 CIS 基准）" value="" />
            {benchmarks.map(item => <FormSelectOption key={item.id} label={item.name} value={item.id} />)}
          </FormSelect>
          <small className="ks-scan-benchmark-hint">
            这里选择的是 <strong>CIS Benchmark 版本</strong>，不是集群 Kubernetes 版本。仅列出 kube-bench 镜像内置的 CIS v1.12.0 及以上版本；通过 <code>--benchmark</code> 固定合规口径。
          </small>
        </FormGroup>

        <FormGroup fieldId="security-scan-clusters" isRequired label={`选择集群（已选 ${selected.size} 套）`}>
          <div className="ks-scan-clusters" id="security-scan-clusters">
            <div className="ks-scan-clusters__toolbar">
              <SearchInput
                aria-label="搜索待扫描集群"
                onChange={(_, value) => setQuery(value)}
                onClear={() => setQuery('')}
                placeholder="搜索集群名称、环境或技术栈"
                value={query}
              />
              <Checkbox
                id="security-scan-select-visible"
                isChecked={allVisibleSelected}
                isDisabled={visible.length === 0}
                label="全选当前结果"
                onChange={(_, checked) => toggleVisible(checked)}
              />
            </div>
            {visible.length === 0 ? <div className="ks-scan-clusters__empty">没有匹配的集群，请调整搜索条件。</div>
              : <div className="ks-scan-clusters__list">{visible.map(item => {
                const busy = isBusy(item)
                return <Checkbox
                  id={`security-scan-cluster-${item.cluster_id}`}
                  isChecked={selected.has(item.cluster_id)}
                  isDisabled={busy}
                  key={item.cluster_id}
                  label={<span className="ks-scan-cluster">
                    <strong>{item.cluster_name}</strong>
                    <small>{item.environment || '未分类'} · {item.technology_stack || '未标记'} · {scanStateLabel(item)}</small>
                  </span>}
                  onChange={(_, checked) => toggle(item.cluster_id, checked)}
                />
              })}</div>}
          </div>
        </FormGroup>
        {selected.size > 1 && <Alert isInline title={`本次将提交 ${selected.size} 个扫描任务`} variant="info">
          平台默认同时执行 2 个任务，其余任务依次排队，避免同时冲击多套集群的 API Server。
        </Alert>}
        {error && <Alert isInline title="提交失败" variant="danger">{error}</Alert>}
      </Form>
    </ModalBody>
    <ModalFooter>
      <Button isDisabled={selected.size === 0 || submitting} isLoading={submitting} onClick={() => void submit()} variant="primary">
        {submitting ? '正在提交' : `开始扫描${selected.size > 0 ? `（${selected.size}）` : ''}`}
      </Button>
      <Button isDisabled={submitting} onClick={onClose} variant="link">取消</Button>
    </ModalFooter>
  </Modal>
}
