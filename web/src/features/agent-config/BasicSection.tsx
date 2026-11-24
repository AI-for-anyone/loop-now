import { useId, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { RunnerPicker } from '@/components/runner-picker/RunnerPicker'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import type { Schemas } from '@/api/client'
import { invalidateModelCatalog, useModelProviders, useUpdateAgent } from '@/api/queries'
import { t } from '@/i18n'
import { agentNameError, fieldMessages, isAgentNameConflict, isModelField, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { useRuntimeLookup } from '@/lib/runtime'
import { RuntimeDifferences } from './RuntimeNotes'
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
  const [notice, setNotice] = useState<string | null>(null)
  const [nameRule, setNameRule] = useState<string | undefined>(undefined)
  const runnerLabel = useId()

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
  // The runtime is determined by the model provider: the catalog says which runtime runs it; without the catalog, the runtime definitions
  // do. Saving a provider of another runtime switches the runtime from the next message, and the conversation goes with it.
  const nextRuntime = model ? (providers.data?.providers.find((provider) => provider.id === model.provider)?.runtime ?? runtimes.forProvider(model.provider)?.id ?? agent.runtime) : agent.runtime

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

  return (
    <Section id="basic" title={t.config.basic.title}>
      {/* Internal ID and display name sit in two columns on one row and wrap when narrow; the runner picker spans the width below them.
          Fields on the same row align to the top: help text under one column grows downward without pushing the neighboring column down. */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] items-start gap-4">
        <IdField label={t.config.basic.id} id={agent.id} />
        <div className="grid content-start gap-1.5">
          <Label htmlFor="agent-name">{t.config.basic.name}</Label>
          <Input id="agent-name" aria-invalid={nameError ? true : undefined} value={name} onChange={(e) => setName(e.target.value)} />
          <FieldError message={nameError} />
        </div>
      </div>
      <div className="grid gap-2">
        <span id={runnerLabel} className="text-[13.5px] leading-snug font-black">{t.runtime.picker.label}</span>
        <RunnerPicker
          labelledBy={runnerLabel}
          agentId={agent.id}
          value={model}
          thinkingLevel={thinkingLevel}
          invalid={Boolean(fieldMessages(update.error, isModelField))}
          onChoose={(choice) => {
            if (choice.model.provider !== 'openai-codex') setServiceTier(null)
            setModel(choice.model)
            setThinkingLevel(choice.thinkingLevel)
          }}
          onThinkingChange={setThinkingLevel}
        />
        <FieldError message={fieldMessages(update.error, isModelField)} />
        <FieldError message={fieldMessages(update.error, (field) => field === 'thinkingLevel')} />
      </div>
      {/* The Codex speed for openai-codex, and how the chosen runtime differs from the bundled one (going by the model in the form, or the
          Agent's current runtime when the catalog is unavailable). */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] items-start gap-4 empty:hidden">
        {model?.provider === 'openai-codex' && <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(update.error, (field) => field === 'serviceTier')} />}
        <RuntimeDifferences runtime={runtimes.get(nextRuntime)} className="col-span-full" />
      </div>
      <p className="text-xs text-ink-500">{t.config.basic.thinking.nextRun}</p>
      {!isAgentNameConflict(update.error) && <ErrorAlert error={unmatchedFieldError(update.error, shownInline)} />}
      {serverUpdated && dirty && (
        <Alert variant="warning" role="status">
          <AlertDescription className="text-[13.5px] font-bold text-ink">{t.config.basic.serverUpdated}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap items-center justify-end gap-x-4 gap-y-2.5">
        {dirty && <p role="status" className="mr-auto rounded-full bg-tint-yellow px-3 py-0.5 text-[13px] font-black">{t.common.unsaved}</p>}
        {notice && !dirty && (
          <p className="mr-auto rounded-full bg-tint-green px-3 py-0.5 text-[13px] font-black text-green-text" role="status">
            {notice}
          </p>
        )}
        <Button size="lg" disabled={!dirty || !model || name.trim() === '' || update.isPending} onClick={save}>
          {update.isPending ? t.common.saving : t.common.save}
        </Button>
      </div>
    </Section>
  )
}
