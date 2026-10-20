import { useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import './Auth.css'
import { AuthContext, type User } from './context'
import { request, roleNames } from './api'
import { PlatformBrand } from '../components/PlatformBrand'
export function AccountMenu() {
 const auth = useContext(AuthContext)
 const menu = useRef<HTMLDetailsElement>(null)
 useEffect(() => {
  const dismiss = (event: PointerEvent) => { if (menu.current && !menu.current.contains(event.target as Node)) menu.current.open = false }
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape' && menu.current?.open) { menu.current.open = false; menu.current.querySelector('summary')?.focus() } }
  document.addEventListener('pointerdown', dismiss); document.addEventListener('keydown', escape)
  return () => { document.removeEventListener('pointerdown', dismiss); document.removeEventListener('keydown', escape) }
 }, [])
 if (!auth) return <span>平台管理员</span>
 const select = (action: () => void) => { if (menu.current) menu.current.open = false; action() }
 return <details ref={menu} className="ks-account-menu"><summary aria-label={`${auth.user.username} · ${roleNames[auth.user.role]}`}><span className="ks-account-avatar">{auth.user.username.slice(0,1).toUpperCase()}</span><span className="ks-account-identity"><strong>{auth.user.username}</strong><small>{roleNames[auth.user.role]}</small></span></summary><div>{auth.user.role === 'admin' && <button onClick={() => select(auth.manage)}>平台角色权限</button>}<button onClick={() => select(auth.password)}>修改密码</button><button onClick={() => select(auth.logout)}>退出登录</button></div></details>
}

export function AuthGate({ children }: { children: ReactNode }) {
 const [user, setUser] = useState<User | null>(null)
 const [loading, setLoading] = useState(true)
 const [setup, setSetup] = useState(false)
 const [connectionError, setConnectionError] = useState('')
 const [panel, setPanel] = useState<'password' | null>(null)
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {
  let active = true
  void fetch('/api/v1/auth/session').then(async res => { const body = await res.json(); if (!active) return; if (res.ok) setUser(body.data); else if (res.status === 401) { setUser(null); setSetup(!!body.data?.bootstrap_required) } else throw new Error('服务暂时不可用') }).catch(() => { if (active) setConnectionError('无法连接登录服务，请重试。') }).finally(() => { if (active) setLoading(false) })
  return () => { active = false }
 }, [refresh])
 useEffect(() => {
  const original = window.fetch
  window.fetch = async (...args) => { const response = await original(...args); const path = String(args[0]); if (response.status === 401 && !path.includes('/auth/')) { setUser(null); setPanel(null) }; return response }
  return () => { window.fetch = original }
 }, [])
 const logout = async () => { try { await request('logout', 'POST'); setUser(null); setPanel(null) } catch (cause) { window.alert(cause instanceof Error ? cause.message : '退出失败') } }
 if (loading) return <main className="ks-login"><p>正在检查登录状态…</p></main>
 if (connectionError) return <main className="ks-login"><p role="alert">{connectionError}</p><button onClick={() => { setConnectionError(''); setLoading(true); setRefresh(value => value + 1) }}>重试连接</button></main>
 if (!user) return <Login setup={setup} onLogin={setUser} onSetup={() => setSetup(false)} />
 return <AuthContext.Provider value={{ user, logout: () => void logout(), manage: () => { window.history.pushState({}, '', '/settings/access'); window.dispatchEvent(new PopStateEvent('popstate')) }, password: () => setPanel('password') }}>{children}{panel && <div className="ks-auth-overlay"><section className="ks-auth-panel" role="dialog" aria-modal="true" aria-label={'修改密码'}><header><h2>{'修改密码'}</h2><button onClick={() => setPanel(null)}>关闭</button></header><Password onChanged={() => { setPanel(null); setUser(null) }} /></section></div>}</AuthContext.Provider>
}
function Login({ setup, onLogin, onSetup }: { setup: boolean; onLogin: (user: User) => void; onSetup: () => void }) {
 const [username, setUsername] = useState('admin'); const [password, setPassword] = useState(''); const [confirm, setConfirm] = useState(''); const [token, setToken] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const [notice, setNotice] = useState('')
 return <main className="ks-login"><section><div className="ks-login-brand"><PlatformBrand variant="login" /></div><h1>{setup ? '初始化管理员' : '登录运维平台'}</h1><p>{setup ? '首次使用需要服务器上的一次性初始化凭证，位于平台数据库同目录的 bootstrap-token 文件。' : '登录后查看集群、容量和异常事件。'}</p>{notice && <p role="status">{notice}</p>}{error && <p role="alert">{error}</p>}<form onSubmit={async e => { e.preventDefault(); setError(''); if (setup && password !== confirm) { setError('两次密码不一致。'); return }; setBusy(true); try { const data = await request(setup ? 'setup' : 'login', 'POST', { username, password, ...(setup ? { token: token.trim() } : {}) }); setPassword(''); setConfirm(''); setToken(''); if (setup) { onSetup(); setNotice('管理员已创建，请使用新密码登录。') } else onLogin(data) } catch (cause) { setError(cause instanceof Error ? cause.message : '登录失败') } finally { setBusy(false) } }}><label>用户名<input autoComplete="username" required maxLength={64} value={username} onChange={e => setUsername(e.target.value)} /></label>{setup && <label>一次性初始化凭证<input autoComplete="off" type="password" required value={token} onChange={e => setToken(e.target.value)} /></label>}<label>密码<input type="password" autoComplete={setup ? 'new-password' : 'current-password'} required minLength={setup ? 12 : undefined} value={password} onChange={e => setPassword(e.target.value)} /></label>{setup && <label>确认密码<input type="password" autoComplete="new-password" required value={confirm} onChange={e => setConfirm(e.target.value)} /></label>}<button disabled={busy} type="submit">{busy ? '正在处理…' : setup ? '创建管理员' : '登录'}</button></form></section></main>
}
function Password({ onChanged }: { onChanged: () => void }) {
 const [current, setCurrent] = useState(''); const [password, setPassword] = useState(''); const [confirm, setConfirm] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false)
 return <form className="ks-auth-form" onSubmit={async e => { e.preventDefault(); if (confirm !== password) { setError('两次密码不一致。'); return }; setBusy(true); try { await request('password', 'PUT', { current_password: current, password }); onChanged() } catch (cause) { setError((cause as Error).message) } finally { setBusy(false) } }}><p>修改成功后，所有已登录会话失效，请重新登录。</p>{error && <p role="alert">{error}</p>}<label>当前密码<input required type="password" autoComplete="current-password" value={current} onChange={e => setCurrent(e.target.value)} /></label><label>新密码（12–72 字节）<input required minLength={12} type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} /></label><label>确认新密码<input required type="password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} /></label><button disabled={busy}>修改并重新登录</button></form>
}
