import { useSyncExternalStore } from 'react'
import { XIcon } from 'lucide-react'
import { t } from '@/i18n'
import { toastStore } from '@/lib/toast'

// 轻提示的显示位置：右下角叠放，新的在下面；读屏按礼貌模式朗读。
export function Toasts() {
  const toasts = useSyncExternalStore(toastStore.subscribe, toastStore.get)
  return (
    <div aria-live="polite" data-toasts className="pointer-events-none fixed right-4 bottom-4 z-[60] flex max-w-[min(420px,calc(100vw-2rem))] flex-col gap-2">
      {toasts.map((toast) => (
        <div key={toast.id} className="pointer-events-auto flex min-w-0 items-start gap-3 rounded-card bg-ink px-4 py-3 text-[13.5px] leading-snug text-white shadow-[3px_3px_0_var(--riso-pink)]">
          <p className="min-w-0 flex-1 [overflow-wrap:anywhere]">
            {toast.text}
            {toast.link && (
              <>
                {' '}
                <a href={toast.link.href} target="_blank" rel="noopener noreferrer" className="font-bold text-riso-yellow underline">
                  {toast.link.label}
                </a>
              </>
            )}
          </p>
          <button type="button" aria-label={t.common.close} onClick={() => toastStore.dismiss(toast.id)} className="shrink-0 cursor-pointer rounded-full p-0.5 outline-none focus-visible:shadow-focus">
            <XIcon aria-hidden="true" className="size-4" />
          </button>
        </div>
      ))}
    </div>
  )
}
