import {
  Alert,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  FormSelect,
  FormSelectOption,
  Label,
  SearchInput,
  Title,
  Toolbar,
  ToolbarContent,
  ToolbarGroup,
  ToolbarItem,
} from '@patternfly/react-core'
import {
  ArrowRightIcon,
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  InfoCircleIcon,
  SearchIcon,
} from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useMemo, useState } from 'react'

import {
  inspectionCategoryLabels,
  inspectionSeverityLabels,
  type InspectionCategory,
  type InspectionFinding,
  type InspectionSeverity,
} from '../inspections/findings'
import './FleetInspectionsPage.css'

const severityMeta: Record<InspectionSeverity, { color: 'red' | 'orange' | 'blue'; icon: typeof ExclamationCircleIcon }> = {
  critical: { color: 'red', icon: ExclamationCircleIcon },
  warning: { color: 'orange', icon: ExclamationTriangleIcon },
  info: { color: 'blue', icon: InfoCircleIcon },
}

type Props = {
  findings: InspectionFinding[]
  onOpenCapacity: (clusterId: string) => void
  onOpenCluster: (clusterId: string) => void
}

function FindingDetail({ finding, onOpenCapacity, onOpenCluster }: Props & { finding: InspectionFinding }) {
  const Icon = severityMeta[finding.severity].icon
  return (
    <aside aria-label="巡检发现详情" className="ks-inspection-detail">
      <header>
        <div className={`ks-inspection-detail__icon is-${finding.severity}`}><Icon /></div>
        <div>
          <Label color={severityMeta[finding.severity].color} isCompact>{inspectionSeverityLabels[finding.severity]}</Label>
          <Title headingLevel="h2" size="lg">{finding.title}</Title>
          <p>{finding.clusterName} · {finding.environment} · {finding.clusterType} / {finding.technologyStack}</p>
        </div>
      </header>

      <section>
        <h3>为什么需要关注</h3>
        <p>{finding.explanation}</p>
      </section>
      <section>
        <h3>检查依据</h3>
        <ul>{finding.evidence.map((item) => <li key={item}>{item}</li>)}</ul>
        <small>数据来源：{finding.source} · 核验时间：{finding.checkedAt || '—'}</small>
      </section>
      <section className="ks-inspection-detail__recommendation">
        <h3>建议下一步</h3>
        <p>{finding.recommendation}</p>
      </section>
      <Button
        icon={<ArrowRightIcon />}
        iconPosition="end"
        onClick={() => finding.destination === 'capacity' ? onOpenCapacity(finding.clusterId) : onOpenCluster(finding.clusterId)}
        variant="primary"
      >
        {finding.destination === 'capacity' ? '查看容量证据' : '进入集群排查'}
      </Button>
    </aside>
  )
}

