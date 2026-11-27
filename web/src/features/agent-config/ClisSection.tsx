import { useId, useState, type FormEvent } from 'react'
import { ChevronDownIcon, PlusIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { selectClass } from '@/lib/field'
import type { Schemas } from '@/api/client'
import { useCredentialSelections } from '@/api/project-credentials'
import { putCliCredential, useAddCli, useCliTemplates, useDeleteCliCredential, useRemoveCli } from '@/api/queries'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { CliToolStatus, CredentialStatus, WarningList } from './CredentialStatus'
import { Section } from './Section'
import { useSecretSubmit } from './useSecretSubmit'
import { ProjectCliSelection } from '@/features/project-credentials/AgentAccountSelection'
import { CredentialFields } from '@/features/project-credentials/CredentialFields'

const CLI_NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

const listClass =
  'grid min-w-0 divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-control'

export function ClisSection({ agent }: { agent: Schemas['Agent'] }) {
  const templates = useCliTemplates()
  const [adding, setAdding] = useState(false)

  const templateOf = (cli: string) => templates.data?.templates.find((tpl) => tpl.name === cli)
  // Credentials can't be entered until the templates have arrived; explain why.
  const unavailableReason = templates.data
    ? t.config.clis.credentialUnavailable.missing
    : templates.isError
      ? t.config.clis.credentialUnavailable.failed
      : t.config.clis.credentialUnavailable.loading

  return (
    <Section
      id="clis"
      title={t.config.clis.title}
      count={agent.clis.length}
      actions={
        <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
          <PlusIcon />
          {t.config.clis.add}
        </Button>
      }
    >
      {agent.clis.length === 0 && <p className="text-[13px] text-ink-500">{t.config.clis.empty}</p>}
      {templates.isError && !templates.data && (
        <div className="grid gap-1.5">
          <p className="text-[13px] font-bold text-red-text">{t.config.clis.templatesFailed}</p>
          <ErrorAlert error={templates.error} />
        </div>
      )}
      {agent.clis.length > 0 && (
        <ul className={listClass}>
          {agent.clis.map((cli) => (
            <CliItem
              key={cli.name}
              agentId={agent.id}
              configVersion={agent.configVersion}
              project={agent.scope === 'project'}
              cli={cli}
              template={templateOf(cli.name)}
              unavailableReason={unavailableReason}
            />
          ))}
        </ul>
      )}
      {adding && <AddCliDialog agentId={agent.id} onClose={() => setAdding(false)} />}
    </Section>
  )
}

function AddCliDialog({ agentId, onClose }: { agentId: string; onClose: () => void }) {
  const addCli = useAddCli(agentId)
  const [name, setName] = useState('')
  const [invalid, setInvalid] = useState<string | null>(null)
  const inputId = useId()

  const add = async (event: FormEvent) => {
    event.preventDefault()
    const cli = name.trim()
    if (!CLI_NAME.test(cli)) {
      setInvalid(t.config.clis.nameInvalid)
      return
    }
    setInvalid(null)
    if (await addCli.mutateAsync(cli).catch(() => null)) {
      setName('')
      onClose()
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !addCli.isPending) onClose() }}>
      <DialogContent kind="create" className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>{t.config.clis.add}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => void add(e)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor={inputId}>{t.config.clis.name}</Label>
            <Input
              id={inputId}
              placeholder={t.config.clis.name}
              className="font-mono text-[13.5px]"
              aria-invalid={invalid ? true : undefined}
              autoFocus
              spellCheck={false}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <FieldError message={invalid ?? undefined} />
          </div>
          <ErrorAlert error={addCli.error} />
          <DialogFooter>
            <Button type="button" variant="secondary" disabled={addCli.isPending} onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={addCli.isPending}>
              {t.config.clis.add}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// One-line account summary for a project CLI on its collapsed row (reads the shared credential-selections cache).
function ProjectCliSummary({ agentId, cli }: { agentId: string; cli: string }) {
  const isDb = cli === 'mysql' || cli === 'psql'
  const selections = useCredentialSelections(agentId)
  if (isDb) return <span className="truncate text-[12.5px] text-ink-500">{t.config.clis.configure}</span>
  const item = selections.data?.selections.find((entry) => entry.purpose === 'cli' && entry.cli === cli)
  if (!item) return <span className="truncate text-[12.5px] text-ink-500">{t.config.clis.configure}</span>
  const currentId = item.selectedAccountId ?? item.resolvedAccountId
  const account = currentId ? item.candidates.find((candidate) => candidate.id === currentId) : undefined
  if (account) return <span className="truncate text-[12.5px] font-medium text-ink-700">{account.name}</span>
  return <span className="truncate text-[12.5px] text-ink-500">{t.config.credentialStatus.unset}</span>
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
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [deletingCredential, setDeletingCredential] = useState(false)
  const remove = useRemoveCli(agentId)
  const deleteCredential = useDeleteCliCredential(agentId)
  const bodyId = useId()
  // Don't keep the open state while the template is unavailable, so the dialog doesn't pop up by itself when the template arrives later.
  if (editing && !template) setEditing(false)

  const platformStatus = cli.credential ? t.config.credentialStatus[cli.credential.status] : t.config.credentialStatus.unset

  if (!cli.hasTemplate) {
    return (
      <li className="flex min-h-12 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2" aria-label={cli.name}>
        <span className="font-mono text-[14px] font-semibold">{cli.name}</span>
        <span className="min-w-0 truncate text-[12.5px] text-ink-500">{t.config.clis.markerOnly}</span>
        <Button size="xs" variant="ghost" className="ml-auto shrink-0" onClick={() => setRemoving(true)}>
          {t.common.remove}
        </Button>
        <ConfirmDialog
          open={removing}
          onOpenChange={setRemoving}
          title={t.config.clis.removeTitle(cli.name)}
          body={project ? t.credentials.clis.removeProjectBody : t.config.clis.removeBody}
          confirmLabel={t.common.remove}
          destructive
          irreversible={false}
          onConfirm={() => remove.mutateAsync(cli.name)}
        />
      </li>
    )
  }

  // Template CLI: compact ~48px summary row that expands inline account/DB or credential controls on click (matching AgentPlugins).
  return (
    <li
      className={cn('min-w-0', open && 'shadow-[inset_2px_0_0_var(--ink)]')}
      aria-label={cli.name}
    >
      <div
        className={cn(
          'flex min-h-12 items-center gap-2 pr-3 pl-4 transition-colors',
          open
            ? 'bg-surface-subtle'
            : 'hover:bg-surface-hover',
        )}
      >
        <button
          type="button"
          aria-expanded={open}
          aria-controls={bodyId}
          onClick={() => setOpen((value) => !value)}
          className="flex min-h-12 min-w-0 flex-1 cursor-pointer flex-wrap items-center gap-x-2.5 gap-y-1 py-2 text-left outline-none focus-visible:shadow-focus"
        >
          <span className="font-mono text-[14px] font-semibold">{cli.name}</span>{' '}
          <Badge tone="info" className="text-riso-blue">
            {template ? t.config.clis.templateVersion(template.version) : t.config.clis.credential}
          </Badge>{' '}
          <span className="min-w-0 flex-1 truncate">
            {project ? (
              <ProjectCliSummary agentId={agentId} cli={cli.name} />
            ) : (
              <span
                className={cn(
                  'text-[12.5px] font-medium',
                  cli.credential?.status === 'set' ? 'text-green-text' : cli.credential ? 'text-red-text' : 'text-ink-500',
                )}
              >
                {platformStatus}
              </span>
            )}
          </span>
          <ChevronDownIcon aria-hidden="true" className={cn('size-4 shrink-0 text-ink-500 transition-transform motion-reduce:transition-none', open && 'rotate-180')} />
        </button>
        <Button size="xs" variant="ghost" className="shrink-0" onClick={() => setRemoving(true)}>
          {t.common.remove}
        </Button>
      </div>

      {open && (
        <div id={bodyId} className="grid min-w-0 gap-3 border-t border-hairline bg-surface-subtle/50 px-4 py-3.5">
          {!template && <p className="text-[12.5px] text-ink-500">{unavailableReason}</p>}
          {template && <WarningList warnings={template.warnings} />}
          {template && <CliToolStatus template={template} />}
          {project ? (
            <ProjectCliSelection agentId={agentId} configVersion={configVersion} cli={cli.name} embedded />
          ) : (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-[13px]">
              <span className="font-semibold">{t.config.clis.credential}</span>
              <CredentialStatus credential={cli.credential} />
              <div className="ml-auto flex flex-wrap gap-x-2 gap-y-1">
                <Button
                  size="xs"
                  variant={cli.credential ? 'secondary' : 'ink'}
                  className={cn(cli.credential && 'bg-surface-control hover:bg-surface-hover')}
                  disabled={!template}
                  onClick={() => setEditing(true)}
                >
                  {cli.credential ? t.config.clis.replaceCredential : t.config.clis.setCredential}
                </Button>
                {cli.credential && (
                  <Button size="xs" variant="ghost" onClick={() => setDeletingCredential(true)}>
                    {t.common.delete}
                  </Button>
                )}
              </div>
            </div>
          )}
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
        // Only a removal that also deletes an entered credential can't be undone; a CLI removed without a credential, or removed at
        // project level (the shared account is kept), can be added back.
        irreversible={!project && !!cli.credential}
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
    <Dialog open onOpenChange={(open) => { if (!open && !pending) onClose() }}>
      <DialogContent kind="edit" className="sm:max-w-[540px]">
        <DialogHeader>
          <DialogTitle>{t.config.clis.credentialTitle(cli.name)}</DialogTitle>
          {cli.credential && <DialogDescription>{t.config.clis.replaceHint}</DialogDescription>}
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => void save(e)} autoComplete="off" noValidate>
          <WarningList warnings={template.warnings} />
          {template.fields.map((field) => {
            const id = `cli-field-${field.name}`
            const message = missing.includes(field.name) ? t.common.required : fieldMessages(error, (f) => f === `fields.${field.name}`)
            if (field.multiline)
              return (
                <CredentialFields
                  key={field.name}
                  fields={[field]}
                  values={values}
                  errors={message ? { [field.name]: message } : {}}
                  disabled={pending}
                  onChange={(name, value) => setValues((previous) => ({ ...previous, [name]: value }))}
                />
              )
            return (
              <div key={field.name} className="grid gap-1.5">
                <Label htmlFor={id}>{localizedText(field.label)}</Label>
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
            <Button type="button" variant="secondary" disabled={pending} onClick={onClose}>
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
