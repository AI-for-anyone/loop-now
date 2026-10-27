import { useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { IdField } from '@/components/IdField'
import { ModelPicker } from '@/components/ModelPicker'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import { ThinkingLevelPicker } from '@/components/ThinkingLevelPicker'
import type { Schemas } from '@/api/client'
import { useUpdateAgent } from '@/api/queries'
import { t } from '@/i18n'
import { agentNameError, fieldMessages, isAgentNameConflict, isModelField, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { Section } from './Section'
import { useDirtyForm } from '@/features/prompts/draft-navigation'

type ModelRef = Schemas['ModelRef']

const sameModel = (a: ModelRef | null, b: ModelRef | null) => a?.provider === b?.provider && a?.modelId === b?.modelId

export function BasicSection({ agent, busy }: { agent: Schemas['Agent']; busy: boolean }) {
  const update = useUpdateAgent(agent.id)
  const serverTier = agent.serviceTier === 'default' ? null : agent.serviceTier ?? null
  const [name, setName] = useState(agent.name)
  const [model, setModel] = useState<ModelRef | null>(agent.model)
  const [thinkingLevel, setThinkingLevel] = useState<Schemas['ThinkingLevel'] | null>(agent.thinkingLevel ?? null)
  const [serviceTier, setServiceTier] = useState<Schemas['CodexServiceTier'] | null>(serverTier)
  const [notice, setNotice] = useState<string | null>(null)
  const [nameRule, setNameRule] = useState<string | undefined>(undefined)

  // 表单所依据的服务端值。服务端刷新出新值时：人没改过的项跟随最新值，改过的项保留人的输入。
  const [base, setBase] = useState({ name: agent.name, model: agent.model, thinkingLevel: agent.thinkingLevel ?? null, serviceTier: serverTier })
  const [serverUpdated, setServerUpdated] = useState(false)
  if (agent.name !== base.name || !sameModel(agent.model, base.model) || (agent.thinkingLevel ?? null) !== base.thinkingLevel || serverTier !== base.serviceTier) {
    const edited = { name: name !== base.name, model: !sameModel(model, base.model), thinkingLevel: thinkingLevel !== base.thinkingLevel, serviceTier: serviceTier !== base.serviceTier }
    if (!edited.name) setName(agent.name)
    if (!edited.model) setModel(agent.model)
    if (!edited.thinkingLevel && !edited.model) setThinkingLevel(agent.thinkingLevel ?? null)
    if (!edited.serviceTier && !edited.model) setServiceTier(serverTier)
    if (!edited.model && agent.model.provider !== 'openai-codex') setServiceTier(null)
    // 有未保存的修改时，服务端带来了与表单不同的值（别处改了没改过的项，或把改过的项改成了别的值）就提示；
    // 保存自己的修改后服务端的值与表单相同，不提示。
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

  const save = async () => {
    setNotice(null)
    const ruleError = name !== agent.name ? nameRuleError(name) : undefined
    setNameRule(ruleError)
    if (ruleError) return
    const body: Schemas['AgentUpdate'] = {}
    if (name !== agent.name) body.name = name
    if (modelChanged && model) body.model = model
    if (thinkingChanged) body.thinkingLevel = thinkingLevel
    if (tierChanged) body.serviceTier = serviceTier
    if (Object.keys(body).length === 0) return
    const saved = await update.mutateAsync(body).catch(() => null)
    if (saved) {
      setServerUpdated(false)
      setNotice(busy ? t.config.savedWhileBusy : t.common.saved)
    }
  }

  const shownInline = (field: string) => field === 'name' || field === 'thinkingLevel' || field === 'serviceTier' || isModelField(field)
  const nameError = nameRule ?? agentNameError(update.error, agent.scope)

  return (
    <Section id="basic" title={t.config.basic.title}>
      {/* 内部 ID 与显示名称一行两列，窄时换行；模型提供方与模型同样按两列排（ModelPicker 内部的网格）。 */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] gap-4">
        <IdField label={t.config.basic.id} id={agent.id} />
        <div className="grid content-start gap-1.5">
          <Label htmlFor="agent-name">{t.config.basic.name}</Label>
          <Input id="agent-name" aria-invalid={nameError ? true : undefined} value={name} onChange={(e) => setName(e.target.value)} />
          <FieldError message={nameError} />
        </div>
      </div>
      <div className="[&>div]:grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] [&>div]:gap-4 [&>div>p]:col-span-full">
        <ModelPicker value={model} onChange={next => { if (!sameModel(next, model)) setThinkingLevel(null); if (next?.provider !== 'openai-codex') setServiceTier(null); setModel(next) }} error={fieldMessages(update.error, isModelField)}>
          {selected => <>
            <ThinkingLevelPicker value={thinkingLevel} onChange={setThinkingLevel} levels={selected?.thinkingLevels ?? []} defaultLevel={selected?.defaultThinkingLevel} disabled={!selected?.thinkingLevels?.length} error={fieldMessages(update.error, field => field === 'thinkingLevel')} />
            {model?.provider === 'openai-codex' && <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(update.error, field => field === 'serviceTier')} />}
          </>}
        </ModelPicker>
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
        <Button size="lg" disabled={!dirty || !model || name.trim() === '' || update.isPending} onClick={() => void save()}>
          {update.isPending ? t.common.saving : t.common.save}
        </Button>
      </div>
    </Section>
  )
}
