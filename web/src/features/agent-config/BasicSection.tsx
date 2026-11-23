import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CircleAlertIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { ModelPicker } from '@/components/ModelPicker'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import { ThinkingLevelPicker } from '@/components/ThinkingLevelPicker'
import { ApiError, type Schemas } from '@/api/client'
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
  const [confirmingSwitch, setConfirmingSwitch] = useState(false)

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
  // The runtime is determined by the model provider: if the selected provider uses a different runtime, saving switches it (a new
  // session starts), so confirm first. The catalog says which runtime runs the provider; without it (the provider can't be changed then),
  // the runtime definitions do.
  const nextRuntime = model ? (providers.data?.providers.find((provider) => provider.id === model.provider)?.runtime ?? runtimes.forProvider(model.provider)?.id ?? agent.runtime) : agent.runtime
  const switchesRuntime = modelChanged && nextRuntime !== agent.runtime

  const save = () => {
    setNotice(null)
    const ruleError = name !== agent.name ? nameRuleError(name) : undefined
    setNameRule(ruleError)
    if (ruleError) return
    if (switchesRuntime) setConfirmingSwitch(true)
    else void submit()
  }

  // Failures don't throw: the confirm dialog closes as usual and the error shows in this section (field errors next to their inputs).
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
  // Per the contract, updating an Agent returns 409 agent_not_idle (because the Agent is replying) only when it switches the runtime.
  const notIdle = update.error instanceof ApiError && update.error.code === 'agent_not_idle'

  return (
    <Section id="basic" title={t.config.basic.title}>
      {/* Internal ID and display name sit in two columns on one row and wrap when narrow; model provider and model are laid out in two
          columns the same way (the grid inside ModelPicker).
          Fields on the same row align to the top: help text under one column grows downward without pushing the neighboring column down. */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] items-start gap-4">
        <IdField label={t.config.basic.id} id={agent.id} />
        <div className="grid content-start gap-1.5">
          <Label htmlFor="agent-name">{t.config.basic.name}</Label>
          <Input id="agent-name" aria-invalid={nameError ? true : undefined} value={name} onChange={(e) => setName(e.target.value)} />
          <FieldError message={nameError} />
        </div>
      </div>
      <div className="[&>div]:grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] [&>div]:gap-4 [&>div>p]:col-span-full">
        <ModelPicker agentId={agent.id} value={model} onChange={next => { if (!sameModel(next, model)) setThinkingLevel(null); if (next?.provider !== 'openai-codex') setServiceTier(null); setModel(next) }} error={fieldMessages(update.error, isModelField)}>
          {(selected, provider) => <>
            <ThinkingLevelPicker value={thinkingLevel} onChange={setThinkingLevel} levels={selected?.thinkingLevels ?? []} defaultLevel={selected?.defaultThinkingLevel} disabled={!selected?.thinkingLevels?.length} error={fieldMessages(update.error, field => field === 'thinkingLevel')} />
            {model?.provider === 'openai-codex' && <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(update.error, field => field === 'serviceTier')} />}
            {/* Shown as soon as a provider is selected (even before a model is chosen); when the catalog is unavailable, go by the model in
                the form or the Agent's current runtime. */}
            <RuntimeDifferences runtime={runtimes.get(provider?.runtime ?? nextRuntime)} className="col-span-full" />
          </>}
        </ModelPicker>
      </div>
      <p className="text-xs text-ink-500">{t.config.basic.thinking.nextRun}</p>
      {notIdle ? (
        <Alert variant="error">
          <CircleAlertIcon />
          <AlertDescription className="text-[13.5px] leading-[1.7]">{t.runtime.switch.notIdle}</AlertDescription>
        </Alert>
      ) : (
        !isAgentNameConflict(update.error) && <ErrorAlert error={unmatchedFieldError(update.error, shownInline)} />
      )}
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
      <ConfirmDialog
        open={confirmingSwitch}
        onOpenChange={setConfirmingSwitch}
        title={t.runtime.switch.title}
        body={t.runtime.switch.body(runtimes.name(agent.runtime), runtimes.name(nextRuntime))}
        confirmLabel={t.runtime.switch.confirm}
        onConfirm={submit}
      />
    </Section>
  )
}
