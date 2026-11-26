import { useId, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { KeyRoundIcon, PlusIcon, ShieldAlertIcon, XIcon } from 'lucide-react'
import { ApiError, type Schemas } from '@/api/client'
import { useAcpCommands, useCreateAcpCommand, useDeleteAcpCommand, useUpdateAcpCommand } from '@/api/acp-commands'
import { useProjects, useRuntimes } from '@/api/queries'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { PathPicker } from '@/features/desktop/PathPicker'
import { t } from '@/i18n'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { useRuntimeLookup } from '@/lib/runtime'
import { agentPath } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { createBody, draftOf, formProblems, hasProblems, newEnvRow, updateBody, type AcpCommandDraft, type EnvRow, type FormProblems } from './acp-command-form'

type Command = Schemas['AcpCommand']
const text = t.runtime.commands

// The local ACP commands people added, under the runtimes in global settings: each is a runtime of its own (its name in the runner picker),
// run on this computer as the server's user. Listed with whether its executable is found (GET /runtimes); added, changed and deleted here.
export function AcpCommandsSection() {
  const commands = useAcpCommands()
  const [editing, setEditing] = useState<Command | 'new' | null>(null)
  const [deleting, setDeleting] = useState<Command | null>(null)
  const list = commands.data?.commands
  return (
    <div className="grid gap-3.5 border-t-2 border-dashed border-ink/30 pt-4">
      <div className="flex flex-wrap items-end gap-x-4 gap-y-2.5">
        <div className="grid min-w-0 flex-[1_1_320px] gap-1">
          <h3 className="font-display-cn text-[22px] leading-[1.2] text-ink">{text.title}</h3>
          <p className="text-[13.5px] text-ink-700">{text.intro}</p>
        </div>
        <Button size="sm" className="h-[34px] rounded-full px-4 text-[13.5px] font-black" onClick={() => setEditing('new')}>
          <PlusIcon aria-hidden="true" className="size-4" strokeWidth={3} />
          {text.add}
        </Button>
      </div>
      <ErrorAlert error={commands.error} />
      {commands.isPending && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
      {list?.length === 0 && (
        <p className="rounded-card border-2 border-dashed border-ink/35 px-[18px] py-4 text-[13.5px] font-bold text-ink-500">{text.empty}</p>
      )}
      {list?.map((command) => <AcpCommandCard key={command.id} command={command} onEdit={() => setEditing(command)} onDelete={() => setDeleting(command)} />)}
      {editing && <AcpCommandDialog command={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
      <DeleteAcpCommandDialog command={deleting} onClose={() => setDeleting(null)} />
    </div>
  )
}

function AcpCommandCard({ command, onEdit, onDelete }: { command: Command; onEdit: () => void; onDelete: () => void }) {
  const lookup = useRuntimeLookup()
  const definition = lookup.get(command.runtime)
  const status = useRuntimes().data?.runtimes.find((runtime) => runtime.id === command.runtime)
  return (
    <section aria-label={command.name} className="flex min-w-0 flex-col gap-3 rounded-card border border-hairline-strong bg-surface-control px-[18px] py-3.5 shadow-surface [[data-skin=plate]_&]:mr-[5px] [[data-skin=plate]_&]:mb-[5px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[5px_5px_0_var(--ink)]">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {definition && <RuntimeMark runtime={definition} className="size-9" />}
        <span className="min-w-0 font-display-cn text-[26px] leading-[1.1] break-words">{command.name}</span>
        {status && <Badge tone={status.available ? 'success' : 'danger'}>{status.available ? text.found : text.notFound}</Badge>}
        <span className="ml-auto flex gap-2">
          <Button size="sm" variant="secondary" aria-label={`${text.edit} ${command.name}`} onClick={onEdit}>
            {text.edit}
          </Button>
          <Button size="sm" variant="danger-outline" aria-label={`${text.remove} ${command.name}`} onClick={onDelete}>
            {text.remove}
          </Button>
        </span>
      </div>
      <dl className="grid grid-cols-[minmax(64px,auto)_minmax(0,1fr)] items-baseline gap-x-3 gap-y-2 text-[13.5px]">
        <dt className="font-black text-ink-500">{text.command}</dt>
        <dd className="min-w-0">
          {/* The command as the program gets it: the executable, then each argument on a ticket of its own (no shell joins them). */}
          <span className="flex min-w-0 flex-wrap items-center gap-1.5 font-mono text-[12.5px]">
            <span className="font-bold break-all text-ink">{command.command}</span>
            {command.args.map((arg, index) => (
              <span key={index} className="rounded-[5px] bg-tint-blue px-1.5 py-px break-all whitespace-pre-wrap text-ink shadow-[inset_0_0_0_1.5px_rgb(24_38_94/.35)]">
                {arg}
              </span>
            ))}
            {command.args.length === 0 && <span className="font-sans text-[12.5px] text-ink-500">{text.noArgs}</span>}
          </span>
        </dd>
        {command.envNames.length > 0 && (
          <>
            <dt className="font-black text-ink-500">{text.env}</dt>
            <dd className="flex min-w-0 flex-wrap gap-1.5">
              {command.envNames.map((name) => (
                <span key={name} className="inline-flex items-center gap-1 rounded-full bg-tint-yellow px-2.5 py-0.5 font-mono text-[12px] text-ink">
                  <KeyRoundIcon aria-hidden="true" className="size-3" strokeWidth={2.6} />
                  {name}
                  <span className="font-sans text-[11.5px] font-bold text-ink-500">{text.saved}</span>
                </span>
              ))}
            </dd>
          </>
        )}
        {status?.binaryPath && (
          <>
            <dt className="font-black text-ink-500">{text.path}</dt>
            <dd className="min-w-0 font-mono text-[12.5px] break-all">{status.binaryPath}</dd>
          </>
        )}
      </dl>
      {status && !status.available ? (
        <Alert variant="warning" role="note">
          <AlertDescription className="text-[13.5px] text-ink">{text.notFoundNote(command.command)}</AlertDescription>
        </Alert>
      ) : (
        <p className="text-[12.5px] text-ink-700">{text.unknownState}</p>
      )}
      {status && <p className="text-[12px] text-ink-500">{text.checkedAt(t.chat.time(status.checkedAt))}</p>}
    </section>
  )
}

const fieldsShown = (field: string) => field === 'name' || field === 'command' || field === 'args' || field === 'env' || field.startsWith('env.')

// Add a command, or change one. The warning comes first: whatever is entered runs on this computer as the server's user.
function AcpCommandDialog({ command, onClose }: { command?: Command; onClose: () => void }) {
  const create = useCreateAcpCommand()
  const update = useUpdateAcpCommand()
  const saving = create.isPending || update.isPending
  const error = command ? update.error : create.error
  const [draft, setDraft] = useState<AcpCommandDraft>(() => draftOf(command))
  const [problems, setProblems] = useState<FormProblems | null>(null)
  const ids = useId()
  const copy = text.dialog
  const set = (change: Partial<AcpCommandDraft>) => setDraft((current) => ({ ...current, ...change }))
  const setRow = (key: string, change: Partial<EnvRow>) => set({ env: draft.env.map((row) => (row.key === key ? { ...row, ...change } : row)) })

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const found = formProblems(draft)
    setProblems(found)
    if (hasProblems(found)) return
    if (command) {
      const body = updateBody(draft, command)
      if (Object.keys(body).length > 0 && !(await update.mutateAsync({ id: command.id, body }).catch(() => null))) return
    } else if (!(await create.mutateAsync(createBody(draft)).catch(() => null))) return
    onClose()
  }

  const shown = problems ? formProblems(draft) : null
  const nameError = shown?.name ? copy.required.name : fieldMessages(error, (field) => field === 'name')
  const commandError = shown?.command ? copy.required.command : fieldMessages(error, (field) => field === 'command')
  const argsError = fieldMessages(error, (field) => field === 'args')
  const envError = fieldMessages(error, (field) => field === 'env' || field.startsWith('env.'))
  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent kind={command ? 'edit' : 'create'} className="sm:max-w-[600px]">
        <DialogHeader>
          <DialogTitle>{command ? copy.editTitle(command.name) : copy.addTitle}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)} noValidate>
          <Alert variant="warning" role="note" className="flex items-start gap-2.5">
            <ShieldAlertIcon aria-hidden="true" className="mt-0.5 size-5 shrink-0" strokeWidth={2.4} />
            <AlertDescription className="text-[13.5px] leading-[1.65] font-black text-ink">{copy.trust}</AlertDescription>
          </Alert>
          <Field id={`${ids}-name`} label={copy.name} hint={copy.nameHint} error={nameError}>
            <Input id={`${ids}-name`} value={draft.name} autoComplete="off" readOnly={saving} aria-invalid={nameError ? true : undefined} aria-describedby={`${ids}-name-hint`} onChange={(event) => set({ name: event.target.value })} />
          </Field>
          <Field id={`${ids}-command`} label={copy.command} hint={copy.commandHint} error={commandError}>
            <PathPicker kind="file" value={draft.command} onPick={(path) => set({ command: path })} disabled={saving}>
              <Input id={`${ids}-command`} value={draft.command} autoComplete="off" spellCheck={false} readOnly={saving} className="font-mono text-[14px]" placeholder="hermes" aria-invalid={commandError ? true : undefined} aria-describedby={`${ids}-command-hint`} onChange={(event) => set({ command: event.target.value })} />
            </PathPicker>
          </Field>
          <Field id={`${ids}-args`} label={copy.args} hint={copy.argsHint} error={argsError}>
            <Textarea id={`${ids}-args`} value={draft.args} rows={3} spellCheck={false} readOnly={saving} className="min-h-[84px] font-mono text-[13.5px] leading-[1.6] whitespace-pre" placeholder={'acp\n--stdio'} aria-invalid={argsError ? true : undefined} aria-describedby={`${ids}-args-hint`} onChange={(event) => set({ args: event.target.value })} />
          </Field>
          <fieldset className="grid min-w-0 gap-2" aria-describedby={`${ids}-env-hint`}>
            <legend className="mb-1.5 text-[13.5px] leading-snug font-black text-ink">{copy.env}</legend>
            {draft.env.map((row) => (
              <EnvRowField key={row.key} row={row} problem={shown?.env[row.key]} disabled={saving} onChange={(change) => setRow(row.key, change)} onRemove={() => set({ env: draft.env.filter((other) => other.key !== row.key) })} />
            ))}
            <Button type="button" variant="ghost" size="sm" className="justify-self-start px-1" disabled={saving} onClick={() => set({ env: [...draft.env, newEnvRow()] })}>
              <PlusIcon aria-hidden="true" className="size-4" strokeWidth={3} />
              {copy.addEnv}
            </Button>
            <FieldError message={envError} />
            <p id={`${ids}-env-hint`} className="text-[12.5px] leading-[1.65] text-ink-500">
              {copy.envHint}
            </p>
          </fieldset>
          <ErrorAlert error={unmatchedFieldError(error, fieldsShown)} />
          <DialogFooter>
            <Button type="button" variant="secondary" disabled={saving} onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? copy.saving : command ? copy.submitEdit : copy.submitAdd}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Field({ id, label, hint, error, children }: { id: string; label: string; hint: string; error?: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      <FieldError message={error} />
      <p id={`${id}-hint`} className="text-[12.5px] leading-[1.65] text-ink-500">
        {hint}
      </p>
    </div>
  )
}

// One variable: a new one is a name and a value; a saved one shows its name and "saved", and can be refilled (only then is a value sent)
// or removed.
function EnvRowField({ row, problem, disabled, onChange, onRemove }: { row: EnvRow; problem?: 'invalid' | 'taken' | 'value'; disabled: boolean; onChange: (change: Partial<EnvRow>) => void; onRemove: () => void }) {
  const copy = text.dialog
  const valueInput = (
    <Input
      type="password"
      autoComplete="new-password"
      data-1p-ignore
      value={row.value}
      readOnly={disabled}
      aria-label={copy.envValue(row.name.trim())}
      aria-invalid={problem === 'value' ? true : undefined}
      placeholder={copy.envValue('')}
      className="h-10 font-mono text-[13.5px]"
      onChange={(event) => onChange({ value: event.target.value })}
    />
  )
  return (
    <div className="grid gap-1">
      {/* Name, value, remove in one row; on a narrow screen the value goes under the name, at full width. */}
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto]">
        {row.saved ? (
          <span className="flex h-10 min-w-0 items-center gap-1.5 rounded-input bg-tint-yellow px-3 font-mono text-[13.5px] font-bold text-ink">
            <KeyRoundIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.6} />
            <span className="truncate">{row.name}</span>
          </span>
        ) : (
          <Input
            value={row.name}
            autoComplete="off"
            spellCheck={false}
            readOnly={disabled}
            aria-label={copy.envName}
            aria-invalid={problem === 'invalid' || problem === 'taken' ? true : undefined}
            placeholder="API_KEY"
            className="h-10 font-mono text-[13.5px]"
            onChange={(event) => onChange({ name: event.target.value })}
          />
        )}
        <div className="order-last col-span-full min-w-0 sm:order-none sm:col-span-1">
        {row.saved && !row.refill ? (
          <span className="flex h-10 min-w-0 items-center justify-between gap-2 rounded-input px-3 text-[13px] text-ink-500 shadow-[inset_0_0_0_2px_rgb(24_38_94/.2)]">
            <span className="truncate font-bold">{copy.savedValue}</span>
            <Button type="button" variant="ghost" size="sm" className="h-7 shrink-0 px-1 text-[13px]" disabled={disabled} aria-label={`${copy.refill} ${row.name}`} onClick={() => onChange({ refill: true })}>
              {copy.refill}
            </Button>
          </span>
        ) : row.saved ? (
          <span className="flex min-w-0 items-center gap-1.5">
            {valueInput}
            <Button type="button" variant="ghost" size="sm" className="h-7 shrink-0 px-1 text-[13px]" disabled={disabled} aria-label={`${copy.keep} ${row.name}`} onClick={() => onChange({ refill: false, value: '' })}>
              {copy.keep}
            </Button>
          </span>
        ) : (
          valueInput
        )}
        </div>
        <Button type="button" variant="secondary" size="icon-sm" className="rounded-full" disabled={disabled} aria-label={copy.removeEnv(row.name.trim())} title={copy.removeEnv(row.name.trim())} onClick={onRemove}>
          <XIcon aria-hidden="true" className="size-4" strokeWidth={3} />
        </Button>
      </div>
      {problem && <p role="alert" className={cn('text-[12.5px] leading-snug font-bold text-red-text')}>{copy.envProblems[problem]}</p>}
    </div>
  )
}

