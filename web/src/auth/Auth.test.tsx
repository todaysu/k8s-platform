import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { AccountMenu, AuthGate } from './Auth'
import { usePermission } from './context'
function Child() { const configure = usePermission('configure'); const exec = usePermission('exec'); return <><AccountMenu /><p>集群数据</p><button disabled={!configure}>配置</button><button disabled={!exec}>终端</button></> }
const user = { id: 1, username: 'reader', role: 'viewer', disabled: false }
function mock(session: unknown, setup = false) { const fetch = vi.fn(async (input: RequestInfo | URL) => { const path = String(input); if (path.endsWith('/session')) return { ok: !!session, status: session ? 200 : 401, json: async () => ({ data: session || { bootstrap_required: setup } }) }; if (path.endsWith('/login')) return { ok: true, status: 200, json: async () => ({ data: user }) }; if (path.endsWith('/logout')) return { ok: true, status: 200, json: async () => ({ data: true }) }; return { ok: false, status: 401, json: async () => ({ error: { code: 'UNAUTHENTICATED' } }) } }); vi.stubGlobal('fetch', fetch); return fetch }
afterEach(() => { cleanup(); vi.unstubAllGlobals() })
it('gates cluster data until login and applies viewer permissions', async () => { const fetch = mock(null); const events = userEvent.setup(); render(<AuthGate><Child /></AuthGate>); expect(screen.queryByText('集群数据')).not.toBeInTheDocument(); await screen.findByRole('heading', { name: '登录运维平台' }); expect(screen.getByText('FTC')).toBeInTheDocument(); expect(screen.getByText('集群运维平台')).toBeInTheDocument(); await events.type(screen.getByLabelText('密码'), 'test-password-123'); await events.click(screen.getByRole('button', { name: '登录' })); expect(await screen.findByText('集群数据')).toBeInTheDocument(); expect(screen.getByRole('button', { name: '配置' })).toBeDisabled(); expect(screen.getByRole('button', { name: '终端' })).toBeDisabled(); expect(fetch.mock.calls.some(([path]) => String(path).endsWith('/login'))).toBe(true) })
it('displays bootstrap setup without exposing a token', async () => { mock(null, true); render(<AuthGate><Child /></AuthGate>); expect(await screen.findByRole('heading', { name: '初始化管理员' })).toBeInTheDocument(); expect(screen.getByLabelText('一次性初始化凭证')).toHaveValue(''); expect(screen.getByLabelText('确认密码')).toBeInTheDocument() })
it('operator can use terminal but cannot configure or manage accounts', async () => { mock({ ...user, role: 'operator' }); render(<AuthGate><Child /></AuthGate>); await screen.findByText('集群数据'); expect(screen.getByRole('button', { name: '终端' })).toBeEnabled(); expect(screen.getByRole('button', { name: '配置' })).toBeDisabled(); expect(screen.queryByRole('button', { name: '账号与权限' })).not.toBeInTheDocument() })
it('removes protected data on expired API session', async () => { mock(user); render(<AuthGate><Child /></AuthGate>); await screen.findByText('集群数据'); await window.fetch('/api/v1/clusters'); await waitFor(() => expect(screen.queryByText('集群数据')).not.toBeInTheDocument()); expect(await screen.findByRole('heading', { name: '登录运维平台' })).toBeInTheDocument() })

it('dismisses the administrator menu outside, preserves inside interaction, and closes after selection', async () => {
 const userEventSession = userEvent.setup(); mock({ ...user, role: 'admin' }); render(<AuthGate><Child /></AuthGate>)
 const menu = await screen.findByText('管理员', { selector: '.ks-account-identity small' })
 await userEventSession.click(menu)
 const details = menu.closest('details')!
 expect(details).toHaveAttribute('open')
 expect(screen.getByRole('button', { name: '平台角色权限' })).toBeVisible()
 await userEventSession.click(screen.getByText('集群数据'))
 expect(details).not.toHaveAttribute('open')
 await userEventSession.click(menu)
 await userEventSession.keyboard('{Escape}')
 expect(details).not.toHaveAttribute('open')
 await userEventSession.click(menu)
 await userEventSession.click(screen.getByRole('button', { name: '平台角色权限' }))
 expect(details).not.toHaveAttribute('open')
 expect(window.location.pathname).toBe('/settings/access')
})
