import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  CardBody,
  Form,
  FormGroup,
  FormSelect,
  FormSelectOption,
  Page,
  PageSection,
  Spinner,
  TextInput,
  Title,
} from '@patternfly/react-core'
import { PlatformBrand } from '../components/PlatformBrand'
import './SetupPage.css'

type ApiResponse<T> = { data: T | null; error: { code: string; message: string } | null }

type PreflightNode = {
  name: string
  arch: string
  os: string
  cpu_millis: number
  memory_bytes: number
  internal_ips: string[]
  schedulable: boolean
  taints?: string[]
}

type PreflightIngressClass = {
  name: string
  default: boolean
  controller: string
}

type Preflight = {
  namespace: string
  nodes: PreflightNode[]
  ingress_classes: PreflightIngressClass[]
  runtime_secret_exists: boolean
  warnings: string[]
  errors: string[]
}

type InstallStep = {
  key: string
  label: string
  status: 'pending' | 'running' | 'done' | 'failed'
  message?: string
}

type InstallStatus = {
  state: 'idle' | 'running' | 'completed' | 'failed'
  steps: InstallStep[]
  error?: string
  url?: string
  ca_url?: string
  ca_cert?: string
  hosts?: string[]
  permissions_revoked?: boolean
}

type ExternalDatabase = {
  host: string
  port: string
  database: string
  username: string
  password: string
  tls: string
}

const setupSteps = ['环境检查', '外部 MySQL', '访问配置', '确认安装']

async function readResponse<T>(response: Response, fallback: string): Promise<T> {
  let payload: ApiResponse<T>
  try {
    payload = await response.json() as ApiResponse<T>
  } catch {
    throw new Error('平台返回了无法识别的响应。')
  }
  if (!response.ok || payload.error) {
    throw new Error(payload.error?.message ?? fallback)
  }
  return payload.data as T
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  return readResponse<T>(response, '初始化请求失败。')
}

async function getInstallStatus(token: string): Promise<InstallStatus> {
  const response = await fetch('/api/v1/setup/install', { headers: { 'X-Setup-Token': token } })
  return readResponse<InstallStatus>(response, '读取安装进度失败。')
}

const formatMemory = (bytes: number) => `${Math.round(bytes / 1024 / 1024 / 1024)} GiB`
const formatCPU = (millis: number) => `${(millis / 1000).toFixed(millis % 1000 === 0 ? 0 : 1)} 核`

