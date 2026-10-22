import { Alert, Button, Card, CardBody, Spinner } from '@patternfly/react-core'
import { CheckCircleIcon, CogIcon, DatabaseIcon, JavaIcon, OutlinedBellIcon, SecurityIcon, TachometerAltIcon } from '@patternfly/react-icons'
import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchPlatformSettings, savePlatformSettings, type PlatformSettings, type SettingsApplyStatus, type SettingsSection } from '../api/settings'
import { usePermission } from '../auth/context'
import { validateSection, type FieldErrors } from './platformSettingsValidation'
import './PlatformSettingsPage.css'

const sectionMeta: Record<SettingsSection, { description: string; icon: typeof CogIcon; label: string }> = {
  runtime: { label: '接口与并发', description: '控制 API、实时会话和单集群访问的并发上限。', icon: TachometerAltIcon },
  collection: { label: '采集任务', description: '调整健康、容量和资源索引的采集频率。', icon: CogIcon },
  events: { label: '事件保留', description: '分级管理 Kubernetes Event 原始数据与汇总数据的保留时间。', icon: OutlinedBellIcon },
  security: { label: '安全扫描', description: '管理 x86/ARM kube-bench 镜像、扫描队列和超时。', icon: SecurityIcon },
  java: { label: 'Java 排障', description: '管理 Arthas 来源、诊断并发和大文件保护策略。', icon: JavaIcon },
  database: { label: '数据库运维', description: '配置备份策略、本地备份目录和磁盘阈值。', icon: DatabaseIcon },
}

const validSections = Object.keys(sectionMeta) as SettingsSection[]
type SaveNotice = { message: string; variant: 'success' | 'warning' | 'info' }

const noticeForApply = (apply: SettingsApplyStatus): SaveNotice => {
  if (apply.status === 'restarting') return { message: apply.message, variant: 'success' }
  if (apply.status === 'failed' || apply.status === 'restart_unavailable') return { message: apply.message, variant: 'warning' }
  return { message: apply.message, variant: 'info' }
}
const activeSectionFromPath = (): SettingsSection => {
  const querySection = new URLSearchParams(window.location.search).get('section')
  const candidate = (querySection || window.location.pathname.split('/')[2]) as SettingsSection
  return validSections.includes(candidate) ? candidate : 'runtime'
}

type FieldProps = { children?: React.ReactNode; error?: string; help: string; label: string; wide?: boolean }
function Field({ children, error, help, label, wide }: FieldProps) {
  return <label className={`ks-settings-field${wide ? ' is-wide' : ''}${error ? ' has-error' : ''}`}><span className="ks-settings-field-label">{label}</span><span className="ks-settings-field-control">{children}</span>{error && <span className="ks-settings-field-error" role="alert">{error}</span>}<small className="ks-settings-field-help"><span>作用</span>{help}</small></label>
}

function NumberInput({ value, onChange, min = 1, max, invalid }: { value: number; onChange: (value: number) => void; min?: number; max?: number; invalid?: boolean }) {
  return <input aria-invalid={invalid || undefined} max={max} min={min} onChange={(event) => onChange(Number(event.target.value))} type="number" value={value} />
}

type NumberFieldProps = { error?: string; help: string; label: string; max?: number; min?: number; onChange: (value: number) => void; value: number; wide?: boolean }
function NumberField({ error, help, label, max, min, onChange, value, wide }: NumberFieldProps) {
  return <Field error={error} help={help} label={label} wide={wide}><NumberInput invalid={Boolean(error)} max={max} min={min} onChange={onChange} value={value} /></Field>
}

type TextFieldProps = { error?: string; help: string; label: string; onChange: (value: string) => void; placeholder?: string; type?: 'text' | 'time'; value: string; wide?: boolean }
function TextField({ error, help, label, onChange, placeholder, type = 'text', value, wide }: TextFieldProps) {
  return <Field error={error} help={help} label={label} wide={wide}><input aria-invalid={Boolean(error) || undefined} placeholder={placeholder} type={type} value={value} onChange={(event) => onChange(event.target.value)} /></Field>
}

