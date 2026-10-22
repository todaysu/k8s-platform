import {
  Alert,
  Button,
  EmptyState,
  EmptyStateBody,
  EmptyStateFooter,
  Label,
  Modal,
  ModalBody,
  ModalHeader,
  SearchInput,
  Spinner,
  Title,
} from '@patternfly/react-core'
import { CubesIcon, SearchIcon } from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useCallback, useEffect, useState } from 'react'

import { searchGlobalResources, type GlobalResourceResult } from '../api/clusters'
import './GlobalResourceSearch.css'

type Props = {
  onClose: () => void
  onOpenResult: (result: GlobalResourceResult) => void
  open: boolean
}

export default function GlobalResourceSearch({ onClose, onOpenResult, open }: Props) {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<GlobalResourceResult[]>([])
  const [failedClusters, setFailedClusters] = useState<string[]>([])
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(false)
  const [indexPending, setIndexPending] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    const normalized = query.trim()
    if (normalized.length < 2) return
    const controller = new AbortController()
    const timer = window.setTimeout(async () => {
      setLoading(true)
      setError('')
      try {
        const response = await searchGlobalResources(normalized, controller.signal)
        if (controller.signal.aborted) return
        setResults(response.items)
        setFailedClusters(response.failed_clusters ?? [])
        setTruncated(response.truncated)
        setIndexPending(response.index_pending ?? false)
      } catch (requestError) {
        if (!controller.signal.aborted) setError(requestError instanceof Error ? requestError.message : '跨集群搜索失败。')
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }, 350)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [open, query])

  const updateQuery = (value: string) => {
    setQuery(value)
    if (value.trim().length >= 2) return
    setResults([])
    setFailedClusters([])
    setTruncated(false)
    setIndexPending(false)
    setLoading(false)
    setError('')
  }

  const close = useCallback(() => {
    setQuery('')
    setResults([])
    setError('')
    setFailedClusters([])
    setTruncated(false)
    setIndexPending(false)
    setLoading(false)
    onClose()
  }, [onClose])

  useEffect(() => {
    if (!open) return
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Element
      if (!(target instanceof Element) || !target.closest('[aria-labelledby="global-resource-search-title"]')) close()
    }
    document.addEventListener('pointerdown', dismiss)
    return () => document.removeEventListener('pointerdown', dismiss)
  }, [open, close])

  return (
    <Modal aria-labelledby="global-resource-search-title" isOpen={open} onClose={close} variant="large">
      <ModalHeader
        description="在全部纳管集群中定位工作负载和 Pod，结果会标明所在集群与命名空间。"
        labelId="global-resource-search-title"
        title="跨集群资源搜索"
      />
      <ModalBody>
        <SearchInput
          aria-label="跨集群搜索"
          autoFocus
          className="ks-global-search-input"
          onChange={(_, value) => updateQuery(value)}
          onClear={() => updateQuery('')}
          placeholder="输入应用、工作负载、Pod、命名空间或镜像（至少 2 个字符）"
          value={query}
        />
        {failedClusters.length > 0 && <Alert isInline title="部分集群未完成搜索" variant="warning">未读取：{failedClusters.join('、')}。其他集群的结果仍可使用。</Alert>}
        {truncated && <Alert isInline title="结果过多" variant="info">当前只显示前 200 条，请使用更精确的关键词。</Alert>}
        {error && <Alert isInline title="搜索失败" variant="danger">{error}</Alert>}
        {loading ? <div className="ks-global-search-state"><Spinner aria-label="正在跨集群搜索" /><span>正在查询资源索引…</span></div> : query.trim().length < 2 ? (
          <EmptyState icon={SearchIcon} titleText="输入至少 2 个字符" headingLevel="h2">
            <EmptyStateBody>建议使用业务名、命名空间、Pod 名或镜像名搜索。</EmptyStateBody>
          </EmptyState>
        ) : results.length === 0 && !error ? (
          <EmptyState icon={CubesIcon} titleText={indexPending ? "资源索引正在准备" : "没有匹配的资源"} headingLevel="h2">
            <EmptyStateBody>{indexPending ? "后台采集完成后即可搜索，请稍后重试。" : "请检查关键词，或者换用应用名、命名空间和镜像名。"}</EmptyStateBody>
            <EmptyStateFooter><Button isInline onClick={() => updateQuery('')} variant="link">清除搜索</Button></EmptyStateFooter>
          </EmptyState>
        ) : !error && <div className="ks-global-search-results">
          <div className="ks-global-search-summary"><Title headingLevel="h2" size="md">搜索结果</Title><span>{results.length} 条</span></div>
          <Table aria-label="跨集群资源搜索结果" gridBreakPoint="grid-lg" variant="compact">
            <Thead><Tr><Th width={30}>资源</Th><Th>类型</Th><Th width={25}>所在集群</Th><Th>命名空间</Th><Th>状态</Th><Th width={25}>Owner / 镜像</Th></Tr></Thead>
            <Tbody>{results.map((result) => <Tr isClickable key={`${result.cluster_id}/${result.kind}/${result.namespace}/${result.name}`} onRowClick={() => onOpenResult(result)}>
              <Td dataLabel="资源"><Button className="ks-global-search-link" isInline onClick={(event) => { event.stopPropagation(); onOpenResult(result) }} variant="link">{result.name}</Button></Td>
              <Td dataLabel="类型"><Label color={result.kind === 'Pod' ? 'blue' : 'purple'}>{result.kind}</Label></Td>
              <Td dataLabel="所在集群"><strong>{result.cluster_name}</strong><small>{result.environment || '未分类'} · {result.technology_stack || '未标记'}</small></Td>
              <Td dataLabel="命名空间">{result.namespace}</Td>
              <Td dataLabel="状态">{result.status || '—'}</Td>
              <Td dataLabel="Owner / 镜像"><span>{result.owner_name ? `${result.owner_kind} / ${result.owner_name}` : result.images[0] || '—'}</span></Td>
            </Tr>)}</Tbody>
          </Table>
        </div>}
      </ModalBody>
    </Modal>
  )
}
