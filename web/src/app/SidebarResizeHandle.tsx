import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react'
import { t } from '@/i18n'
import { SIDEBAR_WIDTH_MIN, SIDEBAR_WIDTH_STEP, clampSidebarWidth } from '@/lib/sidebar-width'
import { cn } from '@/lib/utils'

type SidebarResizeHandleProps = {
  width: number
  min: number
  max: number
  onPreview: (width: number | null) => void
  onCommit: (width: number) => void
  onReset: () => void
}

// Drag handle on the right edge of the permanent desktop sidebar (never mounted inside the narrow phone drawer).
// Invisible at rest; reveals a thin accent line on hover, while dragging, and on keyboard focus.
export function SidebarResizeHandle({ width, min, max, onPreview, onCommit, onReset }: SidebarResizeHandleProps) {
  const [dragging, setDragging] = useState(false)
  const cleanupRef = useRef<(() => void) | null>(null)

  useEffect(() => () => cleanupRef.current?.(), [])

  const startDrag = (kind: 'pointer' | 'mouse', startX: number, pointerId: number | null, target: HTMLDivElement) => {
    if (cleanupRef.current) return

    const startWidth = width
    let latestWidth = width
    const prevCursor = document.body.style.cursor
    const prevUserSelect = document.body.style.userSelect

    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    setDragging(true)

    if (kind === 'pointer' && pointerId !== null) {
      try {
        target.setPointerCapture?.(pointerId)
      } catch {
        // Synthetic pointer events have no active pointer to capture.
      }
    }

    const update = (clientX: number) => {
      const next = clampSidebarWidth(startWidth + (clientX - startX), window.innerWidth)
      if (next !== latestWidth) {
        latestWidth = next
        onPreview(next)
      }
    }

    const onPointerMove = (event: PointerEvent) => {
      if (pointerId !== null && event.pointerId !== pointerId) return
      update(event.clientX)
    }

    const onMouseMove = (event: MouseEvent) => {
      update(event.clientX)
    }

    const finish = (commit: boolean, unmounting = false) => {
      if (kind === 'pointer') {
        window.removeEventListener('pointermove', onPointerMove)
        window.removeEventListener('pointerup', onPointerEnd)
        window.removeEventListener('pointercancel', onPointerEnd)
        if (pointerId !== null) {
          try {
            if (target.hasPointerCapture?.(pointerId)) target.releasePointerCapture?.(pointerId)
          } catch {
            // Ignore when the pointer is already released or the element unmounted.
          }
        }
      } else {
        window.removeEventListener('mousemove', onMouseMove)
        window.removeEventListener('mouseup', onMouseEnd)
      }

      document.body.style.cursor = prevCursor
      document.body.style.userSelect = prevUserSelect
      cleanupRef.current = null

      if (!unmounting) {
        setDragging(false)
        onPreview(null)
        if (commit && latestWidth !== startWidth) onCommit(latestWidth)
      }
    }

    const onPointerEnd = (event: PointerEvent) => {
      if (pointerId !== null && event.pointerId !== pointerId) return
      finish(true)
    }

    const onMouseEnd = () => {
      finish(true)
    }

    if (kind === 'pointer') {
      window.addEventListener('pointermove', onPointerMove)
      window.addEventListener('pointerup', onPointerEnd)
      window.addEventListener('pointercancel', onPointerEnd)
    } else {
      window.addEventListener('mousemove', onMouseMove)
      window.addEventListener('mouseup', onMouseEnd)
    }

    cleanupRef.current = () => finish(false, true)
  }

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    startDrag('pointer', event.clientX, event.pointerId, event.currentTarget)
  }

  // Fallback for synthetic mouse-only sequences that do not emit pointerdown first.
  const onMouseDown = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    startDrag('mouse', event.clientX, null, event.currentTarget)
  }

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return
    if (event.key === 'ArrowLeft') {
      event.preventDefault()
      onCommit(clampSidebarWidth(width - SIDEBAR_WIDTH_STEP, window.innerWidth))
    } else if (event.key === 'ArrowRight') {
      event.preventDefault()
      onCommit(clampSidebarWidth(width + SIDEBAR_WIDTH_STEP, window.innerWidth))
    } else if (event.key === 'Home') {
      event.preventDefault()
      onCommit(SIDEBAR_WIDTH_MIN)
    } else if (event.key === 'End') {
      event.preventDefault()
      onCommit(max)
    }
  }

  return (
    <div
      role="separator"
      tabIndex={0}
      aria-orientation="vertical"
      aria-label={t.shell.resizeSidebar}
      aria-valuenow={width}
      aria-valuemin={min}
      aria-valuemax={max}
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onMouseDown={onMouseDown}
      onDoubleClick={onReset}
      onKeyDown={onKeyDown}
      className="window-no-drag group absolute inset-y-0 -right-[7px] z-30 w-[10px] cursor-col-resize touch-none select-none outline-none focus-visible:shadow-none"
    >
      <span
        aria-hidden="true"
        className={cn(
          'pointer-events-none absolute inset-y-0 left-[3px] transition-opacity duration-150',
          dragging
            ? 'w-[3px] bg-riso-blue opacity-100'
            : 'w-[2px] bg-riso-blue opacity-0 group-hover:opacity-100 group-focus-visible:w-[3px] group-focus-visible:bg-riso-blue group-focus-visible:opacity-100 group-focus-visible:shadow-focus',
        )}
      />
    </div>
  )
}
