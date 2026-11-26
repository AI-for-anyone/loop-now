import { useId, useState } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { useQueryClient } from '@tanstack/react-query'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { RunnerPicker } from '@/components/runner-picker/RunnerPicker'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { unavailableNote } from '@/components/runner-picker/provider-access'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import type { Schemas } from '@/api/client'
import { invalidateModelCatalog, useModelProviders, useModels, useUpdateAgent } from '@/api/queries'
import { t } from '@/i18n'
import { agentNameError, describeError, fieldMessages, isAgentNameConflict, isModelField, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { useRuntimeLookup } from '@/lib/runtime'
import { choosableLevels } from '@/lib/thinking'
import { cn } from '@/lib/utils'
import { RuntimeDifferences } from './RuntimeNotes'
import { canRunAgent } from '@/lib/permissions'
import { Section } from './Section'
import { useDirtyForm } from '@/features/prompts/draft-navigation'

type ModelRef = Schemas['ModelRef']

const sameModel = (a: ModelRef | null, b: ModelRef | null) => a?.provider === b?.provider && a?.modelId === b?.modelId

export function BasicSection({ agent, busy }: { agent: Schemas['Agent']; busy: boolean }) {
  const update = useUpdateAgent(agent.id)
  const client = useQueryClient()
  const providers = useModelProviders(agent.id)
  const runtimes = useRuntimeLookup()
  const serverTier = agent.serviceTier === 'default' ? null : agent.serviceTier ?? null
  const [name, setName] = useState(agent.name)
  const [model, setModel] = useState<ModelRef | null>(agent.model)
  const [thinkingLevel, setThinkingLevel] = useState<Schemas['ThinkingLevel'] | null>(agent.thinkingLevel ?? null)
  const [serviceTier, setServiceTier] = useState<Schemas['CodexServiceTier'] | null>(serverTier)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [nameRule, setNameRule] = useState<string | undefined>(undefined)
  const runnerLabel = useId()
  const summaryId = useId()

  // The server values the form is based on. When the server refreshes with new values, fields the person hasn't edited follow the
  // latest values, and edited fields keep the person's input.
  const [base, setBase] = useState({ name: agent.name, model: agent.model, thinkingLevel: agent.thinkingLevel ?? null, serviceTier: serverTier })
  const [serverUpdated, setServerUpdated] = useState(false)
  if (agent.name !== base.name || !sameModel(agent.model, base.model) || (agent.thinkingLevel ?? null) !== base.thinkingLevel || serverTier !== base.serviceTier) {
    const edited = { name: name !== base.name, model: !sameModel(model, base.model), thinkingLevel: thinkingLevel !== base.thinkingLevel, serviceTier: serviceTier !== base.serviceTier }
    if (!edited.name) setName(agent.name)
    if (!edited.model) setModel(agent.model)
    if (!edited.thinkingLevel && !edited.model) setThinkingLevel(agent.thinkingLevel ?? null)
    if (!edited.serviceTier && !edited.model) setServiceTier(serverTier)
    if (!edited.model && agent.model.provider !== 'openai-codex') setServiceTier(null)
    // With unsaved edits, show a notice when the server brings a value that differs from the form (someone elsewhere changed a field
    // not edited here, or changed an edited field to some other value);
    // after saving our own edits the server's values match the form, so no notice.
    const unsaved = edited.name || edited.model || edited.thinkingLevel || edited.serviceTier
    const differs =
      (agent.name !== base.name && agent.name !== name) ||
      (!sameModel(agent.model, base.model) && !sameModel(agent.model, model)) ||
      ((agent.thinkingLevel ?? null) !== base.thinkingLevel && (agent.thinkingLevel ?? null) !== thinkingLevel) ||
      (serverTier !== base.serviceTier && serverTier !== serviceTier)
    if (unsaved && differs) setServerUpdated(true)
    setBase({ name: agent.name, model: agent.model, thinkingLevel: agent.thinkingLevel ?? null, serviceTier: serverTier })
  }

  const modelChanged = !sameModel(model, agent.model)
  const thinkingChanged = thinkingLevel !== (agent.thinkingLevel ?? null)
  const tierChanged = serviceTier !== serverTier
  const dirty = name !== agent.name || modelChanged || thinkingChanged || tierChanged
  useDirtyForm('basic', dirty)

  const currentProvider = model ? providers.data?.providers.find((provider) => provider.id === model.provider) : undefined
  // The runtime is determined by the model provider: the catalog says which runtime runs it; without the catalog, the runtime definitions
  // do. Saving a provider of another runtime switches the runtime from the next message, and the conversation goes with it.
  const nextRuntime = model ? (currentProvider?.runtime ?? runtimes.forProvider(model.provider)?.id ?? agent.runtime) : agent.runtime
  const activeRuntime = runtimes.get(nextRuntime)

  // Read the active provider's models (shares RunnerPicker's query cache) so the compact summary row shows the model name,
  // resolved default thinking level, and any catalog/availability warnings even while the popover is closed.
  const providerListed = model !== null && (providers.data === undefined || currentProvider !== undefined)
  const providerModels = useModels(model && providerListed ? model.provider : null)
  const chosenModel = model ? providerModels.data?.models.find((item) => item.id === model.modelId) : undefined

  const bundled = activeRuntime?.capabilities.bundled ?? false
  const summaryLead = bundled || !activeRuntime ? (model?.provider ?? activeRuntime?.name ?? '') : activeRuntime.name
  const modelDisplay = model ? (chosenModel ? t.config.basic.modelOption(chosenModel.name, chosenModel.id) : model.modelId) : ''
  const levels = choosableLevels(chosenModel)
  const thinkingSummary =
    !model || (chosenModel && levels.length === 0)
      ? null
      : thinkingLevel
        ? (t.config.basic.thinking.levels[thinkingLevel] ?? thinkingLevel)
        : chosenModel?.defaultThinkingLevel
          ? t.config.basic.thinking.defaultWithLevel(t.config.basic.thinking.levels[chosenModel.defaultThinkingLevel] ?? chosenModel.defaultThinkingLevel)
          : null
  const speedSummary = model?.provider === 'openai-codex' ? t.config.basic.serviceTier.levels[serviceTier ?? 'default'] : null

  const providerNote = currentProvider ? unavailableNote(currentProvider, agent.id) : null
  const notInCatalog = Boolean(model && providers.data && (!currentProvider || (providerModels.data !== undefined && !chosenModel)))
  const catalogError = providers.isError ? providers.error : providerModels.isError ? providerModels.error : null

  const save = () => {
    setNotice(null)
    const ruleError = name !== agent.name ? nameRuleError(name) : undefined
    setNameRule(ruleError)
    if (ruleError) return
    void submit()
  }

  // Failures don't throw: the error shows in this section (field errors next to their inputs).
  const submit = async () => {
    const body: Schemas['AgentUpdate'] = {}
    if (name !== agent.name) body.name = name
    if (modelChanged && model) body.model = model
    if (thinkingChanged) body.thinkingLevel = thinkingLevel
    if (tierChanged) body.serviceTier = serviceTier
    if (Object.keys(body).length === 0) return
    const saved = await update.mutateAsync(body).catch((error: unknown) => {
      // The plugin provider can't be chosen (not enabled in the project, not granted, etc.): the flags in the catalog may be stale,
      // so refetch it to show the reason.
      if (fieldMessages(error, (field) => field === 'model.provider')) void invalidateModelCatalog(client)
      return null
    })
    if (saved) {
      setServerUpdated(false)
      setNotice(busy ? t.config.savedWhileBusy : t.common.saved)
    }
  }

  const shownInline = (field: string) => field === 'name' || field === 'thinkingLevel' || field === 'serviceTier' || isModelField(field)
  const nameError = nameRule ?? agentNameError(update.error, agent.scope)
  const modelError = fieldMessages(update.error, isModelField)
  const thinkingError = fieldMessages(update.error, (field) => field === 'thinkingLevel')
  const modelInvalid = Boolean(modelError)

  return (
    <Section id="basic" title={t.config.basic.title}>
      {/* Display name and internal ID share a single horizontal row on sm+ screens, aligned to the top so field errors grow downward. */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <div className="grid min-w-0 flex-1 content-start gap-1.5">
          <Label htmlFor="agent-name">{t.config.basic.name}</Label>
          <Input id="agent-name" aria-invalid={nameError ? true : undefined} value={name} onChange={(e) => setName(e.target.value)} />
          <FieldError message={nameError} />
        </div>
        <div className="min-w-0 sm:w-[280px] sm:shrink-0">
          <IdField label={t.config.basic.id} id={agent.id} />
        </div>
      </div>

      <div className="grid gap-1.5">
        <span id={runnerLabel} className="text-[13px] leading-snug font-semibold text-ink [[data-skin=plate]_&]:font-black">
          {t.runtime.picker.label}
        </span>
        <PopoverPrimitive.Root open={pickerOpen} onOpenChange={setPickerOpen}>
          <PopoverPrimitive.Trigger asChild>
            <button
              type="button"
              aria-labelledby={`${runnerLabel} ${summaryId}`}
              aria-invalid={modelInvalid || undefined}
              className={cn(
                'group flex min-h-[52px] w-full cursor-pointer items-center gap-3 rounded-card border border-hairline bg-surface-control px-3.5 py-2 text-left transition-colors outline-none hover:border-hairline-strong hover:bg-surface-hover focus-visible:shadow-focus aria-expanded:border-ink aria-expanded:bg-surface-active/50 aria-invalid:border-red aria-invalid:ring-1 aria-invalid:ring-red [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-white [[data-skin=plate]_&]:aria-expanded:bg-tint-yellow',
              )}
            >
              {activeRuntime && <RuntimeMark runtime={activeRuntime} className="size-8 shrink-0" />}
              <span id={summaryId} className="flex min-w-0 flex-1 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[13.5px]">
                <span className="font-semibold text-ink [[data-skin=plate]_&]:font-bold">{summaryLead}</span>
                {modelDisplay && (
                  <>
                    <span aria-hidden="true" className="text-ink-500">·</span>
                    <span className="min-w-0 truncate font-mono text-[13px] text-ink">{modelDisplay}</span>
                  </>
                )}
                {thinkingSummary && (
                  <>
                    <span aria-hidden="true" className="text-ink-500">·</span>
                    <span className="text-[12.5px] text-ink-700">{thinkingSummary}</span>
                  </>
                )}
                {speedSummary && (
                  <>
                    <span aria-hidden="true" className="text-ink-500">·</span>
                    <span className="text-[12.5px] text-ink-700">{speedSummary}</span>
                  </>
                )}
              </span>
              <span className="ml-auto shrink-0 rounded-input border border-hairline-strong bg-surface-canvas px-2.5 py-1 text-[12.5px] font-medium text-ink group-hover:bg-surface-control [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black">
                {t.config.basic.changeRunner}
              </span>
            </button>
          </PopoverPrimitive.Trigger>
          <PopoverPrimitive.Portal>
            <PopoverPrimitive.Content
              role="dialog"
              aria-label={t.runtime.picker.label}
              side="bottom"
              align="start"
              sideOffset={8}
              collisionPadding={12}
              className="z-50 flex max-h-[min(520px,calc(100dvh-140px))] w-[min(460px,calc(100vw-32px))] flex-col overflow-hidden rounded-panel border border-hairline-strong bg-surface-elevated text-ink shadow-floating outline-none [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),6px_6px_0_2px_var(--riso-blue)]"
            >
              <div className="flex min-h-0 flex-1 flex-col">
                <RunnerPicker
                  variant="popover"
                  labelledBy={runnerLabel}
                  agentId={agent.id}
                  value={model}
                  thinkingLevel={thinkingLevel}
                  invalid={modelInvalid}
                  onChoose={(choice) => {
                    if (choice.model.provider !== 'openai-codex') setServiceTier(null)
                    setModel(choice.model)
                    setThinkingLevel(choice.thinkingLevel)
                  }}
                  onThinkingChange={setThinkingLevel}
                  // An Agent with its own folder, or asking before it acts, cannot move to a runtime that cannot do that (Pi).
                  offers={(definition) => canRunAgent(agent, definition)}
                />
              </div>
              <div className="flex shrink-0 items-center justify-end border-t border-hairline bg-surface-subtle/60 px-3 py-1.5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper">
                <Button type="button" size="sm" variant="secondary" onClick={() => setPickerOpen(false)}>
                  {t.config.basic.done}
                </Button>
              </div>
            </PopoverPrimitive.Content>
          </PopoverPrimitive.Portal>
        </PopoverPrimitive.Root>

        {!pickerOpen && providerNote && (
          <p role="note" className="text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
            {providerNote}
          </p>
        )}
        {!pickerOpen && notInCatalog && model && (
          <p role="note" className="text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
            {t.runtime.picker.notInCatalog(`${model.provider} / ${model.modelId}`)}
          </p>
        )}
        {!pickerOpen && catalogError && model && (
          <div className="grid gap-0.5 text-[12.5px] text-red-text">
            <p className="font-semibold [[data-skin=plate]_&]:font-bold">{t.config.basic.catalogUnavailable}</p>
            <p>{t.config.basic.catalogReason(describeError(catalogError))}</p>
            <p role="note" className="text-ink-700">
              {`${t.runtime.picker.inUse} `}
              <span className="font-mono">{`${model.provider} / ${model.modelId}`}</span>
            </p>
          </div>
        )}
        <FieldError message={modelError} />
        <FieldError message={thinkingError} />
      </div>

      {/* The Codex speed for openai-codex, and how the chosen runtime differs from the bundled one (going by the model in the form, or the
          Agent's current runtime when the catalog is unavailable). */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] items-start gap-3 empty:hidden">
        {model?.provider === 'openai-codex' && (
          <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(update.error, (field) => field === 'serviceTier')} />
        )}
        <RuntimeDifferences runtime={activeRuntime} className="col-span-full" />
      </div>

      {!isAgentNameConflict(update.error) && <ErrorAlert error={unmatchedFieldError(update.error, shownInline)} />}
      {serverUpdated && dirty && (
        <Alert variant="warning" role="status">
          <AlertDescription className="text-[13px] font-medium text-ink [[data-skin=plate]_&]:font-bold">{t.config.basic.serverUpdated}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-2">
        {dirty && (
          <p role="status" className="mr-auto rounded-full border border-hairline bg-tint-yellow px-3 py-0.5 text-[12.5px] font-medium [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black">
            {t.common.unsaved}
          </p>
        )}
        {notice && !dirty && (
          <p
            className="mr-auto rounded-full border border-hairline bg-tint-green px-3 py-0.5 text-[12.5px] font-medium text-green-text [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black"
            role="status"
          >
            {notice}
          </p>
        )}
        <Button size="sm" disabled={!dirty || !model || name.trim() === '' || update.isPending} onClick={save}>
          {update.isPending ? t.common.saving : t.common.save}
        </Button>
      </div>
    </Section>
  )
}
