import { ArrowRightIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { t } from '@/i18n'
import { useRuntimeLookup, type AgentRuntime } from '@/lib/runtime'

// Where the conversation was handed over: a relay ticket between two blue dashed rules (the pink cut line stays for a session the user
// started over). By why it happened (ContextHandoff.reason): a change of runtime names both runtimes with their marks; a lost session
// and a task's start name the runtime's new session and say why in a line of their own. Every kind says how much of the saved
// conversation went along.
export function HandoffRow({ handoff }: { handoff: Schemas['ContextHandoff'] }) {
  const lookup = useRuntimeLookup()
  const copy = t.chat.handoff
  const from = lookup.name(handoff.fromRuntime), to = lookup.name(handoff.toRuntime)
  const parts =
    handoff.omittedMessages === 0
      ? [copy.whole(handoff.includedMessages)]
      : [copy.included(handoff.includedMessages), copy.omitted(handoff.omittedMessages)]
  if (handoff.images > 0) parts.push(copy.images(handoff.images))
  // A new session of the same runtime gets the conversation: the session was lost, a task started, or a change of runtime was undone
  // after the other runtime had run a message.
  const renewed = handoff.reason !== 'runtime_changed' || handoff.fromRuntime === handoff.toRuntime
  const said = parts.join(copy.separator)
  const label = handoff.reason === 'session_lost' ? copy.lostLabel(to, said) : handoff.reason === 'task_start' ? copy.taskLabel(to, said) : renewed ? copy.renewedLabel(to, said) : copy.label(from, to, said)
  const why = handoff.reason === 'session_lost' ? copy.lost : handoff.reason === 'task_start' ? copy.task : null
  const side = (id: AgentRuntime, name: string) => {
    const runtime = lookup.get(id)
    return (
      <span className="inline-flex items-center gap-1.5">
        {runtime && <RuntimeMark runtime={runtime} className="size-6" />}
        <span className="font-display-latin text-[13.5px] font-semibold [[data-skin=plate]_&]:text-[14px] [[data-skin=plate]_&]:font-extrabold">{name}</span>
      </span>
    )
  }
  return (
    <div role="separator" aria-label={label} className="flex items-center gap-3">
      <span aria-hidden="true" className="h-0 flex-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-riso-blue/70" />
      <div aria-hidden="true" className="grid justify-items-center gap-1">
        <span className="inline-flex items-center gap-2.5 rounded-full border border-hairline-strong bg-surface-subtle py-1 pr-3.5 pl-1.5 text-ink [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--riso-blue)]">
          {renewed ? (
            side(handoff.toRuntime, copy.renewed(to))
          ) : (
            <>
              {side(handoff.fromRuntime, from)}
              <ArrowRightIcon className="size-4 text-ink-500 [[data-skin=plate]_&]:text-riso-pink" strokeWidth={2.5} />
              {side(handoff.toRuntime, to)}
              <span className="sr-only">{copy.route(from, to)}</span>
            </>
          )}
        </span>
        {why && <span className="text-center text-[12.5px] font-semibold text-ink [[data-skin=plate]_&]:font-black">{why}</span>}
        <span className="text-center text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700">{parts.join(' · ')}</span>
      </div>
      <span aria-hidden="true" className="h-0 flex-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-riso-blue/70" />
    </div>
  )
}

// A change of model on the same runtime: a quiet dotted rule with the two models in small type.
export function ModelChangeLine({ from, to }: { from: Schemas['RunSelection']; to: Schemas['RunSelection'] }) {
  const ref = (selection: Schemas['RunSelection']) => `${selection.provider} · ${selection.modelId}`
  const sameProvider = from.provider === to.provider
  const shown = (selection: Schemas['RunSelection']) => (sameProvider ? selection.modelId : ref(selection))
  return (
    <div role="separator" aria-label={t.chat.modelChange(ref(from), ref(to))} className="flex items-center gap-2.5 px-6">
      <span aria-hidden="true" className="h-0 flex-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dotted [[data-skin=plate]_&]:border-ink/25" />
      <span aria-hidden="true" className="inline-flex min-w-0 items-center gap-1.5 font-mono text-[11.5px] text-ink-500">
        <span className="truncate">{shown(from)}</span>
        <ArrowRightIcon className="size-3 shrink-0" strokeWidth={2.4} />
        <span className="truncate font-semibold text-ink-700 [[data-skin=plate]_&]:font-bold">{shown(to)}</span>
      </span>
      <span aria-hidden="true" className="h-0 flex-1 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dotted [[data-skin=plate]_&]:border-ink/25" />
    </div>
  )
}
