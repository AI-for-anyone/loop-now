import { useId, useState, type FormEvent } from 'react'
import { CheckIcon, FolderOpenIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useUpdateAgent } from '@/api/queries'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { PathPicker } from '@/features/desktop/PathPicker'
import { PermissionModePicker } from '@/features/permissions/PermissionModePicker'
import { t } from '@/i18n'
import { fieldMessages } from '@/lib/errors'
import { useRuntimeLookup, type RuntimeDefinition } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { Section } from './Section'

const copy = t.config.work

function Saved({ show }: { show: boolean }) {
  if (!show) return null
  return (
    <span role="status" className="inline-flex items-center gap-1 text-[12.5px] font-black text-green-text">
      <CheckIcon aria-hidden="true" className="size-3.5" strokeWidth={3} />
      {t.permissions.saved} · {t.permissions.nextMessage}
    </span>
  )
}

// The folder the Agent works in: empty is the workspace Loop Now gives it. Saved with its own button (a path is typed or chosen in the
// system dialog); a runtime that cannot work in a folder of its own (Pi) says so instead.
function FolderField({ agent, runtime }: { agent: Schemas['Agent']; runtime: RuntimeDefinition | undefined }) {
  const update = useUpdateAgent(agent.id)
  const ids = { input: useId(), hint: useId() }
  const [path, setPath] = useState(agent.workingDirectory ?? '')
  const [saved, setSaved] = useState(false)
  // A change from elsewhere replaces the field only when nothing is typed in it.
  const [base, setBase] = useState(agent.workingDirectory ?? '')
  if ((agent.workingDirectory ?? '') !== base) {
    if (path.trim() === base) setPath(agent.workingDirectory ?? '')
    setBase(agent.workingDirectory ?? '')
  }
  const unsupported = runtime !== undefined && !runtime.capabilities.workingDirectory
  const changed = path.trim() !== (agent.workingDirectory ?? '')
  const save = (next: string | null) => {
    setSaved(false)
    update.mutate({ workingDirectory: next }, { onSuccess: () => { setSaved(true); setPath(next ?? '') } })
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (changed) save(path.trim() || null)
  }
  const error = fieldMessages(update.error, (field) => field === 'workingDirectory')
  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <Label htmlFor={ids.input} className="inline-flex items-center gap-1.5 text-[14.5px] font-black">
        <FolderOpenIcon aria-hidden="true" className="size-4 text-riso-blue" strokeWidth={2.6} />
        {copy.folder.label}
      </Label>
      <p id={ids.hint} className="text-[13px] text-ink-700">{copy.folder.hint}</p>
      <PathPicker kind="directory" value={path} onPick={setPath} disabled={unsupported || update.isPending}>
        <Input
          id={ids.input}
          value={path}
          onChange={(event) => setPath(event.target.value)}
          placeholder={copy.folder.placeholder}
          aria-describedby={ids.hint}
          aria-invalid={error ? true : undefined}
          disabled={unsupported}
          spellCheck={false}
          className="font-mono text-[13.5px]"
        />
      </PathPicker>
      {!agent.workingDirectory && !changed && <p className="text-[12.5px] text-ink-500">{copy.folder.empty}</p>}
      {unsupported && runtime && <p role="note" className="text-[12.5px] font-bold text-ink-700">{copy.folder.unsupported(runtime.name)}</p>}
      <FieldError message={error} />
      {!error && <ErrorAlert error={update.error} />}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button type="submit" size="sm" disabled={!changed || update.isPending}>
          {copy.folder.save}
        </Button>
        {agent.workingDirectory && (
          <Button type="button" size="sm" variant="ghost" disabled={update.isPending} onClick={() => save(null)}>
            {copy.folder.clear}
          </Button>
        )}
        <Saved show={saved && !changed} />
      </div>
      {path.trim() && <p className="text-[12.5px] text-ink-500">{copy.folder.piNote}</p>}
    </form>
  )
}

