import {
  Alert, Button, FormGroup, Label, Modal, ModalBody, ModalFooter, ModalHeader,
  Spinner, TextArea, TextInput, Title,
} from '@patternfly/react-core'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  fetchCapacityClassification, saveCapacityClassification,
  type CapacityClassificationOverview,
} from '../api/clusters'
import './CapacityClassificationDialog.css'

type Props = { onClose: () => void; onSaved: () => void; open: boolean }
const parseNamespaces = (text: string) => [...new Set(text.split(/[\s,，]+/).filter(Boolean))].sort()
const validNamespace = (name: string) => name.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(name)
const formatCPU = (value: number) => value >= 1000 ? `${(value / 1000).toFixed(1)} 核` : `${value} m`
const formatMemory = (value: number) => `${(value / 1024 ** 3).toFixed(2)} GiB`

export default function CapacityClassificationDialog({ onClose, onSaved, open }: Props) {
  const [data, setData] = useState<CapacityClassificationOverview | null>(null)
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const load = useCallback(async () => {
    setLoading(true); setData(null); setError(''); setNote(''); setQuery('')
    try {
      const result = await fetchCapacityClassification()
      setData({ ...result, discovered: result.discovered ?? [] })
      setText((result.classification.business_namespaces ?? []).join('\n'))
    } catch (cause) { setError(cause instanceof Error ? cause.message : '读取业务命名空间失败。') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => {
    if (!open) return undefined
    const timeout = window.setTimeout(() => void load(), 0)
    return () => window.clearTimeout(timeout)
  }, [load, open])

  const business = useMemo(() => parseNamespaces(text), [text])
  const invalid = business.filter((name) => !validNamespace(name))
  // Merge old server responses by name too; the business list is global across stacks.
  const discovered = useMemo(() => {
    const values = new Map<string, CapacityClassificationOverview['discovered'][number]>()
    for (const item of data?.discovered ?? []) {
      const current = values.get(item.namespace)
      if (current) {
        current.cluster_count += item.cluster_count
        current.pods += item.pods
        current.cpu_requests_milli += item.cpu_requests_milli
        current.memory_requests_bytes += item.memory_requests_bytes
      } else values.set(item.namespace, { ...item })
    }
    return [...values.values()].sort((a, b) => a.namespace.localeCompare(b.namespace))
  }, [data])
  const candidates = discovered.filter((item) => !business.includes(item.namespace) && item.namespace.includes(query.trim()))
  const removeBusiness = (name: string) => setText(business.filter((item) => item !== name).join('\n'))
  const save = async () => {
    if (!data || invalid.length > 0) return
    setSaving(true); setError('')
    try {
      await saveCapacityClassification({ business_namespaces: business, note: note.trim() })
      onSaved(); onClose()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '保存业务命名空间失败。') }
    finally { setSaving(false) }
  }

  return <Modal aria-labelledby="capacity-classification-title" className="ks-classification-modal" isOpen={open} onClose={() => { if (!saving) onClose() }} variant="large">
    <ModalHeader labelId="capacity-classification-title" title="业务命名空间配置" />
    <ModalBody>
      {error && <Alert isInline title="业务命名空间操作失败" variant="danger">{error}{!data && <Button onClick={() => void load()} variant="link">重新读取</Button>}</Alert>}
      {loading ? <div className="ks-classification-loading"><Spinner /><span>正在读取业务清单和集群 Namespace…</span></div> : data && <>
        <div className="ks-business-rule-summary"><span>当前规则：{data.classification.id ? `版本 ${data.classification.id}` : '尚未保存'}</span><span>业务 Namespace：{business.length}</span><span>集群已发现：{discovered.length}</span></div>
        <span className="ks-classification-view-hint">业务 Namespace 为全局规则，保存后对所有纳管集群生效。</span>
        <FormGroup fieldId="business-namespaces" label="业务 Namespace">
          <TextArea id="business-namespaces" aria-describedby="business-namespaces-help" isDisabled={saving} onChange={(_, value) => setText(value)} placeholder={'myapp\norders\npayments'} resizeOrientation="vertical" rows={4} value={text} validated={invalid.length ? 'error' : 'default'} />
          <span id="business-namespaces-help" className="ks-classification-view-hint">每行一个 Namespace，或用逗号分隔。</span>
        </FormGroup>
        {invalid.length > 0 && <Alert isInline title="Namespace 名称无效" variant="danger">{invalid.join('、')}：须为 1–63 位小写字母、数字或连字符，首尾须为字母或数字。</Alert>}
        <div className="ks-business-labels">{business.map((name) => <Label color="purple" key={name} onClose={saving ? undefined : () => removeBusiness(name)} closeBtnAriaLabel={`移出业务 ${name}`}>{name}{!discovered.some((item) => item.namespace === name) && '（尚未发现）'}</Label>)}</div>
        {business.length === 0 && <Alert isInline title="尚未设置业务 Namespace" variant="info">当前所有 Namespace 均按平台组件统计；请在交付前确认业务清单。</Alert>}
        <div className="ks-business-candidates-header"><div><Title headingLevel="h2" size="md">从集群选择业务 Namespace</Title></div><TextInput aria-label="搜索可加入业务的 Namespace" isDisabled={saving} onChange={(_, value) => setQuery(value)} placeholder="搜索 Namespace" value={query} /></div>
        {candidates.length > 0 ? <div className="ks-classification-table"><Table aria-label="可加入业务的 Namespace" gridBreakPoint="grid-lg" variant="compact">
          <Thead><Tr><Th>Namespace</Th><Th>覆盖集群</Th><Th>Pod</Th><Th>CPU Requests</Th><Th>内存 Requests</Th><Th screenReaderText="操作" /></Tr></Thead>
          <Tbody>{candidates.map((item) => <Tr key={item.namespace}><Td dataLabel="Namespace"><strong>{item.namespace}</strong><Label isCompact color="blue">平台组件</Label></Td><Td dataLabel="覆盖集群">{item.cluster_count}</Td><Td dataLabel="Pod">{item.pods}</Td><Td dataLabel="CPU Requests">{formatCPU(item.cpu_requests_milli)}</Td><Td dataLabel="内存 Requests">{formatMemory(item.memory_requests_bytes)}</Td><Td dataLabel="操作"><Button aria-label={`加入业务 ${item.namespace}`} isDisabled={saving} onClick={() => setText([...new Set([...business, item.namespace])].sort().join('\n'))} variant="link">加入业务</Button></Td></Tr>)}</Tbody>
        </Table></div> : <div className="ks-classification-empty"><Title headingLevel="h3" size="md">没有可加入的 Namespace</Title><span>可调整搜索条件，或直接在业务清单中填写名称。</span></div>}
        <FormGroup className="ks-classification-note" fieldId="classification-note" label="规则变更说明"><TextArea id="classification-note" isDisabled={saving} onChange={(_, value) => setNote(value)} placeholder="例如：确认交付业务 Namespace" resizeOrientation="vertical" rows={2} value={note} /></FormGroup>
      </>}
    </ModalBody>
    <ModalFooter><Button isDisabled={!data || loading || saving || invalid.length > 0} isLoading={saving} onClick={() => void save()} variant="primary">保存业务清单</Button><Button isDisabled={saving} onClick={onClose} variant="link">取消</Button></ModalFooter>
  </Modal>
}
