import { createContext, useContext, useLayoutEffect } from 'react'
export const DirtyContext = createContext<((id: string, dirty: boolean | null) => void) | null>(null)

// currentProjectName comes from the project matching the currently resolved projectId; ownership is never guessed from an
// arbitrary new path.
export function sameRenamedProjectPage(current: string, next: string, currentProjectName?: string) {
  if (!currentProjectName) return false
  const from = current.split('/'), to = next.split('/')
  if (from[1] !== 'p' || to[1] !== 'p' || from.slice(3).join('/') !== to.slice(3).join('/')) return false
  try { return decodeURIComponent(from[2]) !== currentProjectName && decodeURIComponent(to[2]) === currentProjectName }
  catch { return false }
}

export function useDirtyForm(id: string, dirty: boolean) {
  const register = useContext(DirtyContext)
  if (!register) throw new Error('Dirty form requires the page navigation guard')
  useLayoutEffect(() => { register(id, dirty); return () => register(id, null) }, [register, id, dirty])
}
