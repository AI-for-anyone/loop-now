import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { nonzeroUUID } from '@/features/project-credentials/import-selection'

// Transport-independent control flow, not an API DTO. The callback adapter
// captures its formal input before begin(); a committed route precedes POST.
export function useOriginalActionLocator(routeKey: string) {
  const [params, setParams] = useSearchParams()
  const id = params.get(routeKey)
  const [request, setRequest] = useState<{ key: string; id: string; invoke: (id: string) => Promise<void> } | null>(null)
  const [result, setResult] = useState<{ key: string; id: string; error: unknown } | null>(null)
  const latestKey = useRef(routeKey), latestId = useRef(id), mounted = useRef(true), issued = useRef(new Set<string>())
  const latestNavigation = useRef({ params, setParams })
  const starting = useRef<{ key: string; id: string } | null>(null)
  useLayoutEffect(() => { latestKey.current = routeKey; latestId.current = id; latestNavigation.current = { params, setParams } }, [routeKey, id, params, setParams])
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  useEffect(() => {
    if (!request || request.key !== routeKey || id !== request.id || issued.current.has(request.id)) return
    issued.current.add(request.id)
    void (async () => {
      try {
        if (mounted.current && latestKey.current === request.key && latestId.current === request.id) await request.invoke(request.id)
      } catch (error) {
        if (mounted.current && latestKey.current === request.key && latestId.current === request.id) setResult({ key: request.key, id: request.id, error })
      } finally {
        if (starting.current?.id === request.id) starting.current = null
        if (mounted.current && latestKey.current === request.key) setRequest(previous => previous?.id === request.id ? null : previous)
      }
    })()
  }, [id, request, routeKey])
  const begin = (invoke: (newId: string) => Promise<void>, verifiedPreviousTerminal = false) => {
    if (starting.current?.key === routeKey || id && (!nonzeroUUID(id) || !verifiedPreviousTerminal)) return false
    const nextId = crypto.randomUUID()
    starting.current = { key: routeKey, id: nextId }
    setResult(null)
    setRequest({ key: routeKey, id: nextId, invoke })
    const next = new URLSearchParams(params)
    next.set(routeKey, nextId)
    setParams(next, { replace: true })
    return true
  }
  const adopt = (originalId: string) => {
    if (id || !nonzeroUUID(originalId)) return false
    const next = new URLSearchParams(params)
    next.set(routeKey, originalId)
    setParams(next, { replace: true })
    return true
  }
  const rejectBeforeAcceptance = (rejectedId: string) => {
    // Only the transport adapter may call this after a definitive rejected
    // POST. It is never used for GET404 or unknown network/server results.
    if (latestKey.current !== routeKey || latestId.current !== rejectedId) return
    // The submitted callback belongs to the render before begin changed the
    // route. React Router's updater also captures that render's search params;
    // use the committed navigation instead of restoring an older operation ID.
    const next = new URLSearchParams(latestNavigation.current.params)
    if (next.get(routeKey) !== rejectedId) return
    next.delete(routeKey)
    latestNavigation.current.setParams(next, { replace: true })
  }
  return {
    id, valid: id === null || nonzeroUUID(id), begin, adopt, rejectBeforeAcceptance,
    pending: request?.key === routeKey && request.id === id,
    error: result?.key === routeKey && result.id === id ? result.error : null,
  }
}

export const larkLoginLocatorKey = (projectId: string, accountId?: string) => `cliAuth.${projectId}.lark-cli.${accountId ?? 'new'}`
export const taskLarkSwitchLocatorKey = (taskId: string) => `taskLarkSwitch.${taskId}`
