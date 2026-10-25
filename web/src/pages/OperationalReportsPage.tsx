import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Checkbox, Label, Spinner } from '@patternfly/react-core'
import { ChartLineIcon, ExternalLinkAltIcon, FileAltIcon, SyncAltIcon } from '@patternfly/react-icons'
import {
  createReport, getReportModelConfig, listReports, reportPath,
  type OperationalReport, type ReportItem, type ReportPeriod,
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

function reportItem(value: OperationalReport): ReportItem {
  return { id: value.id, title: value.title, period: value.request.period, environment: value.request.environment, cluster_type: value.request.cluster_type, generated_at: value.generated_at, ai_status: value.ai_status, url: value.url }
}

function AIState({ status }: { status: string }) {
  if (status === 'completed' || status === 'success') return <Label color="blue">AI 总结</Label>
  if (status === 'failed' || status === 'fallback') return <Label color="orange">统计报告 · AI 未完成</Label>
  return <Label color="grey">统计报告</Label>
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
  const [busy, setBusy] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const [generationError, setGenerationError] = useState('')
  const [notice, setNotice] = useState('')
  const [generated, setGenerated] = useState<OperationalReport | null>(null)
  const [preview, setPreview] = useState<ReportItem | null>(null)
  const [filter, setFilter] = useState('')
  const [filterPeriod, setFilterPeriod] = useState('')
  const [aiReady, setAIReady] = useState(false)
  const [configLoading, setConfigLoading] = useState(canConfigure)
  const environments = useMemo(() => [...new Set(clusters.map(cluster => cluster.environment?.trim()).filter((value): value is string => Boolean(value)))].sort(), [clusters])
  const matchingClusters = clusters.filter(cluster => (!environment || cluster.environment === environment) && (!pool || (cluster.cluster_type ?? cluster.clusterType) === pool)).length
  const shown = items.filter(item => (!filterPeriod || item.period === filterPeriod) && `${item.title} ${item.environment} ${item.cluster_type}`.toLowerCase().includes(filter.trim().toLowerCase()))

  const refresh = useCallback(async () => {
    setLoading(true); setLoadError('')
    try { setItems(await listReports()) } catch (error) { setLoadError(message(error, '读取报告列表失败。')) } finally { setLoading(false) }
  }, [])

  useEffect(() => {
    let active = true
    void listReports().then(value => { if (active) setItems(value) }).catch(error => { if (active) setLoadError(message(error, '读取报告列表失败。')) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])

  useEffect(() => {
    if (!canConfigure) return
    let active = true
    void getReportModelConfig().then(value => { if (active) setAIReady(Boolean(value.endpoint && value.model)) }).catch(() => { if (active) setAIReady(false) }).finally(() => { if (active) setConfigLoading(false) })
    return () => { active = false }
  }, [canConfigure])

  useEffect(() => {
    if (!busy) return
    const timer = window.setInterval(() => setElapsed(value => value + 1), 1000)
    return () => window.clearInterval(timer)
  }, [busy])

  const generate = async () => {
    if (!canConfigure || busy) return
    setBusy(true); setElapsed(0); setGenerationError(''); setNotice(''); setGenerated(null)
    try {
      const value = await createReport({ period, date, environment, cluster_type: pool, use_ai: useAI })
      const item = reportItem(value)
      setGenerated(value); setPreview(item); setItems(current => [item, ...current.filter(existing => existing.id !== item.id)].slice(0, 100))
      setNotice('报告已生成并保存，可预览或通过链接直接访问。')
    } catch (error) { setGenerationError(message(error, '生成报告失败，请重试。')) } finally { setBusy(false) }
  }

  const copyLink = async (item: ReportItem) => {
    try { await navigator.clipboard.writeText(`${location.origin}${reportPath(item)}`); setNotice('报告链接已复制，访问时需要登录平台。') } catch { setNotice('无法复制链接，请使用“打开报告”后复制浏览器地址。') }
  }

  return <div className="ks-reports-page">
    <section className="ks-reports-intro" aria-label="运行报告说明"><div><ChartLineIcon /><p>按周期回看事件、集群健康、容量和安全扫描，生成可直接访问的图表报告。</p></div></section>
    {canConfigure && <section className="ks-reports-card" aria-labelledby="reports-create-title">
      <div className="ks-reports-card-head"><div><h2 id="reports-create-title">生成运行报告</h2><p>默认选择上一个完整自然周期，也可选择当前周期。</p></div><span className="ks-reports-scope">当前范围 {matchingClusters} 套集群</span></div>
      <form onSubmit={event => { event.preventDefault(); void generate() }}>
        <div className="ks-reports-create-fields"><label>报告周期<select value={period} disabled={busy} onChange={event => { const value = event.target.value as ReportPeriod; setPeriod(value); setDate(previousReportDate(value)) }}>{periods.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><label>周期内任意日期<input type="date" required value={date} max={shanghaiDay()} disabled={busy} onChange={event => setDate(event.target.value)} /></label><label>环境<select value={environment} disabled={busy} onChange={event => setEnvironment(event.target.value)}><option value="">全部环境</option>{environments.map(value => <option key={value}>{value}</option>)}</select></label><label>资源池<select value={pool} disabled={busy} onChange={event => setPool(event.target.value)}><option value="">全部资源池</option><option value="G01">G01（TCS / CMBK8S）</option><option value="OCP">OCP</option></select></label></div>
        <div className="ks-reports-create-footer"><div><strong>{reportRange(period, date)}</strong><small>按北京时间统计；当前周期统计到生成时刻。</small></div><div className="ks-reports-generate"><Checkbox id="reports-use-ai" label="使用 AI 总结" isChecked={useAI} isDisabled={!aiReady || configLoading || busy} onChange={(_event, checked) => setUseAI(checked)} /><Button type="submit" icon={<FileAltIcon />} isLoading={busy} isDisabled={busy || !date || date > shanghaiDay()}>{busy ? '正在生成' : `生成${periodName(period)}`}</Button></div></div>
        <p className="ks-reports-ai-hint">{aiReady ? 'AI 使用管理员在平台管理中配置的内网模型，对统计结果提炼重点和建议。' : <>模型尚未配置，当前可生成统计图表报告。需要 AI 总结时，请前往 <a href="/settings/access?tab=parameters&section=reports">管理员 · 平台管理 · AI 报告模型</a> 配置。</>}</p>
      </form>
      {busy && <div className="ks-reports-progress" role="status"><Spinner size="sm" /><span>正在汇总已保存的数据{useAI ? '并等待模型分析' : ''}… 已用 {elapsed} 秒</span></div>}
      {generationError && <div className="ks-reports-inline"><Alert variant="danger" isInline title={generationError} /></div>}
    </section>}
    {notice && <Alert variant="success" isInline title={notice} />}
    {generated?.ai_message && <Alert variant={generated.ai_status === 'failed' || generated.ai_status === 'fallback' ? 'warning' : 'info'} isInline title={generated.ai_message} />}
    <section className="ks-reports-card" aria-labelledby="reports-history-title">
      <div className="ks-reports-card-head"><div><h2 id="reports-history-title">报告记录 <span>{items.length}</span></h2><p>保留生成时的数据快照，列表展示最近 100 份报告。</p></div><Button variant="plain" aria-label="刷新报告列表" icon={<SyncAltIcon />} isDisabled={loading} onClick={() => void refresh()} /></div>
      <div className="ks-reports-list-filters"><label><span>搜索报告</span><input type="search" placeholder="按报告名称、环境或资源池搜索" value={filter} onChange={event => setFilter(event.target.value)} /></label><label><span>周期筛选</span><select value={filterPeriod} onChange={event => setFilterPeriod(event.target.value)}><option value="">全部周期</option>{periods.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><small>显示 {shown.length} 份</small></div>
      {loadError && <div className="ks-reports-inline"><Alert variant="danger" isInline title={loadError} /></div>}
      {loading && !items.length ? <div className="ks-reports-empty"><Spinner size="lg" /><p>正在读取报告…</p></div> : shown.length ? <div className="ks-reports-table-wrap"><table className="ks-reports-table"><thead><tr><th>报告</th><th>范围</th><th>分析方式</th><th>生成时间</th><th>操作</th></tr></thead><tbody>{shown.map(item => <tr key={item.id} className={preview?.id === item.id ? 'is-selected' : ''}><td><button className="ks-reports-title-link" onClick={() => setPreview(item)}>{item.title}</button><small>{periodName(item.period)}</small></td><td>{item.environment || '全部环境'}<small>{item.cluster_type || '全部资源池'}</small></td><td><AIState status={item.ai_status} /></td><td>{dateText(item.generated_at)}</td><td><div className="ks-reports-row-actions"><a href={reportPath(item)} target="_blank" rel="noopener noreferrer">打开报告 <ExternalLinkAltIcon /></a><Button variant="link" isInline onClick={() => setPreview(item)}>预览</Button><Button variant="link" isInline onClick={() => void copyLink(item)}>复制链接</Button></div></td></tr>)}</tbody></table></div> : <div className="ks-reports-empty"><FileAltIcon /><h3>{items.length ? '没有匹配的报告' : '还没有运行报告'}</h3><p>{items.length ? '调整搜索内容或周期筛选。' : canConfigure ? '选择周期和范围，生成第一份运行报告。' : '管理员生成报告后，可以在这里查看。'}</p></div>}
    </section>
    {preview && <section className="ks-reports-card ks-reports-preview" aria-labelledby="reports-preview-title"><div className="ks-reports-card-head"><div><h2 id="reports-preview-title">{preview.title}</h2><p>HTML 图表报告 · 访问链接需要登录平台</p></div><div className="ks-reports-row-actions"><a href={reportPath(preview)} target="_blank" rel="noopener noreferrer">新窗口打开 <ExternalLinkAltIcon /></a><Button variant="link" onClick={() => setPreview(null)}>关闭预览</Button></div></div><iframe key={preview.id} title="运行报告预览" src={reportPath(preview)} sandbox="allow-top-navigation-by-user-activation" /></section>}
  </div>
}
