import { Alert, Button, Card, CardBody, Label, Modal, ModalBody, ModalFooter, ModalHeader, ModalVariant, SearchInput, Spinner, Tab, Tabs, TabTitleText, Title } from '@patternfly/react-core'
import { PlusIcon, SyncAltIcon } from '@patternfly/react-icons'
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table'
import { useContext, useEffect, useState } from 'react'
import { AuthContext, type User } from '../auth/context'
import { request, roleNames } from '../auth/api'
import PlatformSettingsPage from './PlatformSettingsPage'
import './PlatformAccessPage.css'
const roleDetails = { admin: '管理平台配置、账号和角色，并使用全部排查功能。', operator: '查看集群、日志与事件，进入容器终端排查。', viewer: '查看集群、资源、日志与事件，适合观察与协作。' }
const managementTabFromLocation = () => {
 const requested = new URLSearchParams(window.location.search).get('tab')
 if (requested === 'roles') return 'roles'
 if (requested === 'parameters' || /^\/settings\/(runtime|collection|events|security|java|database|reports)/.test(window.location.pathname)) return 'parameters'
 return 'accounts'
}
export default function PlatformAccessPage() {
 const auth = useContext(AuthContext)
 const [users, setUsers] = useState<User[]>([])
 const [loading, setLoading] = useState(true)
 const [error, setError] = useState('')
 const [notice, setNotice] = useState('')
 const [tab, setTab] = useState(managementTabFromLocation)
 const [search, setSearch] = useState('')
 const [role, setRole] = useState('')
 const [status, setStatus] = useState('')
 const [page, setPage] = useState(1)
 const [refresh, setRefresh] = useState(0)
 const [editor, setEditor] = useState<User | 'new' | null>(null)
 const allowed = auth?.user.role === 'admin'
 useEffect(() => {
  const onPopState = () => setTab(managementTabFromLocation())
  window.addEventListener('popstate', onPopState)
  return () => window.removeEventListener('popstate', onPopState)
 }, [])
 useEffect(() => {
  if (!allowed) return
  let active = true
  void request('users').then(data => { if (active) { setUsers(data); setError('') } }).catch(cause => { if (active) setError(cause.message) }).finally(() => { if (active) setLoading(false) })
  return () => { active = false }
 }, [allowed, refresh])
 if (!allowed) return <Alert isInline title="当前账号无法管理平台权限" variant="info">请联系平台管理员管理账号与角色。</Alert>
 const filtered = users.filter(user => user.username.toLowerCase().includes(search.toLowerCase()) && (!role || user.role === role) && (!status || String(user.disabled) === status))
 const pages = Math.max(1, Math.ceil(filtered.length / 20))
 const current = Math.min(page, pages)
 const changeFilter = (fn: () => void) => { fn(); setPage(1) }
 const reload = () => { setLoading(true); setRefresh(value => value + 1) }
 const selectTab = (next: string) => {
  const path = next === 'accounts' ? '/settings/access' : `/settings/access?tab=${next}`
  window.history.pushState({ managementTab: next }, '', path)
  setTab(next)
 }
 return <div className={`ks-access-page${tab === 'parameters' ? ' is-parameters' : ''}`}>
  
  <Tabs activeKey={tab} onSelect={(_, key) => selectTab(String(key))} aria-label="平台管理视图"><Tab eventKey="accounts" title={<TabTitleText>账号管理</TabTitleText>} /><Tab eventKey="roles" title={<TabTitleText>角色与权限</TabTitleText>} /><Tab eventKey="parameters" title={<TabTitleText>参数配置</TabTitleText>} /></Tabs>
  {notice && <Alert isInline variant="success" title={notice} />}
  {tab === 'accounts' ? <>
   <div className="ks-access-summary" aria-label="账号概况"><span>全部账号 <strong>{users.length}</strong></span><span>启用 <strong>{users.filter(user => !user.disabled).length}</strong></span><span>已禁用 <strong>{users.filter(user => user.disabled).length}</strong></span><span>管理员 <strong>{users.filter(user => user.role === 'admin' && !user.disabled).length}</strong></span></div>
   <Card className="ks-access-accounts"><CardBody>
    <div className="ks-access-heading"><div><Title headingLevel="h2" size="lg">平台账号</Title></div><Button icon={<PlusIcon />} onClick={() => setEditor('new')}>创建账号</Button></div>
    <div className="ks-access-toolbar"><SearchInput aria-label="搜索平台账号" placeholder="搜索用户名" value={search} onChange={(_, value) => changeFilter(() => setSearch(value))} onClear={() => changeFilter(() => setSearch(''))} /><label>角色<select aria-label="筛选账号角色" value={role} onChange={e => changeFilter(() => setRole(e.target.value))}><option value="">全部角色</option>{Object.entries(roleNames).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><label>状态<select aria-label="筛选账号状态" value={status} onChange={e => changeFilter(() => setStatus(e.target.value))}><option value="">全部状态</option><option value="false">启用</option><option value="true">已禁用</option></select></label><Button icon={<SyncAltIcon />} variant="plain" aria-label="刷新账号列表" isLoading={loading} onClick={reload} /><small>{filtered.length} 个账号</small></div>
    {error && <Alert isInline title="账号列表读取失败" variant="danger">{error} 已显示的账号可能不是最新状态。<Button variant="link" onClick={reload}>重新读取</Button></Alert>}
    {loading && !users.length ? <div className="ks-access-empty"><Spinner aria-label="正在读取账号" /></div> : <><div className="ks-access-table"><Table aria-label="平台账号列表" variant="compact"><Thead><Tr><Th>账号</Th><Th>角色</Th><Th>状态</Th><Th>访问范围</Th><Th screenReaderText="账号操作" /></Tr></Thead><Tbody>{filtered.slice((current - 1) * 20, current * 20).map(user => <Tr key={user.id}><Td><strong>{user.username}</strong>{user.id === auth.user.id && <Label isCompact>当前账号</Label>}</Td><Td><Label color={user.role === 'admin' ? 'blue' : 'grey'}>{roleNames[user.role]}</Label></Td><Td><span className={`ks-access-status ${user.disabled ? 'is-disabled' : ''}`}>{user.disabled ? '已禁用' : '启用'}</span></Td><Td>全部已纳管集群</Td><Td><Button variant="link" aria-label={`编辑账号 ${user.username}`} onClick={() => setEditor(user)}>编辑</Button></Td></Tr>)}</Tbody></Table></div>{!filtered.length && !error && <div className="ks-access-empty"><strong>没有匹配的账号</strong><p>调整搜索或筛选条件后再试。</p><Button variant="link" onClick={() => { setSearch(''); setRole(''); setStatus('') }}>清除筛选</Button></div>}<div className="ks-access-pagination"><span>共 {filtered.length} 个账号 · 第 {current} / {pages} 页</span><Button variant="secondary" isDisabled={current <= 1 || loading} onClick={() => setPage(current - 1)}>上一页</Button><Button variant="secondary" isDisabled={current >= pages || loading} onClick={() => setPage(current + 1)}>下一页</Button></div></>}
   </CardBody></Card>
   
  </> : tab === 'roles' ? <Card><CardBody><div className="ks-access-heading"><div><Title headingLevel="h2" size="lg">角色权限一览</Title></div></div><div className="ks-access-table"><Table aria-label="角色权限对照" variant="compact"><Thead><Tr><Th>操作</Th><Th>管理员</Th><Th>运维员</Th><Th>只读用户</Th></Tr></Thead><Tbody>{[['查看集群、资源与容量',true,true,true],['查看日志和事件',true,true,true],['使用容器终端',true,true,false],['纳管与移除集群',true,false,false],['修改业务命名空间与交付基线',true,false,false],['管理账号、角色与密码重置',true,false,false],['修改平台参数与报告模型',true,false,false]].map(([label,...values]) => <Tr key={String(label)}><Td>{label}</Td>{values.map((value,index) => <Td key={index}>{value ? '允许' : '—'}</Td>)}</Tr>)}</Tbody></Table></div></CardBody></Card> : <PlatformSettingsPage embedded />}
  {editor && <AccountEditor key={editor === 'new' ? 'new' : editor.id} actor={auth.user} account={editor} onClose={() => setEditor(null)} onSaved={async () => { const currentAccount = editor !== 'new' && editor.id === auth.user.id; setEditor(null); setNotice(currentAccount ? '账号已更新，请重新登录。' : '账号已保存。受影响账号的旧会话已失效。'); reload() }} />}
 </div>
}
function AccountEditor({ actor, account, onClose, onSaved }: { actor: User; account: User | 'new'; onClose: () => void; onSaved: () => void }) {
 const creating = account === 'new'
 const self = !creating && account.id === actor.id
 const [username, setUsername] = useState(creating ? '' : account.username)
 const [role, setRole] = useState<User['role']>(creating ? 'viewer' : account.role)
 const [disabled, setDisabled] = useState(creating ? false : account.disabled)
 const [password, setPassword] = useState('')
 const [confirm, setConfirm] = useState('')
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState('')
 const submit = async (e: React.FormEvent) => { e.preventDefault(); setError(''); if ((creating || password) && password !== confirm) { setError('两次密码不一致。'); return }; setBusy(true); try { await request('users', creating ? 'POST' : 'PATCH', { ...(creating ? { username } : { id: account.id }), role, disabled, ...(password ? { password } : {}) }); onSaved() } catch (cause) { setError((cause as Error).message) } finally { setBusy(false) } }
 return <Modal isOpen variant={ModalVariant.small} onClose={busy ? undefined : onClose} aria-labelledby="ks-account-editor-title"><ModalHeader title={creating ? '创建平台账号' : `编辑账号 · ${account.username}`} labelId="ks-account-editor-title" /><ModalBody>{error && <Alert isInline variant="danger" title={error} />}<form id="ks-account-editor" className="ks-access-form" onSubmit={e => void submit(e)}><label>用户名<input value={username} disabled={!creating || busy} required minLength={3} maxLength={64} pattern="[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}" autoComplete="off" onChange={e => setUsername(e.target.value)} /><small>3–64 位字母、数字、点、横线或下划线；创建后不可修改。</small></label><label>角色<select aria-label="角色" value={role} disabled={self || busy} onChange={e => setRole(e.target.value as User['role'])}>{Object.entries(roleNames).map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select><small>{roleDetails[role]}</small></label>{!creating && <label>账号状态<select aria-label="账号状态" value={String(disabled)} disabled={self || busy} onChange={e => setDisabled(e.target.value === 'true')}><option value="false">启用</option><option value="true">禁用</option></select></label>}{!self && <><label>{creating ? '初始密码' : '重置密码（可选）'}<input required={creating} disabled={busy} type="password" minLength={12} autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} /><small>{creating ? '密码为 12–72 字节。' : '留空保留原密码；填写新密码会使已有登录会话失效。'}</small></label>{(creating || password) && <label>确认密码<input required disabled={busy} type="password" autoComplete="new-password" value={confirm} onChange={e => setConfirm(e.target.value)} /></label>}</>}{self && <p>当前账号的角色和状态受保护。修改自己的密码请使用右上角账号菜单。</p>}</form></ModalBody><ModalFooter><Button type="submit" form="ks-account-editor" isLoading={busy} isDisabled={busy || self} >{creating ? '创建账号' : '保存更改'}</Button><Button variant="link" isDisabled={busy} onClick={onClose}>取消</Button></ModalFooter></Modal>
}
