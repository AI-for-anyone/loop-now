import { useSyncExternalStore } from 'react'
import { XIcon } from 'lucide-react'
import { t } from '@/i18n'
import { toastStore } from '@/lib/toast'

// Where toasts appear: stacked in the top right so they never cover composer controls at the bottom; hovering pauses auto-dismiss.
// Screen readers announce them politely.
export function Toasts() {
  const toasts = useSyncExternalStore(toastStore.subscribe, toastStore.get)
  return (
    <div aria-live="polite" data-toasts className="pointer-events-none fixed top-4 right-4 z-[60] flex max-w-[min(420px,calc(100vw-2rem))] flex-col gap-2">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          data-toast
          onMouseEnter={() => toastStore.pause(toast.id)}
          onMouseLeave={() => toastStore.resume(toast.id)}
          onFocus={() => toastStore.pause(toast.id)}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) toastStore.resume(toast.id)
          }}
          className="pointer-events-auto flex min-w-0 items-start gap-3 rounded-card border border-hairline-strong bg-surface-elevated px-4 py-3 text-[13.5px] leading-snug text-ink shadow-floating"
        >
          <p className="min-w-0 flex-1 [overflow-wrap:anywhere]">
            {toast.text}
            {toast.link && (
              <>
                {' '}
                <a
                  href={toast.link.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="font-semibold text-ink underline underline-offset-2"
                >
                  {toast.link.label}
                </a>
              </>
            )}
          </p>
          <button
            type="button"
            aria-label={t.common.close}
            onClick={() => toastStore.dismiss(toast.id)}
            className="shrink-0 cursor-pointer rounded-ticket p-0.5 text-ink-500 outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus"
          >
            <XIcon aria-hidden="true" className="size-4" />
          </button>
        </div>
      ))}
    </div>
  )
}
