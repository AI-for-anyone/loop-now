import { choosableLevels } from '@/lib/thinking'
import { memo, useEffect, useRef, useState, useId } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { keys, useModels, useUpdateAgent } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { RunnerPicker, type RunnerChoice } from '@/components/runner-picker/RunnerPicker'
import { usePermissionSettings } from '@/api/permissions'
import { PermissionModePicker } from '@/features/permissions/PermissionModePicker'
import { canRunAgent } from '@/lib/permissions'
import { t } from '@/i18n'
import { useRuntimeLookup } from '@/lib/runtime'
import { cn } from '@/lib/utils'

// How long "applies from the next message" stays beside the chip after a change.
const SAVED_MS = 4000

// Who runs the Agent now, on the message box: the runtime's mark, then for a CLI on this computer "name · model · level", for the bundled
// runtime "provider · model · level" (its mark says it is built in; the details spell it out). It opens the runner picker; a choice is saved
// at once and applies from the next message, including messages nobody types. Memoized: the chat page re-renders with every streamed chunk.
export const RunnerChip = memo(function RunnerChip({ agent }: { agent: Schemas['Agent'] }) {
  const lookup = useRuntimeLookup()
  const client = useQueryClient()
  const update = useUpdateAgent(agent.id)
  const [open, setOpen] = useState(false)
  const modeLabel = useId()
  // The changes being saved, merged in the order they were made and shown until every save has come back (a level chosen while a model
  // change is still saving must not hide it); and why a save failed.
  const [draft, setDraft] = useState<Schemas['AgentUpdate']>({})
  const inFlight = useRef(0)
  // The answer to the change made last reflects every change before it (the server applies them in order); answers may come back out of
  // order, so only that one goes into the cache.
  const latest = useRef(0)
  const [failure, setFailure] = useState<unknown>(null)
  const [savedAt, setSavedAt] = useState<number | null>(null)
  useEffect(() => {
    if (savedAt === null) return
    const timer = setTimeout(() => setSavedAt(null), SAVED_MS)
    return () => clearTimeout(timer)
  }, [savedAt])

  const ref = draft.model ?? agent.model
  const level = 'thinkingLevel' in draft ? (draft.thinkingLevel ?? null) : (agent.thinkingLevel ?? null)
  const runtime = draft.model ? lookup.forProvider(ref.provider) : lookup.get(agent.runtime)
  const models = useModels(ref.provider)
  const model = models.data?.models.find((candidate) => candidate.id === ref.modelId)
  const shownLevel = level ?? model?.defaultThinkingLevel ?? null
  const levelText = shownLevel && (!model || choosableLevels(model).length > 0) ? (t.config.basic.thinking.levels[shownLevel] ?? shownLevel) : null
  const bundled = runtime?.capabilities.bundled ?? false
  const lead = bundled || !runtime ? ref.provider : runtime.name
  // The permission mode shows on the chip only when it is not the default for new Agents (as T3).
  const mode = draft.permissionMode ?? agent.permissionMode
  const defaultMode = usePermissionSettings().data?.defaultMode
  const modeText = defaultMode !== undefined && mode !== defaultMode ? t.permissions.modes[mode].name : null
  const parts = [lead, ref.modelId, levelText, modeText].filter((part): part is string => Boolean(part))
  const copy = t.runtime.picker.chip
  const detail = copy.detail(runtime?.name ?? agent.runtime, bundled ? copy.bundled : copy.local, ref.provider, model ? t.config.basic.modelOption(model.name, model.id) : ref.modelId, levelText)

  const save = (body: Schemas['AgentUpdate'], close: boolean) => {
    setDraft((current) => ({ ...current, ...body }))
    setFailure(null)
    inFlight.current += 1
    const turn = ++latest.current
    void update
      .mutateAsync(body)
      .then((next) => {
        if (turn === latest.current) client.setQueryData(keys.agent(agent.id), next)
        setSavedAt(Date.now())
        if (close) setOpen(false)
      })
      .catch((error: unknown) => setFailure(error))
      .finally(() => {
        inFlight.current -= 1
        if (inFlight.current === 0) setDraft({})
      })
  }
  const choose = ({ model: next, thinkingLevel }: RunnerChoice) => {
    // Only openai-codex takes a speed: leaving it clears the speed the Agent had.
    const tier = next.provider !== 'openai-codex' && agent.serviceTier && agent.serviceTier !== 'default' ? { serviceTier: null } : {}
    save({ model: next, thinkingLevel, ...tier }, true)
  }

  return (
    <span className="inline-flex min-w-0 items-center gap-2">
      <PopoverPrimitive.Root
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (next) setFailure(null)
        }}
      >
        <PopoverPrimitive.Trigger
          type="button"
          title={detail}
          aria-label={copy.label(parts.join(' · '))}
          className={cn(
            'inline-flex h-7 max-w-full min-w-0 cursor-pointer items-center gap-1.5 rounded-lg border border-transparent bg-transparent pr-2 pl-1 text-[12px] font-medium whitespace-nowrap text-ink-700 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus aria-expanded:bg-surface-active aria-expanded:text-ink [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:pr-2.5 [[data-skin=plate]_&]:pl-0.5 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-white [[data-skin=plate]_&]:aria-expanded:bg-riso-yellow',
          )}
        >
          {runtime && <RuntimeMark runtime={runtime} className="size-[18px] [[data-skin=plate]_&]:size-[22px]" />}
          {parts.map((part, index) => (
            <span key={index} className={cn('min-w-0 truncate', index === 1 && 'font-mono text-[11.5px] font-medium [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-bold', index > 0 && 'text-ink-500 [[data-skin=plate]_&]:text-ink-700')}>
              {index > 0 && <span aria-hidden="true" className="mr-1.5 font-sans text-ink-500">·</span>}
              {part}
            </span>
          ))}
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            role="dialog"
            aria-label={copy.title}
            side="top"
            align="start"
            sideOffset={10}
            collisionPadding={12}
            className="z-50 flex max-h-[min(520px,calc(100dvh-140px))] w-[min(460px,calc(100vw-24px))] flex-col overflow-hidden rounded-card border border-hairline-strong bg-surface-elevated text-ink shadow-floating outline-none [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),6px_6px_0_2px_var(--riso-blue)]"
          >
            {/* Compact header strip: picker title and save timing note. */}
            <div className="flex shrink-0 items-center justify-between gap-2 border-b border-hairline bg-surface-subtle/60 px-3 py-2 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow">
              <span className="shrink-0 text-[12.5px] leading-tight font-semibold text-ink [[data-skin=plate]_&]:font-black">{copy.title}</span>
              <span
                title={Object.keys(draft).length > 0 ? copy.saving : copy.hint}
                className="min-w-0 truncate text-[11.5px] text-ink-500 [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700"
              >
                {Object.keys(draft).length > 0 ? copy.saving : copy.hint}
              </span>
            </div>
            <div className="flex min-h-0 flex-1 flex-col">
              <RunnerPicker
                variant="popover"
                agentId={agent.id}
                value={ref}
                thinkingLevel={level}
                onChoose={choose}
                onThinkingChange={(next) => save({ thinkingLevel: next }, false)}
                // An Agent with its own folder, or asking before it acts, cannot move to a runtime that cannot do that (Pi).
                offers={(definition) => canRunAgent({ workingDirectory: agent.workingDirectory, permissionMode: mode }, definition)}
              />
            </div>
            {/* Compact permissions footer: saved at once, applies from the next message. */}
            <div className="flex shrink-0 flex-col gap-1.5 border-t border-hairline bg-surface-subtle/40 px-3 py-2 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper">
              <span id={modeLabel} className="text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink">
                {t.permissions.pickerTitle}
              </span>
              <PermissionModePicker variant="compact" value={mode} runtime={runtime} labelledBy={modeLabel} onChange={(next) => save({ permissionMode: next }, false)} />
            </div>
            {failure !== null && (
              <div className="shrink-0 border-t border-hairline px-3 py-2 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/30">
                <ErrorAlert error={failure} />
              </div>
            )}
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>
      {savedAt !== null && (
        <span role="status" className="text-[12px] font-semibold whitespace-nowrap text-green-text [[data-skin=plate]_&]:font-black">
          {copy.saved}
        </span>
      )}
    </span>
  )
})