export default function PlatformSettingsPage({ embedded = false }: { embedded?: boolean }) {
  const canConfigure = usePermission('configure')
  const formScrollRef = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState<SettingsSection>(activeSectionFromPath)
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  const [draft, setDraft] = useState<PlatformSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState<SaveNotice | null>(null)

  useEffect(() => {
    const onPopState = () => setActive(activeSectionFromPath())
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  useEffect(() => {
    if (formScrollRef.current) formScrollRef.current.scrollTop = 0
  }, [active])

  useEffect(() => {
    let mounted = true
    void fetchPlatformSettings().then((value) => {
      if (!mounted) return
      setSettings(value); setDraft(structuredClone(value)); setError('')
    }).catch((cause) => { if (mounted) setError(cause instanceof Error ? cause.message : '读取配置失败。') }).finally(() => { if (mounted) setLoading(false) })
    return () => { mounted = false }
  }, [])

  const dirty = useMemo(() => settings && draft ? JSON.stringify(settings[active]) !== JSON.stringify(draft[active]) : false, [active, draft, settings])
  const errors = useMemo<FieldErrors>(() => draft ? validateSection(active, draft) : {}, [active, draft])
  const errorCount = Object.keys(errors).length
  const navigate = (section: SettingsSection) => {
    window.history.pushState({ settings: section }, '', `/settings/access?tab=parameters&section=${section}`)
    setActive(section); setNotice(null); setError('')
  }
  const update = <K extends SettingsSection>(section: K, patch: Partial<PlatformSettings[K]>) => {
    setDraft((current) => current ? { ...current, [section]: { ...current[section], ...patch } } : current)
  }
  const save = async () => {
    if (!draft || errorCount > 0) return
    setSaving(true); setError(''); setNotice(null)
    try {
      const result = await savePlatformSettings(active, draft[active])
      setSettings(result.settings); setDraft(structuredClone(result.settings)); setNotice(noticeForApply(result.apply))
      window.dispatchEvent(new CustomEvent('platform-settings-saved', { detail: result.settings }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存配置失败。')
    } finally { setSaving(false) }
  }

  if (loading) return <div className="ks-settings-loading"><Spinner aria-label="正在读取平台配置" /><span>正在读取平台配置…</span></div>
  if (!draft) return <Alert isInline title="配置页加载失败" variant="danger">{error}</Alert>
  const updatedAt = settings?.updated_at?.[active]
  const activeMeta = sectionMeta[active]
  const ActiveIcon = activeMeta.icon
  const fieldCount = Object.keys(draft[active]).length

  return <div className={`ks-settings-page${embedded ? ' is-embedded' : ''}`}>
    <aside className="ks-settings-nav" aria-label="参数配置分类">
      <strong>参数类别</strong>
      {validSections.map((section) => {
        const item = sectionMeta[section]; const Icon = item.icon
        return <button className={active === section ? 'is-active' : ''} key={section} onClick={() => navigate(section)}><Icon /><span><b>{item.label}</b><small>{item.description}</small></span></button>
      })}
    </aside>
    <main className="ks-settings-main">
      {error && <Alert isInline title="操作失败" variant="danger">{error}</Alert>}
      {notice && <Alert isInline title={notice.message} variant={notice.variant} />}
      {errorCount > 0 && <Alert isInline title={`有 ${errorCount} 项参数需要修正`} variant="danger">请修正下方标红的参数后再保存。</Alert>}
      <Card className="ks-settings-card">
        <CardBody className="ks-settings-card-body">
          <header className="ks-settings-section-head">
            <div className="ks-settings-heading">
              <span className="ks-settings-heading-icon"><CogIcon /></span>
              <div className="ks-settings-heading-text">
                <h2>功能参数</h2>
                <span className="ks-settings-heading-count">共 {fieldCount} 项参数</span>
              </div>
            </div>
            <div className="ks-settings-head-actions">
              <span className="ks-settings-current"><ActiveIcon />{activeMeta.label}</span>
              {dirty && <span className="ks-settings-dirty"><span aria-hidden="true" />未保存</span>}
            </div>
          </header>
          <div className="ks-settings-form" ref={formScrollRef}>{renderForm(active, draft, update, errors)}</div>
          {!canConfigure && <p className="ks-settings-readonly">当前账号只能查看配置，修改需要平台管理员权限。</p>}
          <footer className="ks-settings-footer"><span>{updatedAt ? <><CheckCircleIcon /> 上次保存：{new Date(updatedAt).toLocaleString('zh-CN', { hour12: false })}</> : '当前使用平台推荐默认值'}</span><Button isDisabled={!dirty || saving} onClick={() => settings && setDraft(structuredClone(settings))} variant="link">放弃修改</Button><Button isDisabled={!canConfigure || !dirty || errorCount > 0} isLoading={saving} onClick={() => void save()}>保存配置</Button></footer>
        </CardBody>
      </Card>
    </main>
  </div>
}

function renderForm(active: SettingsSection, value: PlatformSettings, update: <K extends SettingsSection>(section: K, patch: Partial<PlatformSettings[K]>) => void, errors: FieldErrors) {
  if (active === 'runtime') {
    const v = value.runtime
    return <>
      <NumberField error={errors.query_concurrency} help="集群、工作负载与事件查询共享的上限。" label="普通查询并发" max={1000} onChange={(n) => update('runtime', { query_concurrency: n })} value={v.query_concurrency} />
      <NumberField error={errors.realtime_concurrency} help="Pod Exec 与 kubectl TTY 等长连接上限。" label="实时通道并发" max={200} onChange={(n) => update('runtime', { realtime_concurrency: n })} value={v.realtime_concurrency} />
      <NumberField error={errors.monitoring_concurrency} help="Prometheus 查询的独立上限。" label="监控查询并发" max={100} onChange={(n) => update('runtime', { monitoring_concurrency: n })} value={v.monitoring_concurrency} />
      <NumberField error={errors.session_concurrency} help="平台管理操作会话上限。" label="会话并发" max={100} onChange={(n) => update('runtime', { session_concurrency: n })} value={v.session_concurrency} />
      <NumberField error={errors.per_cluster_concurrency} help="避免一套集群占用全部 API 配额。" label="单集群并发" max={20} onChange={(n) => update('runtime', { per_cluster_concurrency: n })} value={v.per_cluster_concurrency} />
      <NumberField error={errors.auto_refresh_seconds} help="保存后当前页面立即应用，无需重启。" label="集群列表自动刷新（秒）" max={600} min={10} onChange={(n) => update('runtime', { auto_refresh_seconds: n })} value={v.auto_refresh_seconds} />
    </>
  }

  if (active === 'collection') {
    const v = value.collection
    const groups = [
      ['health', '健康快照', 15, 3600, 5, 300, 64],
      ['capacity', '容量快照', 60, 86400, 5, 600, 32],
      ['resource', '资源索引', 60, 86400, 5, 600, 32],
    ] as const
    return <>{groups.map(([prefix, label, intervalMin, intervalMax, timeoutMin, timeoutMax, workerMax]) => <section className="ks-settings-group" key={prefix}>
      <h3>{label}</h3>
      <NumberField error={errors[`${prefix}_interval_seconds`]} help="两次后台采集的间隔。" label="采集周期（秒）" max={intervalMax} min={intervalMin} onChange={(n) => update('collection', { [`${prefix}_interval_seconds`]: n })} value={v[`${prefix}_interval_seconds`]} />
      <NumberField error={errors[`${prefix}_timeout_seconds`]} help="超时后保留上一份成功快照。" label="单次超时（秒）" max={timeoutMax} min={timeoutMin} onChange={(n) => update('collection', { [`${prefix}_timeout_seconds`]: n })} value={v[`${prefix}_timeout_seconds`]} />
      <NumberField error={errors[`${prefix}_workers`]} help="同时采集的集群数。" label="并发 Worker" max={workerMax} onChange={(n) => update('collection', { [`${prefix}_workers`]: n })} value={v[`${prefix}_workers`]} />
    </section>)}</>
  }

  if (active === 'events') {
    const v = value.events
    return <>
      <NumberField error={errors.normal_retention_days} help="普通事件数量大，建议短期保留。" label="Normal 事件保留（天）" max={365} onChange={(n) => update('events', { normal_retention_days: n })} value={v.normal_retention_days} />
      <NumberField error={errors.warning_retention_days} help="用于排障和历史对比。" label="Warning 事件保留（天）" max={730} onChange={(n) => update('events', { warning_retention_days: n })} value={v.warning_retention_days} />
      <NumberField error={errors.rollup_retention_days} help="支撑长期趋势分析，不保留全部原始明细。" label="小时汇总保留（天）" max={3650} min={30} onChange={(n) => update('events', { rollup_retention_days: n })} value={v.rollup_retention_days} />
      <NumberField error={errors.daily_retention_days} help="用于平台趋势与报表。" label="日汇总保留（天）" max={730} min={7} onChange={(n) => update('events', { daily_retention_days: n })} value={v.daily_retention_days} />
      <NumberField error={errors.cleanup_batch_size} help="每次删除的最大记录数，避免长事务。" label="清理批次" max={50000} min={100} onChange={(n) => update('events', { cleanup_batch_size: n })} value={v.cleanup_batch_size} />
    </>
  }

  if (active === 'security') {
    const v = value.security
    return <>
      <TextField error={errors.kube_bench_image} help="未指定架构专用镜像时使用。" label="通用 kube-bench 镜像" onChange={(s) => update('security', { kube_bench_image: s })} value={v.kube_bench_image} wide />
      <TextField help="内部镜像库的 amd64 镜像，留空使用通用镜像。" label="x86_64 镜像" onChange={(s) => update('security', { kube_bench_image_amd64: s })} value={v.kube_bench_image_amd64} wide />
      <TextField help="内部镜像库的 arm64 镜像，留空使用通用镜像。" label="ARM64 镜像" onChange={(s) => update('security', { kube_bench_image_arm64: s })} value={v.kube_bench_image_arm64} wide />
      <TextField error={errors.namespace} help="临时高权限 Job 所在的 Namespace。" label="扫描 Namespace" onChange={(s) => update('security', { namespace: s })} value={v.namespace} />
      <TextField help="内部镜像库拉取密钥名，公开镜像可留空。" label="ImagePullSecret" onChange={(s) => update('security', { image_pull_secret: s })} value={v.image_pull_secret} />
      <NumberField error={errors.scan_workers} help="同时执行扫描的集群数。" label="集群扫描并发" max={20} onChange={(n) => update('security', { scan_workers: n })} value={v.scan_workers} />
      <NumberField error={errors.host_workers} help="单集群内同时扫描的节点数。" label="节点 Job 并发" max={50} onChange={(n) => update('security', { host_workers: n })} value={v.host_workers} />
      <NumberField error={errors.scan_timeout_minutes} help="超时会标记失败并回收 Job。" label="总扫描超时（分钟）" max={240} min={5} onChange={(n) => update('security', { scan_timeout_minutes: n })} value={v.scan_timeout_minutes} />
      <NumberField error={errors.pod_startup_timeout_minutes} help="用于识别镜像拉取或调度失败。" label="Pod 启动超时（分钟）" max={30} onChange={(n) => update('security', { pod_startup_timeout_minutes: n })} value={v.pod_startup_timeout_minutes} />
    </>
  }

  if (active === 'java') {
    const v = value.java
    return <>
      <TextField error={errors.arthas_url} help="生产离线环境建议使用内部 HTTP 文件地址。" label="Arthas 离线包地址" onChange={(s) => update('java', { arthas_url: s })} placeholder="http://repo.internal/tools/arthas-bin.zip" value={v.arthas_url} wide />
      <TextField help="用于报告和下载包校验。" label="Arthas 版本" onChange={(s) => update('java', { arthas_version: s })} value={v.arthas_version} />
      <TextField error={errors.arthas_sha256} help="可选；填写后校验离线包完整性。" label="SHA-256" onChange={(s) => update('java', { arthas_sha256: s })} value={v.arthas_sha256} wide />
      <NumberField error={errors.max_concurrency} help="同时运行的 Java 诊断任务数。" label="诊断并发" max={20} onChange={(n) => update('java', { max_concurrency: n })} value={v.max_concurrency} />
      <NumberField error={errors.diagnostic_timeout_minutes} help="到期后终止诊断会话。" label="诊断超时（分钟）" max={240} min={5} onChange={(n) => update('java', { diagnostic_timeout_minutes: n })} value={v.diagnostic_timeout_minutes} />
      <NumberField error={errors.temporary_retention_hours} help="诊断产物的默认清理时间。" label="临时文件保留（小时）" max={168} onChange={(n) => update('java', { temporary_retention_hours: n })} value={v.temporary_retention_hours} />
      <Field help="高风险大文件操作，建议默认关闭。" label="Heap Dump"><span className="ks-settings-check"><input checked={v.heap_dump_enabled} onChange={(e) => update('java', { heap_dump_enabled: e.target.checked })} type="checkbox" />允许生成 Heap Dump</span></Field>
      <NumberField error={errors.heap_dump_max_gib} help="超过上限时拒绝执行。" label="Heap Dump 上限（GiB）" max={100} onChange={(n) => update('java', { heap_dump_max_gib: n })} value={v.heap_dump_max_gib} />
    </>
  }

  const v = value.database
  return <>
    <Field help="启用前必须设置备份节点与绝对路径。" label="定时备份"><span className="ks-settings-check"><input checked={v.backup_enabled} onChange={(e) => update('database', { backup_enabled: e.target.checked })} type="checkbox" />启用 MySQL 定时备份</span></Field>
    <TextField error={errors.backup_time} help="按平台所在时区执行。" label="每日备份时间" onChange={(s) => update('database', { backup_time: s })} type="time" value={v.backup_time} />
    <NumberField error={errors.backup_retention_days} help="到期文件由备份执行器清理。" label="备份保留（天）" max={365} onChange={(n) => update('database', { backup_retention_days: n })} value={v.backup_retention_days} />
    <TextField error={errors.backup_target_node} help="使用本地磁盘时固定调度到该节点。" label="备份目标节点" onChange={(s) => update('database', { backup_target_node: s })} value={v.backup_target_node} />
    <TextField error={errors.backup_target_path} help="例如 /data/ftc-platform/mysql-backup，不要与 MySQL 数据目录相同。" label="备份绝对路径" onChange={(s) => update('database', { backup_target_path: s })} value={v.backup_target_path} wide />
    <NumberField error={errors.space_warning_percent} help="超过后在容量和巡检页提示。" label="磁盘告警阈值（%）" max={95} min={50} onChange={(n) => update('database', { space_warning_percent: n })} value={v.space_warning_percent} />
    <NumberField error={errors.space_critical_percent} help="必须高于告警阈值。" label="磁盘严重阈值（%）" max={99} min={60} onChange={(n) => update('database', { space_critical_percent: n })} value={v.space_critical_percent} />
    <NumberField error={errors.connection_check_seconds} help="检查 MySQL 可用性的间隔。" label="连接检查周期（秒）" max={600} min={10} onChange={(n) => update('database', { connection_check_seconds: n })} value={v.connection_check_seconds} />
  </>
}
