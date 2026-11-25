import { CheckIcon } from 'lucide-react'
import { useId } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { usePutTextGenerationSettings, useTextGenerationSettings } from '@/api/text-generation'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RunnerPicker, type RunnerLead } from '@/components/runner-picker/RunnerPicker'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { useRuntimeLookup, type RuntimeDefinition, type RuntimeLookup } from '@/lib/runtime'
import { runtimeSettingsPath } from '@/lib/scope'
import { AutomaticMark } from './marks'
import { selectionName, writerName } from './writer-text'

type Selection = Schemas['TextGenerationSelection']
type Writer = Schemas['TextGenerationWriter']

// Only CLIs that run with their own login on this computer write; the bundled Pi has no login of its own (its keys belong to a project or an
// Agent).
const writesHere = (definition: RuntimeDefinition) => definition.capabilities.accounts === 'local'

// Under "Automatic" in the rail: who that is now while it is in use, the rule while another one is chosen.
function AutomaticState({ automatic, writer, lookup }: { automatic: boolean; writer: Writer | null; lookup: RuntimeLookup }) {
  const copy = t.settings.writing
  if (!automatic) return <span className="text-[12px] font-bold text-ink-500">{copy.firstReady}</span>
  if (!writer) {
    return (
      <span className="riso-tilt inline-flex min-h-5 w-fit items-center rounded-[4px] px-1.5 text-[11.5px] font-black tracking-[0.06em] whitespace-nowrap text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_var(--red-text)] [--tilt:-3deg]">
        {copy.noneReady}
      </span>
    )
  }
  // Two lines rather than cut short: the name of who writes is the point.
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5 text-[12px] leading-snug font-black text-green-text">
      <span aria-hidden="true" className="size-[7px] shrink-0 -translate-y-px rounded-full bg-green shadow-[0_0_0_1.5px_var(--ink)]" />
      {/* The runtime's name is kept on one line (no-break spaces), so a long state wraps before it rather than inside it. */}
      <span className="min-w-0 [overflow-wrap:anywhere]">{copy.now(lookup.name(writer.runtime).replaceAll(' ', '\u00a0'))}</span>
    </span>
  )
}

// What "Automatic" is, beside the rail: its name with whether it is in use, the rule, who writes now while it is in use (a card like the
// rail's: the runtime's mark, its name, the model under it), or the way back to it.
function AutomaticPanel({ automatic, writer, lookup, saving, onUse }: { automatic: boolean; writer: Writer | null; lookup: RuntimeLookup; saving: boolean; onUse: () => void }) {
  const copy = t.settings.writing
  const runtime = writer ? lookup.get(writer.runtime) : undefined
  const level = writer?.thinkingLevel ? (t.config.basic.thinking.levels[writer.thinkingLevel] ?? writer.thinkingLevel) : null
  return (
    <div className="flex h-full min-w-0 flex-col gap-4 overflow-y-auto px-5 pt-5 pb-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <AutomaticMark className="size-10" />
        <h3 className="relative font-display-cn text-[26px] leading-none text-ink">
          <span aria-hidden="true" className="absolute inset-x-[-4px] bottom-[-2px] h-[45%] -rotate-1 bg-riso-yellow" />
          <span className="relative">{copy.automatic}</span>
        </h3>
        {automatic && (
          <p className="ml-1 inline-flex items-center gap-1 rounded-full bg-tint-green px-2.5 py-0.5 text-[12.5px] font-black text-green-text">
            <CheckIcon aria-hidden="true" className="size-3.5" strokeWidth={3} />
            {copy.usingAutomatic}
          </p>
        )}
      </div>
      <p className="max-w-[46ch] text-[13.5px] leading-relaxed text-ink-700">{copy.automaticBody}</p>
      {automatic && writer && (
        <div className="grid justify-items-start gap-2">
          <p className="font-display-latin text-[11px] font-extrabold tracking-[0.14em] text-ink-500 uppercase">{copy.writesNow}</p>
          <div className="grid max-w-full min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 rounded-input border-2 border-ink bg-paper-raised py-2 pr-4 pl-2.5 shadow-[3px_3px_0_var(--riso-pink)]">
            {runtime && <RuntimeMark runtime={runtime} />}
            <p className="grid min-w-0 gap-0.5">
              <span className="font-display-latin text-[15.5px] leading-tight font-extrabold [overflow-wrap:anywhere]">{lookup.name(writer.runtime)}</span>
              <span className="text-[12.5px] leading-snug font-bold text-ink-700 [overflow-wrap:anywhere]">{[writer.modelName || writer.modelId, level].filter(Boolean).join(' · ')}</span>
            </p>
          </div>
        </div>
      )}
      {!automatic && (
        <Button type="button" size="sm" className="self-start" disabled={saving} onClick={onUse}>
          {copy.useAutomatic}
        </Button>
      )}
    </div>
  )
}

