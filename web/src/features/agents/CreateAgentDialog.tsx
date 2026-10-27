import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { ModelPicker } from '@/components/ModelPicker'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import { ThinkingLevelPicker } from '@/components/ThinkingLevelPicker'
import type { Schemas } from '@/api/client'
import { useCreateAgent } from '@/api/queries'
import { t } from '@/i18n'
import { agentNameError, fieldMessages, isAgentNameConflict, isModelField, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'

// 新建 Agent 对话框，Agent 建在当前项目或平台中。列表头与首次使用的空状态打开的是同一个对话框，由 AgentsPage 持有开合状态。
export function CreateAgentDialog({ scope, onClose }: { scope: Scope; onClose: () => void }) {
  const navigate = useNavigate()
  const create = useCreateAgent(scopeKey(scope))
  const [name, setName] = useState('')
  const [model, setModel] = useState<Schemas['ModelRef'] | null>(null)
  const [thinkingLevel, setThinkingLevel] = useState<Schemas['ThinkingLevel'] | null>(null)
  const [serviceTier, setServiceTier] = useState<Schemas['CodexServiceTier'] | null>(null)
  const [prompt, setPrompt] = useState('')
  const [missing, setMissing] = useState<{ name?: string; model?: string }>({})

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const next = { name: name.trim() ? nameRuleError(name) : t.createAgent.nameRequired, model: model ? undefined : t.createAgent.modelRequired }
    setMissing(next)
    if (next.name || next.model || !model) return
    const agent = await create.mutateAsync({ name, model, ...(thinkingLevel ? { thinkingLevel } : {}), ...(model.provider === 'openai-codex' && serviceTier ? { serviceTier } : {}), ...(prompt ? { prompt } : {}) }).catch(() => null)
    if (agent) {
      onClose()
      void navigate(agentPath(scope, agent.id, 'config'))
    }
  }

  const nameError = missing.name ?? agentNameError(create.error, scope.kind)

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t.createAgent.title}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-[18px]" onSubmit={(e) => void submit(e)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor="create-agent-name">{t.config.basic.name}</Label>
            <Input id="create-agent-name" value={name} aria-invalid={nameError ? true : undefined} onChange={(e) => setName(e.target.value)} />
            <FieldError message={nameError} />
          </div>
          <ModelPicker value={model} onChange={next => { if (next?.provider !== model?.provider || next?.modelId !== model?.modelId) setThinkingLevel(null); if (next?.provider !== 'openai-codex') setServiceTier(null); setModel(next) }} error={missing.model ?? fieldMessages(create.error, isModelField)}>
            {selected => <>
              <ThinkingLevelPicker value={thinkingLevel} onChange={setThinkingLevel} levels={selected?.thinkingLevels ?? []} defaultLevel={selected?.defaultThinkingLevel} disabled={!selected?.thinkingLevels?.length} error={fieldMessages(create.error, field => field === 'thinkingLevel')} />
              {model?.provider === 'openai-codex' && <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(create.error, field => field === 'serviceTier')} />}
            </>}
          </ModelPicker>
          <div className="grid gap-1.5">
            <Label htmlFor="create-agent-prompt">{t.config.basic.prompt}</Label>
            <Textarea id="create-agent-prompt" rows={4} placeholder={t.config.basic.promptPlaceholder} value={prompt} onChange={(e) => setPrompt(e.target.value)} />
          </div>
          {!isAgentNameConflict(create.error) && <ErrorAlert error={unmatchedFieldError(create.error, (field) => field === 'name' || field === 'thinkingLevel' || field === 'serviceTier' || isModelField(field))} />}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {t.createAgent.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
