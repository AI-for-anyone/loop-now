import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useBlocker } from 'react-router'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { skipsUnsavedPrompt } from '@/features/agent-config/unsaved-guard'
import { t } from '@/i18n'

import { DirtyContext, sameRenamedProjectPage } from './draft-navigation'

export function DraftNavigationGuard({ children, projectName }: { children: ReactNode; projectName?: string }) {
  const [forms, setForms] = useState<Record<string, boolean>>({})
  const register = useCallback((id: string, dirty: boolean | null) => {
    setForms(previous => {
      if (dirty === null) {
        if (!(id in previous)) return previous
        const next = { ...previous }; delete next[id]; return next
      }
      return previous[id] === dirty ? previous : { ...previous, [id]: dirty }
    })
  }, [])
  const value = useMemo(() => register, [register])
  const dirty = Object.values(forms).some(Boolean)
  const blocker = useBlocker(({ currentLocation, nextLocation }) =>
    dirty && currentLocation.pathname !== nextLocation.pathname && !skipsUnsavedPrompt(nextLocation.state)
      && !sameRenamedProjectPage(currentLocation.pathname, nextLocation.pathname, projectName),
  )
  const leaving = useRef(false)
  useEffect(() => { if (blocker.state === 'blocked') leaving.current = false }, [blocker.state])
  return <DirtyContext.Provider value={value}>{children}<ConfirmDialog
    open={blocker.state === 'blocked'}
    onOpenChange={open => { if (!open && !leaving.current && blocker.state === 'blocked') blocker.reset() }}
    title={t.common.unsavedTitle} body={t.common.unsavedLeave} confirmLabel={t.common.leave}
    onConfirm={() => { if (blocker.state === 'blocked') { leaving.current = true; blocker.proceed() } }}
  /></DirtyContext.Provider>
}