// Who writes with AI, a section of global settings: "Automatic" first, then the CLIs on this computer with their models and levels (the
// runner picker). A choice is saved at once; while it is being saved the picker shows it, and a failed save shows what is saved again.
export function WritingSettings() {
  const copy = t.settings.writing
  const labelId = useId()
  const lookup = useRuntimeLookup()
  const settings = useTextGenerationSettings()
  const put = usePutTextGenerationSettings()

  if (!settings.data) return settings.error ? <ErrorAlert error={settings.error} /> : <p role="status" className="text-[13.5px] text-ink-500">{t.common.loading}</p>

  const { writer } = settings.data
  const selection: Selection | null = put.isPending ? put.variables.selection : settings.data.selection
  const automatic = selection === null
  const save = (next: Selection | null) => put.mutate({ selection: next })
  const lead: RunnerLead = {
    name: copy.automatic,
    mark: <AutomaticMark />,
    state: <AutomaticState automatic={automatic} writer={writer} lookup={lookup} />,
    stateText: !automatic ? copy.firstReady : writer ? copy.now(lookup.name(writer.runtime)) : copy.noneReady,
    panel: <AutomaticPanel automatic={automatic} writer={writer} lookup={lookup} saving={put.isPending} onUse={() => save(null)} />,
    thinkingNote: copy.thinkingNote,
  }

  return (
    <div className="grid gap-3">
      <p className="text-[13.5px] text-ink-700">{copy.hint}</p>
      <div className="grid gap-2">
        <span id={labelId} className="text-[13.5px] leading-snug font-black">
          {copy.who}
        </span>
        <RunnerPicker
          labelledBy={labelId}
          offers={writesHere}
          lead={lead}
          value={selection && { provider: selection.provider, modelId: selection.modelId }}
          thinkingLevel={selection?.thinkingLevel ?? null}
          onChoose={(choice) => save({ ...choice.model, ...(choice.thinkingLevel ? { thinkingLevel: choice.thinkingLevel } : {}) })}
          onThinkingChange={(level) => selection && save({ provider: selection.provider, modelId: selection.modelId, ...(level ? { thinkingLevel: level } : {}) })}
        />
      </div>
      <ErrorAlert error={put.error} />
      {!writer && (
        <Alert variant="warning" role="note">
          <AlertDescription className="grid justify-items-start gap-1.5 text-[13.5px] text-ink">
            <span>{copy.noWriter}</span>
            <Link to={runtimeSettingsPath} className="font-black underline decoration-riso-pink decoration-2 underline-offset-4">
              {copy.toRuntimes}
            </Link>
          </AlertDescription>
        </Alert>
      )}
      {writer?.fellBack && settings.data.selection && !put.isPending && (
        <Alert variant="warning" role="note">
          <AlertDescription className="text-[13.5px] text-ink">{copy.fellBack(selectionName(settings.data.selection, lookup), writerName(writer, lookup))}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
