import { useId, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { RunnerPicker } from '@/components/runner-picker/RunnerPicker'
import { RuntimeDifferences } from '@/features/agent-config/RuntimeNotes'
import { PromptWriter } from '@/features/writing/PromptWriter'
import { CodexServiceTierPicker } from '@/components/CodexServiceTierPicker'
import type { Schemas } from '@/api/client'
import { useCreateAgent } from '@/api/queries'
import { t } from '@/i18n'
import { agentNameError, fieldMessages, isAgentNameConflict, isModelField, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { useRuntimeLookup } from '@/lib/runtime'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'

// The New Agent dialog; the Agent is created in the current project or the platform. The list header and the first-use empty
// state open the same dialog, and AgentsPage holds its open/closed state. Who runs it comes first: the runner picker (runtime, model,
// thinking level), then the name and the prompt.
export function CreateAgentDialog({ scope, onClose }: { scope: Scope; onClose: () => void }) {
  const navigate = useNavigate()
  const runnerLabel = useId()
  const create = useCreateAgent(scopeKey(scope))
  const runtimes = useRuntimeLookup()
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
  const modelError = missing.model ?? fieldMessages(create.error, isModelField)
  // How the chosen runtime differs from the bundled one, worth knowing the moment it is chosen.
  const chosenRuntime = model ? runtimes.forProvider(model.provider) : undefined

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent kind="create" className="sm:max-w-[860px]">
        <DialogHeader>
          <DialogTitle>{t.createAgent.title}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-[18px]" onSubmit={(e) => void submit(e)} noValidate>
          <div className="grid gap-2">
            <span id={runnerLabel} className="text-[13.5px] leading-snug font-black">{t.runtime.picker.label}</span>
            <RunnerPicker
              labelledBy={runnerLabel}
              newIn={scopeKey(scope)}
              value={model}
              thinkingLevel={thinkingLevel}
              invalid={Boolean(modelError)}
              onChoose={(choice) => {
                if (choice.model.provider !== 'openai-codex') setServiceTier(null)
                setModel(choice.model)
                setThinkingLevel(choice.thinkingLevel)
              }}
              onThinkingChange={setThinkingLevel}
            />
            <FieldError message={modelError} />
            <FieldError message={fieldMessages(create.error, (field) => field === 'thinkingLevel')} />
          </div>
          {(model?.provider === 'openai-codex' || chosenRuntime) && (
            <div className={cn('grid items-start gap-4', model?.provider === 'openai-codex' && chosenRuntime && 'sm:grid-cols-2')}>
              {model?.provider === 'openai-codex' && <CodexServiceTierPicker value={serviceTier} onChange={setServiceTier} error={fieldMessages(create.error, (field) => field === 'serviceTier')} />}
              <RuntimeDifferences runtime={chosenRuntime} brief />
            </div>
          )}
          <div className="grid gap-1.5">
            <Label htmlFor="create-agent-name">{t.config.basic.name}</Label>
            <Input id="create-agent-name" value={name} aria-invalid={nameError ? true : undefined} onChange={(e) => setName(e.target.value)} />
            <FieldError message={nameError} />
          </div>
          {/* Write with AI knows the project (none for a platform Agent) and the name typed so far, when it is a valid name. */}
          <PromptWriter
            label={<Label htmlFor="create-agent-prompt">{t.config.basic.prompt}</Label>}
            text={prompt}
            onReplace={setPrompt}
            ask={{ target: 'agent', ...(scope.kind === 'project' ? { projectId: scope.project.id } : {}), ...(name.trim() && !nameRuleError(name) ? { agentName: name } : {}) }}
          >
            {(writing) => <Textarea id="create-agent-prompt" rows={4} placeholder={t.config.basic.promptPlaceholder} value={prompt} disabled={writing} onChange={(e) => setPrompt(e.target.value)} />}
          </PromptWriter>
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
