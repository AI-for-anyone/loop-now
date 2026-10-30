import { useState, type FormEvent } from 'react'
import { PlusIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { selectClass } from '@/lib/field'
import type { Schemas } from '@/api/client'
import { putCliCredential, useAddCli, useCliTemplates, useDeleteCliCredential, useRemoveCli } from '@/api/queries'
import { t } from '@/i18n'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { CliToolStatus, CredentialStatus, WarningList } from './CredentialStatus'
import { Section } from './Section'
import { useSecretSubmit } from './useSecretSubmit'
import { ProjectCliSelection } from '@/features/project-credentials/AgentAccountSelection'
import { CredentialFields } from '@/features/project-credentials/CredentialFields'

const CLI_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

export function ClisSection({ agent }: { agent: Schemas['Agent'] }) {
  const templates = useCliTemplates()
  const addCli = useAddCli(agent.id)
  const [name, setName] = useState('')
  const [invalid, setInvalid] = useState<string | null>(null)

  const add = async (event: FormEvent) => {
    event.preventDefault()
    const cli = name.trim()
    if (!CLI_NAME.test(cli)) {
      setInvalid(t.config.clis.nameInvalid)
      return
    }
    setInvalid(null)
    if (await addCli.mutateAsync(cli).catch(() => null)) setName('')
  }

  const templateOf = (cli: string) => templates.data?.templates.find((tpl) => tpl.name === cli)
  // 模板还没拿到时不能录入凭证，说明原因。
  const unavailableReason = templates.data
    ? t.config.clis.credentialUnavailable.missing
    : templates.isError
      ? t.config.clis.credentialUnavailable.failed
      : t.config.clis.credentialUnavailable.loading

  return (
    <Section id="clis" title={t.config.clis.title} count={agent.clis.length}>
      {agent.clis.length === 0 && <p className="text-[13.5px] text-ink-500">{t.config.clis.empty}</p>}
      {templates.isError && !templates.data && (
        <div className="grid gap-1.5">
          <p className="text-[13.5px] font-bold text-red-text">{t.config.clis.templatesFailed}</p>
          <ErrorAlert error={templates.error} />
        </div>
      )}
      <ul className="grid gap-2.5">
        {agent.clis.map((cli) => (
          <CliItem key={cli.name} agentId={agent.id} configVersion={agent.configVersion} project={agent.scope === 'project'} cli={cli} template={templateOf(cli.name)} unavailableReason={unavailableReason} />
        ))}
      </ul>
      <form className="flex flex-wrap items-start gap-2" onSubmit={(e) => void add(e)} noValidate>
        <div className="grid min-w-0 flex-[1_1_240px] gap-1">
          <Input
            aria-label={t.config.clis.name}
            placeholder={t.config.clis.name}
            className="rounded-full px-4 font-mono text-[13.5px]"
            aria-invalid={invalid ? true : undefined}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <FieldError message={invalid ?? undefined} />
        </div>
        <Button type="submit" size="lg" variant="secondary" disabled={addCli.isPending}>
          <PlusIcon />
          {t.config.clis.add}
        </Button>
      </form>
      <ErrorAlert error={addCli.error} />
    </Section>
  )
}

function CliItem({
  agentId,
  configVersion,
  project,
  cli,
  template,
  unavailableReason,
}: {
  agentId: string
  configVersion: number
  project: boolean
  cli: Schemas['AgentCli']
  template: Schemas['CliTemplate'] | undefined
  unavailableReason: string
}) {
  const [editing, setEditing] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [deletingCredential, setDeletingCredential] = useState(false)
  const remove = useRemoveCli(agentId)
  const deleteCredential = useDeleteCliCredential(agentId)
  // 模板不可用时不保留打开状态，免得模板之后到达时对话框自己弹出。
  if (editing && !template) setEditing(false)

  // 每个 CLI 一张白底条目；有模板的 CLI 把凭证状态与录入按钮放在单独的浅蓝子框内。
  return (
    <li className="grid gap-2.5 rounded-card bg-white px-4 py-3.5" aria-label={cli.name}>
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
        <span className="font-mono text-[15px] font-bold">{cli.name}</span>
        {cli.hasTemplate ? (
          <Badge tone="info" className="text-riso-blue">
            {template ? t.config.clis.templateVersion(template.version) : t.config.clis.credential}
          </Badge>
        ) : (
          <span className="text-[13px] text-ink-500">{t.config.clis.markerOnly}</span>
        )}
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setRemoving(true)}>
          {t.common.remove}
        </Button>
      </div>
      {cli.hasTemplate && !template && <p className="text-[12.5px] text-ink-500">{unavailableReason}</p>}
      {template && <WarningList warnings={template.warnings} />}
      {template && <CliToolStatus template={template} />}
      {cli.hasTemplate && project && <ProjectCliSelection agentId={agentId} configVersion={configVersion} cli={cli.name} />}
      {cli.hasTemplate && !project && (
        <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2 rounded-input bg-tint-blue px-3.5 py-3 text-[13.5px]">
          <span className="font-black">{t.config.clis.credential}</span>
          <CredentialStatus credential={cli.credential} />
          <div className="ml-auto flex flex-wrap gap-x-2 gap-y-1">
            <Button
              size="xs"
              variant={cli.credential ? 'secondary' : 'ink'}
              className={cn(cli.credential && 'bg-white hover:bg-white')}
              disabled={!template}
              onClick={() => setEditing(true)}
            >
              {cli.credential ? t.config.clis.replaceCredential : t.config.clis.setCredential}
            </Button>
            {cli.credential && (
              <Button size="sm" variant="ghost" className="h-[34px]" onClick={() => setDeletingCredential(true)}>
                {t.common.delete}
              </Button>
            )}
          </div>
        </div>
      )}
      {editing && template && <CliCredentialDialog agentId={agentId} cli={cli} template={template} onClose={() => setEditing(false)} />}
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t.config.clis.removeTitle(cli.name)}
        body={project ? t.credentials.clis.removeProjectBody : cli.credential ? t.config.clis.removeBodyWithCredential : t.config.clis.removeBody}
        confirmLabel={t.common.remove}
        destructive
        onConfirm={() => remove.mutateAsync(cli.name)}
      />
      <ConfirmDialog
        open={deletingCredential}
        onOpenChange={setDeletingCredential}
        title={t.config.clis.deleteCredentialTitle(cli.name)}
        body={t.config.clis.deleteCredentialBody}
        confirmLabel={t.common.delete}
        destructive
        onConfirm={() => deleteCredential.mutateAsync(cli.name)}
      />
    </li>
  )
}

