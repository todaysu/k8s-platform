import { createContext, useContext } from 'react'
export type User = { id: number; username: string; role: 'admin' | 'operator' | 'viewer'; disabled: boolean }
export type AuthState = { user: User; logout: () => void; manage: () => void; password: () => void }
export const AuthContext = createContext<AuthState | null>(null)
export function usePermission(permission: 'configure' | 'exec') { const auth = useContext(AuthContext); return !auth || auth.user.role === 'admin' || (permission === 'exec' && auth.user.role === 'operator') }
