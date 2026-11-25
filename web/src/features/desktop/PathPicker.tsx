import { LoaderCircleIcon, MousePointerClickIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { pickPathOnServer, useServerPathPicker } from '@/api/path-picker'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { pathPicker, type PathKind, type PickPathRequest } from './desktop'

// A path input (children) with “选择…” (Choose…) after it: the button opens the system's own dialog for a folder or file, starting at the
// current value, and the chosen full path replaces the value, which can still be edited. Cancelling changes nothing.
// - In the desktop window the shell shows the dialog (as before: a refused call does nothing).
// - In a browser, when Loop Now's server says it can (Context.pathPicker), the server shows the dialog on this computer and answers with
//   the path. The dialog opens in front but usually without the keyboard, so the wait says where it is and to click it once; a refusal
//   (another dialog is open, this computer cannot show one, …) is said under the field. The request, and with it the dialog, is dropped
//   when the field goes away (its form closes).
// - Otherwise the input is shown on its own exactly as before.
export function PathPicker({ kind, value, onPick, disabled, children }: { kind: PathKind; value: string; onPick: (path: string) => void; disabled?: boolean; children: ReactNode }) {
  const desktop = pathPicker()
  const server = useServerPathPicker(!desktop)
  if (desktop) return <DesktopPathPicker pick={desktop} kind={kind} value={value} onPick={onPick} disabled={disabled}>{children}</DesktopPathPicker>
  if (server) return <ServerPathPicker kind={kind} value={value} onPick={onPick} disabled={disabled}>{children}</ServerPathPicker>
  return children
}

type PickerProps = { kind: PathKind; value: string; onPick: (path: string) => void; disabled?: boolean; children: ReactNode }

const requestFor = (kind: PathKind, value: string): PickPathRequest => {
  const defaultPath = value.trim()
  return defaultPath ? { kind, defaultPath } : { kind }
}

function DesktopPathPicker({ pick, kind, value, onPick, disabled, children }: PickerProps & { pick: (request: PickPathRequest) => Promise<string | null> }) {
  const [picking, setPicking] = useState(false)
  const choose = async () => {
    setPicking(true)
    try {
      const path = await pick(requestFor(kind, value))
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

function ServerPathPicker({ kind, value, onPick, disabled, children }: PickerProps) {
  const hintId = useId()
  const [waiting, setWaiting] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])

  const choose = async () => {
    if (waiting) return
    const signal = (controller.current = new AbortController()).signal
    setWaiting(true)
    setError(null)
    try {
      const path = await pickPathOnServer(requestFor(kind, value), signal)
      if (path) onPick(path)
    } catch (cause) {
      if (!signal.aborted) setError(cause)
    } finally {
      if (controller.current?.signal === signal) controller.current = null
      if (!signal.aborted) setWaiting(false)
    }
  }

  return (
    <div className="grid min-w-0 gap-1.5">
      <div className="flex min-w-0 items-center gap-2 [&>:first-child]:min-w-0 [&>:first-child]:flex-1">
        {children}
        {/* While the dialog is open the button stays focusable (aria-disabled), so the focus is still here when the browser comes back. */}
        <Button
          type="button"
          variant="secondary"
          className={cn('h-11', waiting && 'cursor-progress bg-riso-pink hover:bg-riso-pink')}
          disabled={disabled && !waiting}
          aria-disabled={waiting || undefined}
          aria-describedby={waiting ? hintId : undefined}
          onClick={() => void choose()}
        >
          {waiting && <LoaderCircleIcon aria-hidden="true" className="riso-spin size-4 motion-reduce:animate-none" strokeWidth={3} />}
          {waiting ? t.common.choosingPath : t.common.choosePath}
        </Button>
      </div>
      {waiting && (
        <p id={hintId} role="status" className="flex items-start gap-1.5 text-[12.5px] leading-snug font-bold text-ink-700">
          <MousePointerClickIcon aria-hidden="true" className="mt-px size-4 shrink-0 text-riso-pink" strokeWidth={2.5} />
          {t.common.choosingPathHint}
        </p>
      )}
      {error !== null && (
        <p role="alert" className="text-[13.5px] leading-snug font-bold text-red-text">
          {describeError(error)}
        </p>
      )}
    </div>
  )
}