function CliCredentialDialog({ agentId, cli, template, onClose }: { agentId: string; cli: Schemas['AgentCli']; template: Schemas['CliTemplate']; onClose: () => void }) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(template.fields.map((field) => [field.name, field.default ?? (field.type === 'enum' ? field.options?.[0] ?? '' : '')])),
  )
  const [missing, setMissing] = useState<string[]>([])
  const { submit, pending, error, clearError } = useSecretSubmit(agentId)

  const save = async (event: FormEvent) => {
    event.preventDefault()
    clearError()
    const empty = template.fields.filter((field) => field.required && (values[field.name] ?? '') === '').map((field) => field.name)
    setMissing(empty)
    if (empty.length > 0) return
    const fields = Object.fromEntries(Object.entries(values).filter(([, value]) => value !== ''))
    if (await submit(() => putCliCredential(agentId, cli.name, fields))) onClose()
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[540px]">
        <DialogHeader>
          <DialogTitle>{t.config.clis.credentialTitle(cli.name)}</DialogTitle>
          {cli.credential && <DialogDescription>{t.config.clis.replaceHint}</DialogDescription>}
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => void save(e)} autoComplete="off" noValidate>
          <WarningList warnings={template.warnings} />
          {template.fields.map((field) => {
            const id = `cli-field-${field.name}`
            const message = missing.includes(field.name) ? t.common.required : fieldMessages(error, (f) => f === `fields.${field.name}`)
            if (field.multiline) return <CredentialFields key={field.name} fields={[field]} values={values} errors={message ? { [field.name]: message } : {}} disabled={pending} onChange={(name, value) => setValues(previous => ({ ...previous, [name]: value }))} />
            return (
              <div key={field.name} className="grid gap-1.5">
                <Label htmlFor={id}>{field.label}</Label>
                {field.type === 'enum' ? (
                  <select id={id} className={selectClass} value={values[field.name]} onChange={(e) => setValues((v) => ({ ...v, [field.name]: e.target.value }))}>
                    {(field.options ?? []).map((option) => (
                      <option key={option} value={option}>
                        {option}
                      </option>
                    ))}
                  </select>
                ) : (
                  <Input
                    id={id}
                    type={field.type === 'secret' ? 'password' : 'text'}
                    autoComplete={field.type === 'secret' ? 'new-password' : 'off'}
                    className={cn(field.type === 'string' && 'font-mono text-[13px]')}
                    aria-invalid={message ? true : undefined}
                    value={values[field.name]}
                    onChange={(e) => setValues((v) => ({ ...v, [field.name]: e.target.value }))}
                  />
                )}
                <FieldError message={message} />
              </div>
            )
          })}
          <ErrorAlert error={unmatchedFieldError(error, (f) => template.fields.some((field) => f === `fields.${field.name}`))} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={pending}>
              {t.common.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
