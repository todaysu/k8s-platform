export const roleNames = { admin: '管理员', operator: '运维员', viewer: '只读用户' }
const messages: Record<string, string> = { INVALID_CREDENTIALS: '用户名或密码不正确。', LOGIN_RATE_LIMITED: '尝试次数过多，请 15 分钟后重试。', SETUP_REJECTED: '初始化凭证无效或初始化已完成。', INVALID_CREDENTIAL_FORMAT: '用户名须为 3–64 位字母、数字、点、横线或下划线；密码须为 12–72 字节。', PASSWORD_LENGTH: '密码须为 12–72 字节。', USERNAME_EXISTS: '用户名已存在。', CANNOT_DISABLE_SELF: '不能禁用自己或降低自己的管理员权限。', ORIGIN_REJECTED: '请求来源不匹配，请从平台页面操作。', FORBIDDEN: '当前账号没有此操作权限。', UNAUTHENTICATED: '登录已过期，请重新登录。' }
export async function request(path: string, method = 'GET', data?: unknown) {
 const res = await fetch(`/api/v1/auth/${path}`, { method, headers: data ? { 'Content-Type': 'application/json' } : undefined, body: data ? JSON.stringify(data) : undefined })
 const payload = await res.json()
 if (!res.ok) throw new Error(messages[payload.error?.code] || '操作失败，请稍后重试。')
 return payload.data
}