const domainPattern = /^(?=.{1,253}$)([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z0-9]([-a-z0-9]*[a-z0-9])?$/

export default function SetupPage() {
  const [step, setStep] = useState(1)
  const [token, setToken] = useState('')
  const [preflight, setPreflight] = useState<Preflight | null>(null)
  const [checking, setChecking] = useState(false)
  const [database, setDatabase] = useState<ExternalDatabase>({
    host: '',
    port: '3306',
    database: 'ftc_platform',
    username: 'ftc',
    password: '',
    tls: 'disabled',
  })
  const [testingDatabase, setTestingDatabase] = useState(false)
  const [databaseTestResult, setDatabaseTestResult] = useState('')
  const [domain, setDomain] = useState('')
  const [ingressClass, setIngressClass] = useState('')
  const [platformImage, setPlatformImage] = useState('')
  const [platformPullSecret, setPlatformPullSecret] = useState('')
  const [adminUsername, setAdminUsername] = useState('admin')
  const [adminPassword, setAdminPassword] = useState('')
  const [adminConfirm, setAdminConfirm] = useState('')
  const [installing, setInstalling] = useState(false)
  const [installStatus, setInstallStatus] = useState<InstallStatus | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    if (installStatus?.state !== 'completed' || installStatus.permissions_revoked || !token) return
    const timer = window.setTimeout(() => {
      void getInstallStatus(token).then(setInstallStatus).catch(() => {})
    }, 35000)
    return () => window.clearTimeout(timer)
  }, [installStatus?.permissions_revoked, installStatus?.state, token])

  useEffect(() => {
    if (!installing || !token) return
    let active = true
    const timer = window.setInterval(() => {
      void getInstallStatus(token).then(next => {
        if (!active) return
        setInstallStatus(next)
        if (next.state === 'completed' || next.state === 'failed') setInstalling(false)
      }).catch(cause => {
        if (!active) return
        setInstalling(false)
        setError(cause instanceof Error ? cause.message : '读取安装进度失败。')
      })
    }, 2500)
    return () => { active = false; window.clearInterval(timer) }
  }, [installing, token])

  const usableIngressClasses = preflight?.ingress_classes ?? []
  const preflightErrors = preflight?.errors ?? []
  const preflightWarnings = preflight?.warnings ?? []

  const runPreflight = async () => {
    if (!token.trim()) { setError('请先填写初始化凭证。'); return }
    setChecking(true); setError('')
    try {
      const result = await post<Preflight>('/api/v1/setup/preflight', { token })
      setPreflight(result)
      const defaultClass = result.ingress_classes.find(item => item.default)?.name || result.ingress_classes[0]?.name || ''
      setIngressClass(current => current || defaultClass)
      if (result.errors.length > 0) setError('环境检查未通过，请先处理下方问题。')
    } catch (cause) {
      setPreflight(null)
      setError(cause instanceof Error ? cause.message : '环境检查失败。')
    } finally {
      setChecking(false)
    }
  }

  const databaseError = useMemo(() => {
    if (!database.host.trim()) return '请填写外部 MySQL 地址。'
    const port = Number(database.port)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return 'MySQL 端口必须在 1–65535 之间。'
    if (!/^[a-zA-Z0-9_]+$/.test(database.database.trim())) return '数据库名只能包含字母、数字和下划线。'
    if (!/^[a-zA-Z0-9_]+$/.test(database.username.trim())) return '数据库用户名只能包含字母、数字和下划线。'
    if (!database.password) return '请填写数据库密码。'
    return ''
  }, [database])

  const accessError = useMemo(() => {
    if (!domainPattern.test(domain.trim().toLowerCase())) return '请输入合法的内部域名，例如 ftc-platform.cmb.local。'
    if (!ingressClass) return '请选择 IngressClass。'
    if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$/.test(adminUsername)) return '管理员用户名格式无效。'
    if (adminPassword.length < 12 || adminPassword.length > 72) return '管理员密码需要 12–72 字节。'
    if (adminPassword !== adminConfirm) return '两次管理员密码不一致。'
    return ''
  }, [adminConfirm, adminPassword, adminUsername, domain, ingressClass])

  const canContinue = step === 1
    ? Boolean(preflight) && preflightErrors.length === 0 && !preflight?.runtime_secret_exists
    : step === 2
      ? !databaseError
      : step === 3
        ? !accessError
        : true

  const next = () => {
    setError('')
    if (step === 1 && preflightErrors.length > 0) { setError('请先处理环境检查中的错误。'); return }
    if (step === 1 && preflight?.runtime_secret_exists) { setError('平台已经初始化，不能重复安装。'); return }
    if (step === 2 && databaseError) { setError(databaseError); return }
    if (step === 3 && accessError) { setError(accessError); return }
    setStep(current => Math.min(4, current + 1))
  }

  const back = () => { setError(''); setStep(current => Math.max(1, current - 1)) }

  const downloadCA = () => {
    const pem = installStatus?.ca_cert
    if (!pem) return
    const url = URL.createObjectURL(new Blob([pem], { type: 'application/x-pem-file' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'ftc-platform-ca.crt'
    anchor.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const testDatabase = async () => {
    setError('')
    setDatabaseTestResult('')
    if (databaseError) { setError(databaseError); return }
    setTestingDatabase(true)
    try {
      await post('/api/v1/setup/test', {
        token,
        mode: 'external',
        host: database.host.trim(),
        port: database.port.trim(),
        database: database.database.trim(),
        user: database.username.trim(),
        password: database.password,
        tls: database.tls,
      })
      setDatabaseTestResult('连接成功，可以继续。')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '数据库连接失败。')
    } finally {
      setTestingDatabase(false)
    }
  }

  const startInstall = async () => {
    setError('')
    if (databaseError) { setError(databaseError); return }
    if (accessError) { setError(accessError); return }
    setInstalling(true)
    setInstallStatus(null)
    try {
      const result = await post<InstallStatus>('/api/v1/setup/install', {
        token,
        domain: domain.trim().toLowerCase(),
        ingress_class: ingressClass,
        external_database: {
          host: database.host.trim(),
          port: database.port.trim(),
          database: database.database.trim(),
          username: database.username.trim(),
          password: database.password,
          tls: database.tls,
        },
        admin_username: adminUsername,
        admin_password: adminPassword,
        platform_image: platformImage.trim(),
        platform_image_pull_secret: platformPullSecret.trim(),
        api_replicas: 1,
        collector_replicas: 1,
        worker_replicas: 1,
      })
      setInstallStatus(result)
    } catch (cause) {
      setInstalling(false)
      setError(cause instanceof Error ? cause.message : '启动安装失败。')
    }
  }

  const completed = installStatus?.state === 'completed'
  const failed = installStatus?.state === 'failed'

  return <Page className="ks-setup-page">
    <PageSection className="ks-setup-shell">
      <div className="ks-setup-brand"><PlatformBrand variant="login" /></div>
      <Card className="ks-setup-card">
        <CardBody className="ks-setup-card-body">
          <header className="ks-setup-head">
            <div>
              <span className="ks-setup-eyebrow">平台初始化</span>
              <Title headingLevel="h1" size="2xl">准备部署 FTC 集群运维平台</Title>
              <p className="ks-setup-lead">完成环境检查、外部 MySQL、内部域名和管理员配置。初始化结束后即可通过 HTTPS 登录。</p>
            </div>
            <span className={`ks-setup-mode${preflight ? ' is-ready' : ''}`}>{preflight ? '环境已检查' : '等待初始化'}</span>
          </header>

          <ol className="ks-setup-steps" aria-label="初始化步骤">
            {setupSteps.map((label, index) => <li className={step === index + 1 ? 'is-active' : step > index + 1 ? 'is-done' : ''} key={label}>
              <span className="ks-setup-step-number">{step > index + 1 ? '✓' : index + 1}</span>
              <span>{label}</span>
            </li>)}
          </ol>

          <div className="ks-setup-content">
          {error && <Alert isInline title={error} variant="danger" />}

          {step === 1 && <>
            <section className="ks-setup-panel">
              <h2>检查运行环境</h2>
              <p className="ks-setup-panel-lead">初始化令牌由引导 Job 自动生成。平台会检查节点、IngressClass 和是否已经初始化。</p>
              <Form onSubmit={event => { event.preventDefault(); void runPreflight() }}>
                <FormGroup label="初始化凭证" isRequired fieldId="setup-token">
                  <TextInput aria-label="初始化凭证" id="setup-token" type="password" value={token} onChange={(_, value) => setToken(value)} isDisabled={checking || installing} />
                  <small className="ks-setup-hint">读取命令：kubectl -n ftc-platform logs job/ftc-platform-bootstrap</small>
                </FormGroup>
                <div className="ks-setup-inline-actions">
                  <Button type="submit" variant="secondary" isLoading={checking} isDisabled={checking || installing}>检查环境</Button>
                </div>
              </Form>
            </section>

            {preflight && <section className="ks-setup-report" aria-live="polite">
              <div className="ks-setup-report-head"><h3>检查结果</h3><span>{preflight.namespace}</span></div>
              {preflight.errors.length > 0 && <Alert isInline title="环境未就绪" variant="danger"><ul className="ks-setup-list">{preflight.errors.map(item => <li key={item}>{item}</li>)}</ul></Alert>}
              {preflightWarnings.length > 0 && <Alert isInline title="需要关注" variant="warning"><ul className="ks-setup-list">{preflightWarnings.map(item => <li key={item}>{item}</li>)}</ul></Alert>}
              <div className="ks-setup-report-grid">
                <div className="ks-setup-report-card">
                  <h4>节点 <span>{preflight.nodes.length}</span></h4>
                  {preflight.nodes.map(node => <div className="ks-setup-node" key={node.name}>
                    <b>{node.name}</b>
                    <span>{node.arch} · {formatCPU(node.cpu_millis)} · {formatMemory(node.memory_bytes)}</span>
                    <small>{node.internal_ips.join(', ') || '无内网 IP'}{node.schedulable ? '' : ' · 不可调度'}</small>
                  </div>)}
                </div>
                <div className="ks-setup-report-card">
                  <h4>Ingress 与状态</h4>
                  <p><b>IngressClass：</b>{preflight.ingress_classes.length ? preflight.ingress_classes.map(item => `${item.name}${item.default ? '（默认）' : ''}`).join('、') : '未发现'}</p>
                  <p><b>已有运行 Secret：</b>{preflight.runtime_secret_exists ? '是' : '否'}</p>
                  <p><b>MySQL：</b>下一步填写外部 MySQL 连接信息</p>
                </div>
              </div>
            </section>}
          </>}

          {step === 2 && <>
            <section className="ks-setup-panel">
              <h2>配置外部 MySQL</h2>
              <p className="ks-setup-panel-lead">平台不会在集群里安装 MySQL。请使用已有的 MySQL 8.0+，并提前创建数据库和账号；安装时会自动建表并创建管理员。</p>
              <div className="ks-setup-form-grid">
                <FormGroup label="MySQL 地址" isRequired fieldId="setup-db-host">
                  <TextInput id="setup-db-host" value={database.host} onChange={(_, value) => setDatabase(current => ({ ...current, host: value }))} placeholder="mysql.internal 或 10.20.30.20" isDisabled={installing} />
                </FormGroup>
                <FormGroup label="端口" isRequired fieldId="setup-db-port">
                  <TextInput id="setup-db-port" value={database.port} onChange={(_, value) => setDatabase(current => ({ ...current, port: value }))} placeholder="3306" isDisabled={installing} />
                </FormGroup>
                <FormGroup label="数据库名" isRequired fieldId="setup-db-name">
                  <TextInput id="setup-db-name" value={database.database} onChange={(_, value) => setDatabase(current => ({ ...current, database: value }))} placeholder="ftc_platform" isDisabled={installing} />
                </FormGroup>
                <FormGroup label="用户名" isRequired fieldId="setup-db-user">
                  <TextInput id="setup-db-user" value={database.username} onChange={(_, value) => setDatabase(current => ({ ...current, username: value }))} placeholder="ftc" isDisabled={installing} />
                </FormGroup>
                <FormGroup label="密码" isRequired fieldId="setup-db-password">
                  <TextInput id="setup-db-password" type="password" value={database.password} onChange={(_, value) => setDatabase(current => ({ ...current, password: value }))} isDisabled={installing} />
                </FormGroup>
                <FormGroup label="TLS 模式" fieldId="setup-db-tls">
                  <FormSelect id="setup-db-tls" value={database.tls} onChange={(_, value) => setDatabase(current => ({ ...current, tls: value }))} isDisabled={installing}>
                    <FormSelectOption value="disabled" label="关闭（内网明文）" />
                    <FormSelectOption value="preferred" label="优先 TLS" />
                    <FormSelectOption value="required" label="必须 TLS（校验证书）" />
                  </FormSelect>
                </FormGroup>
              </div>
              {databaseError && <Alert isInline title={databaseError} variant="danger" />}
              <div className="ks-setup-inline-actions">
                <Button variant="secondary" isDisabled={testingDatabase || installing} isLoading={testingDatabase} onClick={() => void testDatabase()}>测试数据库连接</Button>
                {databaseTestResult && <Alert isInline title={databaseTestResult} variant="success" />}
              </div>
              <div className="ks-setup-sql">
                <p><b>如果数据库和账号还未创建，请让 MySQL 管理员执行：</b></p>
                <pre>{`CREATE DATABASE ftc_platform CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE USER 'ftc'@'%' IDENTIFIED BY '<强密码>';
GRANT ALL PRIVILEGES ON ftc_platform.* TO 'ftc'@'%';
FLUSH PRIVILEGES;`}</pre>
                <p className="ks-setup-hint">账号需要对 ftc_platform 库有建表/改表/索引权限；平台不会创建数据库或账号。</p>
              </div>
            </section>
          </>}

          {step === 3 && <>
            <section className="ks-setup-panel">
              <h2>配置访问方式</h2>
              <p className="ks-setup-panel-lead">使用集群已有的 IngressClass 暴露平台。平台会生成私有 CA 和服务证书，初始化完成后下载 CA 并配置 hosts。</p>
              <div className="ks-setup-form-grid">
                <FormGroup label="内部域名" isRequired fieldId="setup-domain">
                  <TextInput id="setup-domain" value={domain} onChange={(_, value) => setDomain(value)} placeholder="ftc-platform.cmb.local" />
                  <small className="ks-setup-hint">hosts 指向任意一个节点内网 IP。</small>
                </FormGroup>
                <FormGroup label="IngressClass" isRequired fieldId="setup-ingress-class">
                  <FormSelect id="setup-ingress-class" value={ingressClass} onChange={(_, value) => setIngressClass(value)}>
                    <FormSelectOption value="" label="选择 IngressClass" />
                    {usableIngressClasses.map(item => <FormSelectOption key={item.name} value={item.name} label={`${item.name}${item.default ? '（默认）' : ''}`} />)}
                  </FormSelect>
                </FormGroup>
                <FormGroup label="平台运行镜像" fieldId="setup-platform-image">
                  <TextInput id="setup-platform-image" value={platformImage} onChange={(_, value) => setPlatformImage(value)} placeholder="留空使用安装 ConfigMap" />
                </FormGroup>
                <FormGroup label="平台镜像拉取 Secret" fieldId="setup-platform-pull">
                  <TextInput id="setup-platform-pull" value={platformPullSecret} onChange={(_, value) => setPlatformPullSecret(value)} placeholder="可选" />
                </FormGroup>
              </div>
              {accessError && <Alert isInline title={accessError} variant="danger" />}
              <Title headingLevel="h2" size="lg">第一个管理员</Title>
              <div className="ks-setup-form-grid">
                <FormGroup label="管理员用户名" isRequired fieldId="setup-admin-user">
                  <TextInput id="setup-admin-user" value={adminUsername} onChange={(_, value) => setAdminUsername(value)} />
                </FormGroup>
                <FormGroup label="管理员密码（12–72 字节）" isRequired fieldId="setup-admin-password">
                  <TextInput id="setup-admin-password" type="password" value={adminPassword} onChange={(_, value) => setAdminPassword(value)} />
                </FormGroup>
                <FormGroup label="确认管理员密码" isRequired fieldId="setup-admin-confirm">
                  <TextInput id="setup-admin-confirm" type="password" value={adminConfirm} onChange={(_, value) => setAdminConfirm(value)} />
                </FormGroup>
              </div>
            </section>
          </>}

          {step === 4 && <>
            <section className="ks-setup-panel">
              <h2>确认并安装</h2>
              <p className="ks-setup-panel-lead">安装开始后会连接外部 MySQL、建表、创建 Ingress 和平台实例，过程约 2–5 分钟。</p>
              <div className="ks-setup-review">
                <div><h3>外部 MySQL</h3><p>{database.username}@{database.host}:{database.port}/{database.database}</p><p>TLS：{database.tls === 'disabled' ? '关闭' : database.tls === 'preferred' ? '优先' : '必须'}</p><p>平台只建表，不创建数据库/账号</p></div>
                <div><h3>访问方式</h3><p>https://{domain.trim().toLowerCase()}</p><p>IngressClass {ingressClass} · 自签 CA</p><p>管理员 {adminUsername}</p></div>
              </div>
              {preflight?.nodes.length ? <div className="ks-setup-hosts"><b>安装完成后的 hosts 目标（任一节点）：</b><pre>{preflight.nodes.flatMap(node => node.internal_ips).map(ip => `${ip}  ${domain.trim().toLowerCase()}`).join('\n')}</pre></div> : null}
              {failed && <Alert isInline title="安装失败" variant="danger"><p>{installStatus?.error}</p><p>已创建的资源会保留；请修复问题后重新执行，或联系平台维护人员。</p></Alert>}
              {completed && <div className="ks-setup-result">
                <Alert isInline title="初始化完成" variant="success" />
                <p>访问地址：<a href={installStatus?.url} rel="noreferrer" target="_blank">{installStatus?.url}</a></p>
                <p>管理员：{adminUsername}</p>
                <div className="ks-setup-inline-actions">
                  <Button variant="secondary" onClick={downloadCA} isDisabled={!installStatus?.ca_cert}>下载平台 CA 证书</Button>
                </div>
                <p className="ks-setup-hint">{installStatus?.permissions_revoked ? '初始化权限已回收，setup 身份不能再修改集群。' : '安装完成后约 30 秒会自动回收 setup 的 RoleBinding/ClusterRoleBinding。'}</p>
                <div className="ks-setup-hosts"><b>hosts 配置：</b><pre>{(installStatus?.hosts ?? []).map(ip => `${ip}  ${domain.trim().toLowerCase()}`).join('\n')}</pre></div>
              </div>}
              {installStatus && <ol className="ks-setup-progress">
                {installStatus.steps.map(item => <li className={`is-${item.status}`} key={item.key}>
                  <span>{item.status === 'done' ? '✓' : item.status === 'failed' ? '!' : item.status === 'running' ? '…' : '·'}</span>
                  <div><b>{item.label}</b>{item.message && <small>{item.message}</small>}</div>
                </li>)}
              </ol>}
              {installing && !installStatus && <div className="ks-setup-loading"><Spinner aria-label="正在启动安装" /><span>正在启动安装…</span></div>}
            </section>
          </>}

          </div>
          <footer className="ks-setup-footer">
            <span>{step > 1 ? <Button variant="link" isDisabled={installing} onClick={back}>上一步</Button> : null}</span>
            <div className="ks-setup-inline-actions">
              {step < 4 && <Button variant="primary" isDisabled={!canContinue || installing || checking} onClick={next}>下一步</Button>}
              {step === 4 && !installStatus && <Button variant="primary" isDisabled={!canContinue || installing} isLoading={installing} onClick={() => void startInstall()}>开始安装</Button>}
              {step === 4 && failed && <Button variant="secondary" isDisabled={installing} onClick={() => void startInstall()}>重新尝试</Button>}
              {step === 4 && completed && <Button variant="secondary" onClick={() => window.location.assign(installStatus?.url ?? '/')}>打开平台</Button>}
            </div>
          </footer>
        </CardBody>
      </Card>
    </PageSection>
  </Page>
}
