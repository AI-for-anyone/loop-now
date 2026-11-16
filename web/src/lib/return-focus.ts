import { useRef } from 'react'

// Radix dialogs and drawers only return focus to their Trigger on close; the console's dialogs and drawers are opened and closed by state
// or by the URL and have no Trigger, so focus would land on the page body. This records the element that had focus before opening and
// gives focus back to it on close.
// The caller's own handlers run first; nothing is overridden when the caller has already prevented the default.
export function useReturnFocus(onOpenAutoFocus?: (event: Event) => void, onCloseAutoFocus?: (event: Event) => void) {
  const target = useRef<HTMLElement | null>(null)
  return {
    onOpenAutoFocus: (event: Event) => {
      target.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      onOpenAutoFocus?.(event)
    },
    onCloseAutoFocus: (event: Event) => {
      onCloseAutoFocus?.(event)
      if (event.defaultPrevented) return
      event.preventDefault()
      if (target.current?.isConnected) target.current.focus()
    },
  }
}