// Deleting a command. While Agents still run on it the server refuses (409 runtime_in_use, naming them): the dialog stays open and lists
// them, each linked to its config page, where "Runs on" can be changed.
function DeleteAcpCommandDialog({ command, onClose }: { command: Command | null; onClose: () => void }) {
  const remove = useDeleteAcpCommand()
  const copy = text.deleteDialog
  return (
    <ConfirmDialog
      open={command !== null}
      onOpenChange={(open) => !open && onClose()}
      title={command ? copy.title(command.name) : ''}
      body={copy.body}
      confirmLabel={copy.confirm}
      destructive
      onConfirm={() => (command ? remove.mutateAsync(command.id) : undefined)}
      failureView={(error) => (command && error instanceof ApiError && error.code === 'runtime_in_use' ? <InUse name={command.name} error={error} /> : undefined)}
    />
  )
}

function InUse({ name, error }: { name: string; error: ApiError }) {
  const projects = useProjects().data?.projects
  const details = error.details as Schemas['RuntimeInUseDetails'] | undefined
  const agents = details?.agents ?? []
  const copy = text.deleteDialog
  return (
    <Alert variant="error" role="alert">
      <AlertDescription className="grid gap-2 text-[13.5px] leading-[1.7]">
        <span>{copy.inUse(name, agents.length)}</span>
        {agents.length > 0 && (
          <ul className="grid gap-1">
            {agents.map((agent) => {
              const project = agent.projectId === null ? null : projects?.find((candidate) => candidate.id === agent.projectId)
              const where = agent.projectId === null ? copy.platform : project?.name
              const to = agent.projectId === null ? agentPath({ kind: 'platform' }, agent.id, 'config') : project ? agentPath({ kind: 'project', project }, agent.id, 'config') : null
              return (
                <li key={agent.id} className="flex flex-wrap items-baseline gap-x-2">
                  {to ? (
                    <Link to={to} className="font-black text-ink underline decoration-red decoration-2 underline-offset-4">
                      {agent.name}
                    </Link>
                  ) : (
                    <span className="font-black text-ink">{agent.name}</span>
                  )}
                  {where && <span className="text-[12.5px] text-ink-700">{where}</span>}
                </li>
              )
            })}
          </ul>
        )}
      </AlertDescription>
    </Alert>
  )
}
