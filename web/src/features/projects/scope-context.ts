import { createContext, useContext } from 'react'
import type { Scope } from '@/lib/scope'

// 项目或平台页面中的当前作用域，由 ProjectScopeRoute、PlatformScopeRoute 提供。
export const ScopeContext = createContext<Scope | null>(null)

export function useScope(): Scope {
  const scope = useContext(ScopeContext)
  if (!scope) throw new Error('useScope must be used inside a project or platform route')
  return scope
}