export default function FleetInspectionsPage({ findings, onOpenCapacity, onOpenCluster }: Props) {
  const [category, setCategory] = useState<'ALL' | InspectionCategory>('ALL')
  const [environment, setEnvironment] = useState('ALL')
  const [query, setQuery] = useState('')
  const [selectedID, setSelectedID] = useState(findings[0]?.id ?? '')
  const [severity, setSeverity] = useState<'ALL' | InspectionSeverity>('ALL')

  const environments = useMemo(() => ['ALL', ...new Set(findings.map((finding) => finding.environment))], [findings])
  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return findings.filter((finding) => (
      (severity === 'ALL' || finding.severity === severity)
      && (category === 'ALL' || finding.category === category)
      && (environment === 'ALL' || finding.environment === environment)
      && (!normalized || [finding.title, finding.clusterName, finding.resource, ...finding.evidence].some((value) => value.toLowerCase().includes(normalized)))
    ))
  }, [category, environment, findings, query, severity])

  const selected = visible.find((finding) => finding.id === selectedID) ?? visible[0]
  const critical = findings.filter((finding) => finding.severity === 'critical').length
  const warning = findings.filter((finding) => finding.severity === 'warning').length
  const info = findings.filter((finding) => finding.severity === 'info').length
  const actionable = critical + warning

  return (
    <div className="ks-inspections">
      <Alert isInline title="当前状态巡检" variant="info">
        第一版根据现有集群健康和容量快照实时生成问题，不新增采集链路。问题确认、忽略、恢复历史将在规则结果稳定后接入。
      </Alert>

      <div className="ks-inspection-summary" aria-label="巡检摘要">
        <Card className="is-total"><CardBody><span>当前问题</span><strong>{actionable}</strong><small>需要运维人员确认</small></CardBody></Card>
        <Card className="is-critical"><CardBody><span>严重</span><strong>{critical}</strong><small>优先处理可用性风险</small></CardBody></Card>
        <Card className="is-warning"><CardBody><span>警告</span><strong>{warning}</strong><small>可能影响稳定性或容量</small></CardBody></Card>
        <Card className="is-info"><CardBody><span>提示</span><strong>{info}</strong><small>数据不完整或待配置</small></CardBody></Card>
      </div>

      <Card className="ks-inspection-workspace">
        <CardHeader>
          <CardTitle>
            <Title headingLevel="h2" size="lg">巡检发现</Title>
            <p>同一问题只显示一次；集群状态和容量数据作为证据关联。</p>
          </CardTitle>
        </CardHeader>
        <Toolbar className="ks-inspection-toolbar">
          <ToolbarContent rowWrap={{ default: 'wrap' }}>
            <ToolbarGroup>
              <ToolbarItem className="ks-inspection-search">
                <SearchInput aria-label="搜索巡检发现" onChange={(_, value) => setQuery(value)} onClear={() => setQuery('')} placeholder="搜索问题、集群或证据" value={query} />
              </ToolbarItem>
              <ToolbarItem>
                <FormSelect aria-label="严重程度筛选" onChange={(_, value) => setSeverity(value as 'ALL' | InspectionSeverity)} value={severity}>
                  <FormSelectOption label="全部级别" value="ALL" />
                  {(Object.keys(inspectionSeverityLabels) as InspectionSeverity[]).map((value) => <FormSelectOption key={value} label={inspectionSeverityLabels[value]} value={value} />)}
                </FormSelect>
              </ToolbarItem>
              <ToolbarItem>
                <FormSelect aria-label="巡检分类筛选" onChange={(_, value) => setCategory(value as 'ALL' | InspectionCategory)} value={category}>
                  <FormSelectOption label="全部分类" value="ALL" />
                  {(Object.keys(inspectionCategoryLabels) as InspectionCategory[]).map((value) => <FormSelectOption key={value} label={inspectionCategoryLabels[value]} value={value} />)}
                </FormSelect>
              </ToolbarItem>
              <ToolbarItem>
                <FormSelect aria-label="巡检环境筛选" onChange={(_, value) => setEnvironment(value)} value={environment}>
                  {environments.map((value) => <FormSelectOption key={value} label={value === 'ALL' ? '全部环境' : value} value={value} />)}
                </FormSelect>
              </ToolbarItem>
            </ToolbarGroup>
            <ToolbarItem align={{ default: 'alignEnd' }}><span className="ks-inspection-count">显示 {visible.length} / {findings.length} 项</span></ToolbarItem>
          </ToolbarContent>
        </Toolbar>

        <div className="ks-inspection-layout">
          <div className="ks-inspection-table-wrap">
            <Table aria-label="巡检发现列表" variant="compact">
              <Thead><Tr><Th>级别</Th><Th>问题 / 集群</Th><Th>分类</Th><Th>资源范围</Th><Th>核验时间</Th><Th screenReaderText="选择问题" /></Tr></Thead>
              <Tbody>
                {visible.map((finding) => (
                  <Tr className={finding.id === selected?.id ? 'is-selected' : ''} isClickable key={finding.id} onRowClick={() => setSelectedID(finding.id)}>
                    <Td><Label color={severityMeta[finding.severity].color} isCompact>{inspectionSeverityLabels[finding.severity]}</Label></Td>
                    <Td><strong>{finding.title}</strong><small>{finding.clusterName} · {finding.environment}</small></Td>
                    <Td>{inspectionCategoryLabels[finding.category]}</Td>
                    <Td>{finding.resource}</Td>
                    <Td>{finding.checkedAt || '—'}</Td>
                    <Td isActionCell><Button aria-label={`查看问题 ${finding.title}`} icon={<ArrowRightIcon />} onClick={(event) => { event.stopPropagation(); setSelectedID(finding.id) }} variant="plain" /></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
            {visible.length === 0 && <div className="ks-inspection-empty"><SearchIcon /><Title headingLevel="h3" size="lg">没有匹配的问题</Title><p>调整筛选条件或清除搜索关键词。</p></div>}
          </div>
          {selected ? <FindingDetail finding={selected} findings={findings} onOpenCapacity={onOpenCapacity} onOpenCluster={onOpenCluster} /> : (
            <aside className="ks-inspection-detail is-empty"><CheckCircleIcon /><Title headingLevel="h2" size="lg">当前没有巡检问题</Title><p>平台会继续根据最新健康和容量快照进行检查。</p></aside>
          )}
        </div>
      </Card>
    </div>
  )
}