// “用我本机的 CLI 配置” (Use my own CLI configuration): it runs as it does in the terminal. Saved as soon as it is switched; what it turns on
// is said in plain words, for the runtime the Agent is on.
function LocalConfigField({ agent, runtime }: { agent: Schemas['Agent']; runtime: RuntimeDefinition | undefined }) {
  const update = useUpdateAgent(agent.id)
  const id = useId()
  const [saved, setSaved] = useState(false)
  const unsupported = runtime !== undefined && !runtime.capabilities.localConfig
  const items = (runtime && copy.localConfig.turnsOn[runtime.id]) ?? copy.localConfig.generic
  const on = agent.useLocalConfig
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-3">
        <Switch
          id={id}
          checked={on}
          disabled={unsupported || update.isPending}
          aria-describedby={`${id}-state`}
          onCheckedChange={(next) => {
            setSaved(false)
            update.mutate({ useLocalConfig: next }, { onSuccess: () => setSaved(true) })
          }}
        />
        <Label htmlFor={id} className="text-[14.5px] font-black">
          {copy.localConfig.label}
        </Label>
        <Saved show={saved} />
      </div>
      <div id={`${id}-state`} className={cn('flex flex-col gap-2 rounded-ticket px-4 py-3', on ? 'bg-tint-yellow shadow-[inset_0_0_0_2px_var(--ink)]' : 'bg-ink/[0.04]')}>
        {unsupported && runtime ? (
          <p className="text-[13px] font-bold text-ink-700">{copy.localConfig.unsupported(runtime.name)}</p>
        ) : on ? (
          <>
            <p className="text-[13.5px] font-black">{copy.localConfig.onIntro}</p>
            <ul className="flex flex-col gap-1 pl-1">
              {items.map((item) => (
                <li key={item} className="flex items-start gap-2 text-[13.5px]">
                  <CheckIcon aria-hidden="true" className="mt-1 size-3.5 shrink-0 text-green-text" strokeWidth={3.4} />
                  {item}
                </li>
              ))}
            </ul>
            <p className="text-[12.5px] text-ink-700">{copy.localConfig.alsoKept}</p>
            <p className="text-[12.5px] font-black text-pink-text">{copy.localConfig.risk}</p>
          </>
        ) : (
          <p className="text-[13px] text-ink-700">{copy.localConfig.off}</p>
        )}
      </div>
      <ErrorAlert error={update.error} />
    </div>
  )
}

function ModeField({ agent, runtime }: { agent: Schemas['Agent']; runtime: RuntimeDefinition | undefined }) {
  const update = useUpdateAgent(agent.id)
  const labelId = useId()
  const [saved, setSaved] = useState(false)
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h3 id={labelId} className="text-[14.5px] font-black">
          {t.permissions.title}
        </h3>
        <Saved show={saved} />
      </div>
      <PermissionModePicker
        value={agent.permissionMode}
        runtime={runtime}
        labelledBy={labelId}
        disabled={update.isPending}
        onChange={(mode) => {
          setSaved(false)
          update.mutate({ permissionMode: mode }, { onSuccess: () => setSaved(true) })
        }}
      />
      <ErrorAlert error={update.error} />
    </div>
  )
}

// “工作方式” (How it works): the Agent's own folder, whether it uses the person's CLI configuration (as the terminal), and how often it
// asks before acting. Each saves on its own and applies from the next message.
export function WorkSection({ agent }: { agent: Schemas['Agent'] }) {
  const runtime = useRuntimeLookup().get(agent.runtime)
  return (
    <Section id="work" title={copy.title}>
      <div className="flex flex-col gap-7">
        {agent.importedFrom && (
          <p className="flex flex-col gap-1 rounded-ticket bg-tint-blue px-4 py-2.5 text-[13px]">
            <span className="font-bold">{copy.importedFrom(t.importing.cli[agent.importedFrom.cli], t.chat.time(agent.importedFrom.importedAt))}</span>
            <span className="font-mono text-[12.5px] [overflow-wrap:anywhere] text-ink-700">{agent.importedFrom.path}</span>
          </p>
        )}
        <FolderField agent={agent} runtime={runtime} />
        <LocalConfigField agent={agent} runtime={runtime} />
        <ModeField agent={agent} runtime={runtime} />
      </div>
    </Section>
  )
}
