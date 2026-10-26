import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Checkbox, Label, Modal, ModalBody, ModalFooter, ModalHeader, Spinner } from '@patternfly/react-core'
import { ChartLineIcon, ExclamationCircleIcon, ExclamationTriangleIcon, ExternalLinkAltIcon, FileAltIcon, SyncAltIcon, TrashIcon } from '@patternfly/react-icons'
import {
  createReport, deleteReport, getReportModelConfig, listReportJobs, listReports, reportPath,
  type ReportItem, type ReportJob, type ReportPeriod,
} from '../api/reports'
import { usePermission } from '../auth/context'
import { previousReportDate, reportRange, shanghaiDay } from './operationalReportsDates'
import './OperationalReportsPage.css'

type Props = { clusters: { id: string; name: string; environment?: string; cluster_type?: string; clusterType?: string }[] }
const periods: { value: ReportPeriod; label: string }[] = [{ value: 'weekly', label: '周报' }, { value: 'monthly', label: '月报' }, { value: 'quarterly', label: '季报' }]
const periodName = (period: string) => periods.find(item => item.value === period)?.label ?? '报告'
const dateText = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
}
const message = (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback

function AIState({ status }: { status: string }) {
  if (status === 'completed' || status === 'success') return <Label color="blue">AI 总结</Label>
  if (status === 'failed' || status === 'fallback') return <Label color="orange" title="AI 分析未完成，打开报告可以看到具体原因。" >统计报告 · AI 未完成</Label>
  return <Label color="grey" title="该报告未启用 AI 总结，仅包含统计图表与规则建议。">统计报告</Label>
}

export default function OperationalReportsPage({ clusters }: Props) {
  const canConfigure = usePermission('configure')
  const [items, setItems] = useState<ReportItem[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [period, setPeriod] = useState<ReportPeriod>('weekly')
  const [date, setDate] = useState(() => previousReportDate('weekly'))
  const [environment, setEnvironment] = useState('')
  const [pool, setPool] = useState('')
  const [useAI, setUseAI] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [jobs, setJobs] = useState<ReportJob[]>([])
  const [generationError, setGenerationError] = useState('')
  const [notice, setNotice] = useState('')
  const [filter, setFilter] = useState('')
  const [filterPeriod, setFilterPeriod] = useState('')
  const [aiReady, setAIReady] = useState(false)
  const [configLoading, setConfigLoading] = useState(canConfigure)
  const [deleteTarget, setDeleteTarget] = useState<ReportItem | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const environments = useMemo(() => [...new Set(clusters.map(cluster => cluster.environment?.trim()).filter((value): value is string => Boolean(value)))].sort(), [clusters])
  const matchingClusters = clusters.filter(cluster => (!environment || cluster.environment === environment) && (!pool || (cluster.cluster_type ?? cluster.clusterType) === pool)).length
  const shown = items.filter(item => (!filterPeriod || item.period === filterPeriod) && `${item.title} ${item.environment} ${item.cluster_type}`.toLowerCase().includes(filter.trim().toLowerCase()))
  // A finished job duplicates the row that already exists in 报告记录, so it is
  // dropped here. Two exceptions still deserve space: work in flight, and an AI
  // section that was dropped (the report row alone would not say why). Only the
  // newest of those is actionable; older ones are just history.
  const liveJobs = useMemo(() => {
    const inFlight = jobs.filter(job => job.status !== 'completed')
    // Do not keep an old model warning pinned after a newer report succeeds.
    // Jobs arrive newest-first; only the latest completed run represents the
    // current model state.
    const newestCompleted = jobs.find(job => job.status === 'completed')
    const droppedAI = newestCompleted?.error_message ? newestCompleted : undefined
    return [...inFlight, ...(droppedAI ? [droppedAI] : [])]
      .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at))
      .slice(0, 5)
  }, [jobs])
  const activeCount = liveJobs.filter(job => job.status === 'queued' || job.status === 'running').length

  const refresh = useCallback(async () => {
    setLoading(true); setLoadError('')
    try {
      const [reportItems, reportJobs] = await Promise.all([listReports(), canConfigure ? listReportJobs() : Promise.resolve([])])
      setItems(reportItems); setJobs(reportJobs)
    } catch (error) { setLoadError(message(error, '读取报告列表失败。')) } finally { setLoading(false) }
  }, [canConfigure])

  useEffect(() => {
    let active = true
    void Promise.all([listReports(), canConfigure ? listReportJobs() : Promise.resolve([])]).then(([reportItems, reportJobs]) => { if (active) { setItems(reportItems); setJobs(reportJobs) } }).catch(error => { if (active) setLoadError(message(error, '读取报告列表失败。')) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [canConfigure])

  useEffect(() => {
    if (!canConfigure) return
    let active = true
    void getReportModelConfig().then(value => { if (active) setAIReady(Boolean(value.endpoint && value.model)) }).catch(() => { if (active) setAIReady(false) }).finally(() => { if (active) setConfigLoading(false) })
    return () => { active = false }
  }, [canConfigure])

  useEffect(() => {
    if (!canConfigure || !jobs.some(job => job.status === 'queued' || job.status === 'running')) return
    const timer = window.setInterval(() => { void Promise.all([listReportJobs(), listReports()]).then(([reportJobs, reportItems]) => { setJobs(reportJobs); setItems(reportItems) }).catch(() => undefined) }, 2000)
    return () => window.clearInterval(timer)
  }, [canConfigure, jobs])

  const generate = async () => {
    if (!canConfigure || submitting) return
    setSubmitting(true); setGenerationError(''); setNotice('')
    try {
      const job = await createReport({ period, date, environment, cluster_type: pool, use_ai: useAI })
      setJobs(current => [job, ...current.filter(item => item.id !== job.id)].slice(0, 20))
      setNotice('报告任务已提交到后台，可以离开本页；完成后会出现在报告记录中。')
    } catch (error) { setGenerationError(message(error, '提交报告任务失败，请重试。')) } finally { setSubmitting(false) }
  }

  const copyLink = async (item: ReportItem) => {
    try { await navigator.clipboard.writeText(`${location.origin}${reportPath(item)}`); setNotice('报告链接已复制，访问时需要登录平台。') } catch { setNotice('无法复制链接，请使用“打开报告”后复制浏览器地址。') }
  }

  const removeReport = async () => {
    if (!deleteTarget || deleting) return
    setDeleting(true); setDeleteError('')
    try {
      await deleteReport(deleteTarget.id)
      setItems(current => current.filter(item => item.id !== deleteTarget.id))
      setNotice('报告已删除。')
      setDeleteTarget(null)
    } catch (error) { setDeleteError(message(error, '删除报告失败。')) } finally { setDeleting(false) }
  }

  return <div className="ks-reports-page">
    <section className="ks-reports-intro" aria-label="运行报告说明"><div><ChartLineIcon /><p>按周期回看事件、集群健康、容量和安全扫描，生成可直接访问的图表报告。</p></div></section>
    {canConfigure && <section className="ks-reports-card" aria-labelledby="reports-create-title">
      <div className="ks-reports-card-head"><div><h2 id="reports-create-title">生成运行报告</h2><p>默认选择上一个完整自然周期，也可选择当前周期。</p></div><span className="ks-reports-scope">当前范围 {matchingClusters} 套集群</span></div>
      <form onSubmit={event => { event.preventDefault(); void generate() }}>
        <div className="ks-reports-create-fields"><label>报告周期<select value={period} disabled={submitting} onChange={event => { const value = event.target.value as ReportPeriod; setPeriod(value); setDate(previousReportDate(value)) }}>{periods.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><label>周期内任意日期<input type="date" required value={date} max={shanghaiDay()} disabled={submitting} onChange={event => setDate(event.target.value)} /></label><label>环境<select value={environment} disabled={submitting} onChange={event => setEnvironment(event.target.value)}><option value="">全部环境</option>{environments.map(value => <option key={value}>{value}</option>)}</select></label><label>资源池<select value={pool} disabled={submitting} onChange={event => setPool(event.target.value)}><option value="">全部资源池</option><option value="G01">G01（TCS / CMBK8S）</option><option value="OCP">OCP</option></select></label></div>
        <div className="ks-reports-create-footer"><div><strong>{reportRange(period, date)}</strong><small>按北京时间统计；当前周期统计到生成时刻。</small></div><div className="ks-reports-generate"><Checkbox id="reports-use-ai" label="使用 AI 总结" isChecked={useAI} isDisabled={!aiReady || configLoading || submitting} onChange={(_event, checked) => setUseAI(checked)} /><Button type="submit" icon={<FileAltIcon />} isLoading={submitting} isDisabled={submitting || !date || date > shanghaiDay()}>{submitting ? '正在提交' : `后台生成${periodName(period)}`}</Button></div></div>
        <p className="ks-reports-ai-hint">{aiReady ? 'AI 使用管理员在平台管理中配置的内网模型，对统计结果提炼重点和建议。' : <>模型尚未配置，当前可生成统计图表报告。需要 AI 总结时，请前往 <a href="/settings/access?tab=parameters&section=reports">管理员 · 平台管理 · AI 报告模型</a> 配置。</>}</p>
      </form>
      {generationError && <div className="ks-reports-inline"><Alert variant="danger" isInline title={generationError} /></div>}
    </section>}
    {notice && <Alert variant="success" isInline title={notice} />}
    {canConfigure && liveJobs.length > 0 && <section className="ks-reports-card" aria-labelledby="reports-jobs-title"><div className="ks-reports-card-head"><div><h2 id="reports-jobs-title">后台任务 <span>{activeCount}</span></h2><p>只显示正在执行或失败的任务；生成完成的报告直接进入下方报告记录。</p></div></div><div className="ks-reports-jobs">{liveJobs.map(job => {
              // A completed job can still carry a model failure: the facts were
              // saved, only the AI section was dropped.
              const aiIncomplete = job.status === 'completed' && Boolean(job.error_message)
              return <div className={`ks-reports-job is-${job.status}${aiIncomplete ? ' is-partial' : ''}`} key={job.id}><div>{job.status === 'running' ? <Spinner size="sm" /> : <span className="ks-reports-job-dot" />}<strong>{periodName(job.request.period)} · {job.request.environment || '全部环境'} · {job.request.cluster_type || '全部资源池'}</strong><Label color={job.status === 'failed' ? 'red' : job.status === 'running' ? 'blue' : 'grey'}>{job.status === 'queued' ? '排队中' : job.status === 'running' ? '执行中' : job.status === 'completed' ? '已完成' : '失败'}</Label>{aiIncomplete && <Label color="orange">AI 未完成</Label>}</div><p>{job.message}</p>{job.error_message && <p className={`ks-reports-job-reason${aiIncomplete ? ' is-ai' : ''}`}>{aiIncomplete ? <ExclamationTriangleIcon /> : <ExclamationCircleIcon />}<span>{job.error_message}</span>{canConfigure && <a href="/settings/access?tab=parameters&section=reports">前往模型配置</a>}</p>}{job.report_id && <a className="ks-reports-job-open" href={reportPath({ id: job.report_id })} target="_blank" rel="noopener noreferrer">查看报告 <ExternalLinkAltIcon /></a>}</div>
            })}</div></section>}
    <section className="ks-reports-card" aria-labelledby="reports-history-title">
      <div className="ks-reports-card-head"><div><h2 id="reports-history-title">报告记录 <span>{items.length}</span></h2><p>保留生成时的数据快照，列表展示最近 100 份报告。</p></div><Button variant="plain" aria-label="刷新报告列表" icon={<SyncAltIcon />} isDisabled={loading} onClick={() => void refresh()} /></div>
      <div className="ks-reports-list-filters"><label><span>搜索报告</span><input type="search" placeholder="按报告名称、环境或资源池搜索" value={filter} onChange={event => setFilter(event.target.value)} /></label><label><span>周期筛选</span><select value={filterPeriod} onChange={event => setFilterPeriod(event.target.value)}><option value="">全部周期</option>{periods.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><small>显示 {shown.length} 份</small></div>
      {loadError && <div className="ks-reports-inline"><Alert variant="danger" isInline title={loadError} /></div>}
      {loading && !items.length ? <div className="ks-reports-empty"><Spinner size="lg" /><p>正在读取报告…</p></div> : shown.length ? <div className="ks-reports-table-wrap"><table className="ks-reports-table"><thead><tr><th>报告</th><th>范围</th><th>分析方式</th><th>生成时间</th><th>操作</th></tr></thead><tbody>{shown.map(item => <tr key={item.id}><td><a className="ks-reports-title-link" href={reportPath(item)} target="_blank" rel="noopener noreferrer">{item.title}</a><small>{periodName(item.period)}</small></td><td>{item.environment || '全部环境'}<small>{item.cluster_type || '全部资源池'}</small></td><td><AIState status={item.ai_status} /></td><td>{dateText(item.generated_at)}</td><td><div className="ks-reports-row-actions"><a href={reportPath(item)} target="_blank" rel="noopener noreferrer">打开报告 <ExternalLinkAltIcon /></a><Button variant="link" isInline onClick={() => void copyLink(item)}>复制链接</Button>{canConfigure && <Button variant="link" isInline isDanger icon={<TrashIcon />} onClick={() => { setDeleteTarget(item); setDeleteError('') }}>删除</Button>}</div></td></tr>)}</tbody></table></div> : <div className="ks-reports-empty"><FileAltIcon /><h3>{items.length ? '没有匹配的报告' : '还没有运行报告'}</h3><p>{items.length ? '调整搜索内容或周期筛选。' : canConfigure ? '选择周期和范围，生成第一份运行报告。' : '管理员生成报告后，可以在这里查看。'}</p></div>}
    </section>
    <Modal aria-labelledby="delete-report-title" isOpen={deleteTarget !== null} onClose={() => { if (!deleting) { setDeleteTarget(null); setDeleteError('') } }} variant="small">
      <ModalHeader description="删除后无法恢复，历史报告链接也将失效。" labelId="delete-report-title" title="确认删除报告" titleIconVariant="warning" />
      <ModalBody>
        {deleteError && <Alert isInline title="删除失败" variant="danger">{deleteError}</Alert>}
        <p>即将删除报告 <strong>{deleteTarget?.title}</strong>（{deleteTarget ? periodName(deleteTarget.period) : ''}）。</p>
      </ModalBody>
      <ModalFooter>
        <Button isLoading={deleting} isDanger onClick={() => void removeReport()} variant="danger">确认删除</Button>
        <Button isDisabled={deleting} onClick={() => { setDeleteTarget(null); setDeleteError('') }} variant="link">取消</Button>
      </ModalFooter>
    </Modal>
  </div>
}
