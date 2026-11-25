import { ArrowUpIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { PermissionModeIcon } from './PermissionModePicker'

// Above the message box while requests wait for an approval: how many, and a way to the first card (the reply waits there).
export function WaitingForApproval({ requests }: { requests: Schemas['PermissionRequest'][] }) {
  const first = requests[0]
  return (
    <p role="status" data-waiting-approval className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 rounded-ticket bg-riso-pink px-3 py-1.5 text-[13px] leading-snug font-black text-ink shadow-[inset_0_0_0_2px_var(--ink)]">
      <span className="inline-flex min-w-0 items-center gap-1.5">
        <PermissionModeIcon mode="supervised" className="size-4 shrink-0" />
        {t.permissions.request.waiting(requests.length)}
      </span>
      <span className="text-[12px] font-bold text-ink-700">{t.permissions.request.stopRejects}</span>
      {first && (
        <button
          type="button"
          onClick={() => document.querySelector(`[data-permission-request="${first.id}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })}
          className="ml-auto inline-flex h-7 cursor-pointer items-center gap-1 rounded-full bg-paper-raised px-3 text-[12.5px] font-black text-ink shadow-[inset_0_0_0_2px_var(--ink)] outline-none focus-visible:shadow-focus"
        >
          <ArrowUpIcon aria-hidden="true" className="size-3.5" strokeWidth={3} />
          {t.permissions.request.show}
        </button>
      )}
    </p>
  )
}
