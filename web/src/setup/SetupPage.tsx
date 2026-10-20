import { useEffect, useState } from 'react'
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
  TextInput,
  Title,
} from '@patternfly/react-core'
import { PlatformBrand } from '../components/PlatformBrand'
import './SetupPage.css'

type SetupStatus = {
  setup_required: boolean
  default_host: string
  default_database: string
  default_port: string
  default_user: string
}

type Connection = {
  token: string
  mode: 'in-cluster' | 'external'
  host: string
  port: string
  user: string
  password: string
  database: string
  tls: string
}

type ApiResponse<T> = { data: T | null; error: { code: string; message: string } | null }

async function request<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  let payload: ApiResponse<T>
  try {
    payload = await response.json() as ApiResponse<T>
  } catch {
    throw new Error('平台返回了无法识别的响应。')
  }
  if (!response.ok || payload.error) {
    throw new Error(payload.error?.message ?? '初始化请求失败。')
  }
  return payload.data as T
}

export default function SetupPage() {
  const [status, setStatus] = useState<SetupStatus | null>(null)
  const [mode, setMode] = useState<'in-cluster' | 'external'>('in-cluster')
  const [token, setToken] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('3306')
  const [user, setUser] = useState('ftc')
  const [password, setPassword] = useState('')
  const [database, setDatabase] = useState('')
  const [tls, setTLS] = useState('')
  const [adminUsername, setAdminUsername] = useState('admin')
  const [adminPassword, setAdminPassword] = useState('')
  const [adminConfirm, setAdminConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [initialized, setInitialized] = useState(false)

  useEffect(() => {
    let active = true
    void fetch('/api/v1/setup/status').then(async response => {
      if (!response.ok) throw new Error('无法读取初始化状态')
      const payload = await response.json() as ApiResponse<SetupStatus>
      if (!active || !payload.data) return
      setStatus(payload.data)
      setHost(payload.data.default_host)
      setPort(payload.data.default_port)
      setUser(payload.data.default_user)
      setDatabase(payload.data.default_database)
    }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : '无法读取初始化状态。') })
    return () => { active = false }
  }, [])

  const switchMode = (next: 'in-cluster' | 'external') => {
    setMode(next)
    setError('')
    setNotice('')
    if (next === 'in-cluster' && status) {
      setHost(status.default_host)
      setPort(status.default_port)
      setUser(status.default_user)
      setDatabase(status.default_database)
    }
    if (next === 'external') {
      setHost('')
      setPort('3306')
      setUser('ftc')
      setDatabase('ftc_platform')
    }
  }

  const connection = (): Connection => ({ token, mode, host, port, user, password, database, tls })

  const testConnection = async () => {
    setBusy(true); setError(''); setNotice('')
    try {
      await request<{ ok: boolean }>('/api/v1/setup/test', connection())
      setNotice('数据库连接成功，可以继续初始化。')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '数据库连接失败。')
    } finally { setBusy(false) }
  }

  const initialize = async () => {
    setError(''); setNotice('')
    if (adminPassword !== adminConfirm) { setError('两次管理员密码不一致。'); return }
    if (!token.trim()) { setError('请输入初始化凭证。'); return }
    if (!password) { setError('请输入数据库密码。'); return }
    setBusy(true)
    try {
      await request<{ restarting: boolean }>('/api/v1/setup/initialize', {
        ...connection(),
        admin_username: adminUsername,
        admin_password: adminPassword,
      })
      setInitialized(true)
      setNotice('数据库初始化完成，平台正在重启。约 10 秒后刷新页面。')
      window.setTimeout(() => window.location.reload(), 10000)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '数据库初始化失败。')
    } finally { setBusy(false) }
  }

  return (
    <Page className="ks-setup-page">
      <PageSection className="ks-setup-shell">
        <div className="ks-setup-brand"><PlatformBrand variant="login" /></div>
        <Card className="ks-setup-card">
          <CardBody>
            <Title headingLevel="h1" size="2xl">初始化平台数据库</Title>
            <p className="ks-setup-lead">
              选择数据库部署方式，填写连接信息并创建第一个管理员账号。初始化完成后平台会自动重启并进入登录页。
            </p>
            {error && <Alert isInline title={error} variant="danger" />}
            {notice && <Alert isInline title={notice} variant={initialized ? 'success' : 'info'} />}
            <Form onSubmit={event => { event.preventDefault(); void initialize() }}>
              <FormGroup label="数据库部署方式" isRequired fieldId="setup-mode">
                <FormSelect
                  id="setup-mode"
                  value={mode}
                  onChange={(_, value) => switchMode(value as 'in-cluster' | 'external')}
                  isDisabled={busy || initialized}
                >
                  <FormSelectOption value="in-cluster" label="随平台部署在集群内（使用 platform-mysql Service）" />
                  <FormSelectOption value="external" label="连接集群外 MySQL（提供地址和账号密码）" />
                </FormSelect>
              </FormGroup>

              <div className="ks-setup-grid">
                <FormGroup label="数据库地址" isRequired fieldId="setup-host">
                  <TextInput id="setup-host" value={host} onChange={(_, value) => setHost(value)} isDisabled={busy || initialized} placeholder="mysql.example.com" />
                </FormGroup>
                <FormGroup label="端口" isRequired fieldId="setup-port">
                  <TextInput id="setup-port" value={port} onChange={(_, value) => setPort(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="数据库名" isRequired fieldId="setup-database">
                  <TextInput id="setup-database" value={database} onChange={(_, value) => setDatabase(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="数据库用户名" isRequired fieldId="setup-user">
                  <TextInput id="setup-user" value={user} onChange={(_, value) => setUser(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="数据库密码" isRequired fieldId="setup-password">
                  <TextInput id="setup-password" type="password" value={password} onChange={(_, value) => setPassword(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="TLS（可选）" fieldId="setup-tls">
                  <TextInput id="setup-tls" value={tls} onChange={(_, value) => setTLS(value)} isDisabled={busy || initialized} placeholder="true / skip-verify / 留空" />
                </FormGroup>
              </div>

              <FormGroup label="初始化凭证" isRequired fieldId="setup-token">
                <TextInput id="setup-token" type="password" value={token} onChange={(_, value) => setToken(value)} isDisabled={busy || initialized} />
                <small className="ks-setup-hint">
                  从平台命名空间读取：kubectl -n ftc-platform get secret ftc-platform -o jsonpath=&#123;.data.setup-token&#125; | base64 -d
                </small>
              </FormGroup>

              <Title headingLevel="h2" size="lg">第一个管理员</Title>
              <div className="ks-setup-grid">
                <FormGroup label="管理员用户名" isRequired fieldId="setup-admin-user">
                  <TextInput id="setup-admin-user" value={adminUsername} onChange={(_, value) => setAdminUsername(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="管理员密码（12–72 字节）" isRequired fieldId="setup-admin-password">
                  <TextInput id="setup-admin-password" type="password" value={adminPassword} onChange={(_, value) => setAdminPassword(value)} isDisabled={busy || initialized} />
                </FormGroup>
                <FormGroup label="确认管理员密码" isRequired fieldId="setup-admin-confirm">
                  <TextInput id="setup-admin-confirm" type="password" value={adminConfirm} onChange={(_, value) => setAdminConfirm(value)} isDisabled={busy || initialized} />
                </FormGroup>
              </div>

              <div className="ks-setup-actions">
                <Button type="button" variant="secondary" isDisabled={busy || initialized} onClick={() => void testConnection()}>测试连接</Button>
                <Button type="submit" variant="primary" isLoading={busy} isDisabled={initialized}>初始化数据库</Button>
              </div>
            </Form>
          </CardBody>
        </Card>
      </PageSection>
    </Page>
  )
}
