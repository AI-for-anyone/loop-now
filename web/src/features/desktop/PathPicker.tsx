import { useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { pathPicker, type PathKind } from './desktop'

// A path input (children) with “选择…” (Choose…) after it in the desktop window: the button opens the system's own dialog for a folder
// or file, starting at the current value, and the chosen full path replaces the value, which can still be edited. Cancelling changes
// nothing. In a browser, or in a desktop shell older than the call, the input is shown on its own exactly as before.
export function PathPicker({ kind, value, onPick, disabled, children }: { kind: PathKind; value: string; onPick: (path: string) => void; disabled?: boolean; children: ReactNode }) {
  const [picking, setPicking] = useState(false)
  const pick = pathPicker()
  if (!pick) return children

  const choose = async () => {
    setPicking(true)
    try {
      const defaultPath = value.trim()
      const path = await pick(defaultPath ? { kind, defaultPath } : { kind })
      if (path) onPick(path)
    } catch {
      // The shell refused the call (it checks where the call comes from and what it asks): there is nothing to fill in, and typing works.
    } finally {
      setPicking(false)
    }
  }

  return (
    <div className="flex min-w-0 items-center gap-2 [&>:first-child]:min-w-0 [&>:first-child]:flex-1">
      {children}
      <Button type="button" variant="secondary" className="h-11" disabled={disabled || picking} onClick={() => void choose()}>
        {t.common.choosePath}
      </Button>
    </div>
  )
}
