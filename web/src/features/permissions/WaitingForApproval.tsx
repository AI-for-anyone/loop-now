import { ArrowUpIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { PermissionModeIcon } from './PermissionModePicker'

// Above the message box while requests wait for an approval: how many, and a way to the first card (the reply waits there).
// onLocate: show a reply that is not loaded (the chat page locates it by run).
export function WaitingForApproval({ requests, onLocate }: { requests: Schemas['PermissionRequest'][]; onLocate?: (runId: string) => void }) {
  const first = requests[0]
  return (
    <p role="status" data-waiting-approval className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 rounded-ticket border border-hairline-strong bg-tint-yellow px-3 py-1.5 text-[13px] leading-snug font-semibold text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]">
      <span className="inline-flex min-w-0 items-center gap-1.5">
        <PermissionModeIcon mode="supervised" className="size-4 shrink-0" />
        {t.permissions.request.waiting(requests.length)}
      </span>
      <span className="text-[12px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{t.permissions.request.stopRejects}</span>
      {first && (
        <button
          type="button"
          onClick={() => {
            const card = document.querySelector(`[data-permission-request="${first.id}"]`)
            // The reply that asked is not loaded (an earlier reply is being viewed): locate it.
            if (card) card.scrollIntoView({ block: 'center', behavior: 'smooth' })
            else if (onLocate) onLocate(first.runId)
          }}
          className="ml-auto inline-flex h-7 cursor-pointer items-center gap-1 rounded-full border border-hairline-strong bg-surface-elevated px-3 text-[12.5px] font-semibold text-ink outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]"
        >
          <ArrowUpIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
          {t.permissions.request.show}
        </button>
      )}
    </p>
  )
}
