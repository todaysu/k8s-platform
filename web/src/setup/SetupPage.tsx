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

type PreflightStorageClass = {
  name: string
  default: boolean
  provisioner: string
  allow_volume_expansion: boolean
  volume_binding_mode: string
}

type PreflightIngressClass = {
  name: string
  default: boolean
  controller: string
}

type Preflight = {
  namespace: string
  nodes: PreflightNode[]
  storage_classes: PreflightStorageClass[]
  ingress_classes: PreflightIngressClass[]
  pxc_available: boolean
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

type Volume = { node: string; path: string }

const setupSteps = ['环境检查', '数据库与存储', '访问配置', '确认安装']
const defaultMySQLImages = {
  operator: 'percona/percona-xtradb-cluster-operator:1.18.0',
  pxc: 'percona/percona-xtradb-cluster:8.0.42-33.1',
  haproxy: 'percona/haproxy:2.8.15',
  backup: 'percona/percona-xtrabackup:8.0.42-33.1',
}

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
const absolutePathPattern = /^\/(?!\/)[^\s]*$/

export default function SetupPage() {
  const [step, setStep] = useState(1)
  const [token, setToken] = useState('')
  const [preflight, setPreflight] = useState<Preflight | null>(null)
  const [checking, setChecking] = useState(false)
  const [volumes, setVolumes] = useState<Volume[]>([0, 1, 2].map(() => ({ node: '', path: '/data/ftc-platform/mysql' })))
  const [storageSize, setStorageSize] = useState('200')
  const [mysqlImages, setMySQLImages] = useState(defaultMySQLImages)
  const [mysqlPullSecret, setMySQLPullSecret] = useState('')
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

  const schedulableNodes = preflight?.nodes.filter(node => node.schedulable) ?? []
  const usableIngressClasses = preflight?.ingress_classes ?? []
  const preflightErrors = preflight?.errors ?? []
  const preflightWarnings = preflight?.warnings ?? []

  const runPreflight = async () => {
    if (!token.trim()) { setError('请先填写初始化凭证。'); return }
    setChecking(true); setError('')
    try {
      const result = await post<Preflight>('/api/v1/setup/preflight', { token })
      setPreflight(result)
      setVolumes(current => current.map((volume, index) => ({
        ...volume,
        node: volume.node || result.nodes.filter(node => node.schedulable)[index]?.name || '',
      })))
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

  const volumeError = useMemo(() => {
    const nodes = new Set<string>()
    for (const volume of volumes) {
      if (!volume.node) return '请为三个副本分别选择节点。'
      if (nodes.has(volume.node)) return '三个副本必须使用不同节点。'
      nodes.add(volume.node)
      if (!absolutePathPattern.test(volume.path.trim()) || volume.path.trim() === '/') return '本地盘路径必须是 / 开头的绝对路径，且不能是根目录。'
    }
    const size = Number(storageSize)
    if (!Number.isInteger(size) || size < 10 || size > 8192) return '单卷容量必须是 10–8192 之间的整数 GiB。'
    return ''
  }, [storageSize, volumes])

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
      ? Boolean(preflight) && !volumeError
      : step === 3
        ? !accessError
        : true

  const next = () => {
    setError('')
    if (step === 1 && preflightErrors.length > 0) { setError('请先处理环境检查中的错误。'); return }
    if (step === 1 && preflight?.runtime_secret_exists) { setError('平台已经初始化，不能重复安装。'); return }
    if (step === 2 && volumeError) { setError(volumeError); return }
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

  const startInstall = async () => {
    setError('')
    if (volumeError) { setError(volumeError); return }
    if (accessError) { setError(accessError); return }
    setInstalling(true)
    setInstallStatus(null)
    try {
      const result = await post<InstallStatus>('/api/v1/setup/install', {
        token,
        domain: domain.trim().toLowerCase(),
        ingress_class: ingressClass,
        volumes: volumes.map(volume => ({ node: volume.node, path: volume.path.trim(), size_gib: Number(storageSize) })),
        storage_size_gib: Number(storageSize),
        mysql: {
          namespace: preflight?.namespace ?? 'ftc-platform',
          name: 'platform-mysql',
          secret_name: 'platform-mysql-secrets',
          replicas: 3,
          storage_class: 'ftc-platform-local',
          storage_size: `${Number(storageSize)}Gi`,
          pxc_image: mysqlImages.pxc.trim(),
          haproxy_image: mysqlImages.haproxy.trim(),
          backup_image: mysqlImages.backup.trim(),
          operator_image: mysqlImages.operator.trim(),
          image_pull_secret: mysqlPullSecret.trim(),
        },
        admin_username: adminUsername,
        admin_password: adminPassword,
        platform_image: platformImage.trim(),
        platform_image_pull_secret: platformPullSecret.trim(),
        api_replicas: 2,
        collector_replicas: 2,
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
              <p className="ks-setup-lead">完成环境检查、MySQL 高可用、内部域名和管理员配置。初始化结束后即可通过 HTTPS 登录。</p>
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
              <p className="ks-setup-panel-lead">初始化令牌由引导 Job 自动生成。平台会检查节点、存储、Ingress 和 PXC Operator。</p>
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
                  <h4>存储与 Ingress</h4>
                  <p><b>StorageClass：</b>{preflight.storage_classes.length ? preflight.storage_classes.map(item => `${item.name}${item.default ? '（默认）' : ''}`).join('、') : '将创建本地 SC'}</p>
                  <p><b>IngressClass：</b>{preflight.ingress_classes.length ? preflight.ingress_classes.map(item => `${item.name}${item.default ? '（默认）' : ''}`).join('、') : '未发现'}</p>
                  <p><b>PXC Operator：</b>{preflight.pxc_available ? '已安装' : '未检测到'}</p>
                  <p><b>已有运行 Secret：</b>{preflight.runtime_secret_exists ? '是' : '否'}</p>
                </div>
              </div>
            </section>}
          </>}

          {step === 2 && <>
            <section className="ks-setup-panel">
              <h2>配置 MySQL 高可用</h2>
              <p className="ks-setup-panel-lead">PXC 使用 3 个副本，分别落到 3 个节点的本地盘。节点故障时依靠剩余副本维持仲裁；本地盘不会漂移。</p>
              <div className="ks-setup-volume-list">
                {volumes.map((volume, index) => <div className="ks-setup-volume" key={index}>
                  <span className="ks-setup-volume-index">{index + 1}</span>
                  <FormGroup label={`副本 ${index + 1} 节点`} isRequired fieldId={`setup-node-${index}`}>
                    <FormSelect id={`setup-node-${index}`} value={volume.node} onChange={(_, value) => setVolumes(current => current.map((item, itemIndex) => itemIndex === index ? { ...item, node: value } : item))}>
                      <FormSelectOption value="" label="选择节点" />
                      {schedulableNodes.map(node => <FormSelectOption key={node.name} value={node.name} label={`${node.name} · ${node.arch}`} />)}
                    </FormSelect>
                  </FormGroup>
                  <FormGroup label="本地盘路径" isRequired fieldId={`setup-path-${index}`}>
                    <TextInput id={`setup-path-${index}`} value={volume.path} onChange={(_, value) => setVolumes(current => current.map((item, itemIndex) => itemIndex === index ? { ...item, path: value } : item))} placeholder="/data/ftc-platform/mysql" />
                  </FormGroup>
                </div>)}
              </div>
              {volumeError && <Alert isInline title={volumeError} variant="danger" />}
              <div className="ks-setup-form-grid">
                <FormGroup label="单卷容量（GiB）" isRequired fieldId="setup-size">
                  <TextInput id="setup-size" type="number" value={storageSize} onChange={(_, value) => setStorageSize(value)} />
                  <small className="ks-setup-hint">建议 200 GiB 起；本地 PV 不支持在线扩容。</small>
                </FormGroup>
                <FormGroup label="MySQL 镜像拉取 Secret" fieldId="setup-mysql-pull">
                  <TextInput id="setup-mysql-pull" value={mysqlPullSecret} onChange={(_, value) => setMySQLPullSecret(value)} placeholder="可选，命名空间内已有的 Secret" />
                </FormGroup>
              </div>
              <div className="ks-setup-form-grid">
                <FormGroup label="PXC Operator 镜像" fieldId="setup-operator-image">
                  <TextInput id="setup-operator-image" value={mysqlImages.operator} onChange={(_, value) => setMySQLImages(current => ({ ...current, operator: value }))} />
                </FormGroup>
                <FormGroup label="PXC 镜像" fieldId="setup-pxc-image">
                  <TextInput id="setup-pxc-image" value={mysqlImages.pxc} onChange={(_, value) => setMySQLImages(current => ({ ...current, pxc: value }))} />
                </FormGroup>
                <FormGroup label="HAProxy 镜像" fieldId="setup-haproxy-image">
                  <TextInput id="setup-haproxy-image" value={mysqlImages.haproxy} onChange={(_, value) => setMySQLImages(current => ({ ...current, haproxy: value }))} />
                </FormGroup>
                <FormGroup label="备份镜像" fieldId="setup-backup-image">
                  <TextInput id="setup-backup-image" value={mysqlImages.backup} onChange={(_, value) => setMySQLImages(current => ({ ...current, backup: value }))} />
                </FormGroup>
              </div>
            </section>
          </>}

          {step === 3 && <>
            <section className="ks-setup-panel">
              <h2>配置访问方式</h2>
              <p className="ks-setup-panel-lead">Ingress 使用 hostNetwork 暴露节点 443。平台会生成私有 CA 和服务证书，初始化完成后下载 CA 并配置 hosts。</p>
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
              <p className="ks-setup-panel-lead">安装开始后会依次创建本地存储、PXC、Ingress 和平台实例，过程约 5–15 分钟。</p>
              <div className="ks-setup-review">
                <div><h3>MySQL 与存储</h3><p>PXC 3 副本 / HAProxy 3 副本</p><p>{volumes.map(volume => `${volume.node}:${volume.path}`).join('、')}</p><p>单卷 {storageSize} GiB，本地 PV（Retain，不支持扩容）</p></div>
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
