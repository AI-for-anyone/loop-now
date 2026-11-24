import { ArrowRightIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { t } from '@/i18n'
import { useRuntimeLookup, type AgentRuntime } from '@/lib/runtime'

// Where the conversation was handed to another runtime: a relay ticket between two blue dashed rules (the pink cut line stays for a
// session that started over). It names both runtimes with their marks and says how much of the saved conversation went along.
export function HandoffRow({ handoff }: { handoff: Schemas['ContextHandoff'] }) {
  const lookup = useRuntimeLookup()
  const copy = t.chat.handoff
  const from = lookup.name(handoff.fromRuntime), to = lookup.name(handoff.toRuntime)
  const parts =
    handoff.omittedMessages === 0
      ? [copy.whole(handoff.includedMessages)]
      : [copy.included(handoff.includedMessages), copy.omitted(handoff.omittedMessages)]
  if (handoff.images > 0) parts.push(copy.images(handoff.images))
  const side = (id: AgentRuntime, name: string) => {
    const runtime = lookup.get(id)
    return (
      <span className="inline-flex items-center gap-1.5">
        {runtime && <RuntimeMark runtime={runtime} className="size-6" />}
        <span className="font-display-latin text-[14px] font-extrabold">{name}</span>
      </span>
    )
  }
  return (
    <div role="separator" aria-label={copy.label(from, to, parts.join(copy.separator))} className="flex items-center gap-3">
      <span aria-hidden="true" className="h-0 flex-1 border-t-2 border-dashed border-riso-blue/70" />
      <div aria-hidden="true" className="grid justify-items-center gap-1">
        <span className="inline-flex items-center gap-2.5 rounded-full border-2 border-ink bg-paper-raised py-1 pr-3.5 pl-1.5 text-ink shadow-[3px_3px_0_var(--riso-blue)]">
          {side(handoff.fromRuntime, from)}
          <ArrowRightIcon className="size-4 text-riso-pink" strokeWidth={3} />
          {side(handoff.toRuntime, to)}
          <span className="sr-only">{copy.route(from, to)}</span>
        </span>
        <span className="text-center text-[12px] font-bold text-ink-700">{parts.join(' · ')}</span>
      </div>
      <span aria-hidden="true" className="h-0 flex-1 border-t-2 border-dashed border-riso-blue/70" />
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
      <span aria-hidden="true" className="h-0 flex-1 border-t-2 border-dotted border-ink/25" />
      <span aria-hidden="true" className="inline-flex min-w-0 items-center gap-1.5 font-mono text-[11.5px] text-ink-500">
        <span className="truncate">{shown(from)}</span>
        <ArrowRightIcon className="size-3 shrink-0" strokeWidth={2.6} />
        <span className="truncate font-bold text-ink-700">{shown(to)}</span>
      </span>
      <span aria-hidden="true" className="h-0 flex-1 border-t-2 border-dotted border-ink/25" />
    </div>
  )
}
