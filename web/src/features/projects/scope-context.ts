import { createContext, useContext } from 'react'
import type { Scope } from '@/lib/scope'

// The current scope in project or platform pages, provided by ProjectScopeRoute and PlatformScopeRoute.
export const ScopeContext = createContext<Scope | null>(null)

export function useScope(): Scope {
  const scope = useContext(ScopeContext)
  if (!scope) throw new Error('useScope must be used inside a project or platform route')
  return scope
}
