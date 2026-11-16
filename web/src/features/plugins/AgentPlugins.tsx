import { useId, useLayoutEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'
import { ApiError, type Schemas } from '@/api/client'
import { useAgentPlugins, usePlugin, usePluginAccounts, useSaveAgentPlugin } from '@/api/plugins'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Section } from '@/features/agent-config/Section'
import { useScope } from '@/features/projects/scope-context'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { projectBase } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { Quiet } from './parts'
import { PluginActionButtons } from './PluginActions'

type AgentPlugin = Schemas['AgentPlugin']
const text = t.plugins.agent

const listClass = 'grid min-w-0 divide-y divide-ink/10 overflow-hidden rounded-card bg-white @container'
// When wide, the name takes a fixed column and the summaries line up in one column; when narrow, the name and the expand marker are on
// top and the summary fills the next row. Row height stays within 48–52px either way.
const rowGrid = 'grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 px-4 py-1.5 text-left @min-[36rem]:h-12 @min-[36rem]:grid-cols-[11rem_minmax(0,1fr)_auto] @min-[36rem]:py-0'
const rowName = 'col-start-1 row-start-1 truncate text-[14.5px]/[20px] font-bold'
const rowSummary = 'col-span-2 col-start-1 row-start-2 flex min-w-0 items-center text-[13px]/[18px] @min-[36rem]:col-span-1 @min-[36rem]:col-start-2 @min-[36rem]:row-start-1'
const rowTrail = 'col-start-2 row-start-1 flex items-center gap-2 @min-[36rem]:col-start-3'

const usesAccounts = (agent: Schemas['Agent'], plugin: AgentPlugin) => agent.scope === 'project' && agent.projectId !== null && plugin.accountKinds.length > 0

// For a Claude Code Agent: when the plugin's Agent side is only Pi extensions (no Skills, MCP servers or CLI folder), mark that it has no
// effect on Claude Code.
// The components come from the manifest in the plugin details; if that can't be read, nothing is marked.
function PiOnlyHint({ pluginId }: { pluginId: string }) {
  const detail = usePlugin(pluginId)
  const parts = detail.data?.manifest.components
  if (!parts || parts.agentExtensions === 0 || parts.skills.length > 0 || parts.mcpServers.length > 0 || parts.bin.length > 0) return null
  return <span className="min-w-0 truncate font-bold text-pink-text">· {text.piOnly}</span>
}

// One-line summary: whether it is granted, and the names of the chosen accounts (once the account list has been read).
function Summary({ agent, plugin }: { agent: Schemas['Agent']; plugin: AgentPlugin }) {
  const accounts = usePluginAccounts(agent.projectId ?? '', plugin.pluginId, usesAccounts(agent, plugin) && plugin.accountIds.length > 0)
  const names = (accounts.data?.accounts ?? []).filter((account) => plugin.accountIds.includes(account.id)).map((account) => account.name)
  return (
    <span className={cn(rowSummary, 'gap-1.5 text-ink-700')}>
      {plugin.granted ? <span className="shrink-0 font-bold text-ink">{text.granted}</span> : <span className="shrink-0 text-ink-500">{text.notGranted}</span>}
      {plugin.granted && names.length > 0 && <span className="min-w-0 truncate">· {text.accountNames(t.plugins.confirm.list(names))}</span>}
      {agent.runtime === 'claude-code' && <PiOnlyHint pluginId={plugin.pluginId} />}
    </span>
  )
}

// The grant form shown when expanded: a grant checkbox, and for a project Agent, account choices per account kind (a kind that doesn't
// support multiple accounts allows only one).
function GrantForm({ agent, plugin, onSaved, onCancel, onDirty }: { agent: Schemas['Agent']; plugin: AgentPlugin; onSaved: () => void; onCancel: () => void; onDirty: (dirty: boolean) => void }) {
  const id = useId()
  const save = useSaveAgentPlugin(agent.id, plugin.pluginId)
  const accounts = usePluginAccounts(agent.projectId ?? '', plugin.pluginId, usesAccounts(agent, plugin))
  const [granted, setGranted] = useState(plugin.granted)
  const [accountIds, setAccountIds] = useState(plugin.accountIds)
  const dirty = granted !== plugin.granted || JSON.stringify([...accountIds].sort()) !== JSON.stringify([...plugin.accountIds].sort())
  useDirtyForm(`agent-plugin:${agent.id}:${plugin.pluginId}`, dirty)
  useLayoutEffect(() => {
    onDirty(dirty)
    return () => onDirty(false)
  }, [onDirty, dirty])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (dirty) save.mutate({ granted, accountIds }, { onSuccess: onSaved })
  }
  const toggle = (kind: Schemas['PluginAccountKind'], accountId: string, on: boolean) =>
    setAccountIds((current) => {
      const ofKind = new Set((accounts.data?.accounts ?? []).filter((account) => account.kind === kind.kind).map((account) => account.id))
      const rest = kind.multiple ? current : current.filter((value) => !ofKind.has(value))
      return on ? [...rest.filter((value) => value !== accountId), accountId] : rest.filter((value) => value !== accountId)
    })
  const notEnabled = save.error instanceof ApiError && save.error.code === 'plugin_not_enabled'
  return (
    <form className="grid min-w-0 gap-3.5" onSubmit={submit}>
      <label className="inline-flex items-center gap-2 text-[14px] font-bold">
        <input type="checkbox" checked={granted} disabled={save.isPending} onChange={(event) => setGranted(event.target.checked)} className="size-4 accent-riso-blue" />
        {text.grant(localizedText(plugin.displayName))}
      </label>
      {usesAccounts(agent, plugin) &&
        plugin.accountKinds.map((kind) => {
          const mine = (accounts.data?.accounts ?? []).filter((account) => account.kind === kind.kind)
          return (
            <fieldset key={kind.kind} className="grid min-w-0 gap-1.5" disabled={!granted || save.isPending}>
              <legend className="mb-1 text-[13px] font-black text-ink-700">{text.accounts(localizedText(kind.label))}</legend>
              <ErrorAlert error={accounts.error} />
              {accounts.isSuccess && mine.length === 0 && <Quiet>{text.noAccounts}</Quiet>}
              <div className="flex flex-wrap gap-2">
                {mine.map((account) => (
                  <label key={account.id} className="inline-flex min-h-9 items-center gap-2 rounded-full border-[1.5px] border-ink/30 bg-white px-3.5 text-[13.5px] font-bold has-[:checked]:border-riso-blue has-[:checked]:bg-tint-blue has-[:disabled]:opacity-50">
                    <input
                      type={kind.multiple ? 'checkbox' : 'radio'}
                      name={`${id}-${kind.kind}`}
                      checked={accountIds.includes(account.id)}
                      onChange={(event) => toggle(kind, account.id, event.target.checked)}
                      className="accent-riso-blue"
                    />
                    {account.name}
                  </label>
                ))}
              </div>
            </fieldset>
          )
        })}
      {notEnabled ? <p role="status" className="rounded-input bg-tint-yellow px-3 py-2 text-[13px] font-bold">{text.notEnabledHint}</p> : <ErrorAlert error={save.error} />}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" size="sm" disabled={!dirty || save.isPending}>
          {save.isPending ? t.plugins.form.saving : text.save}
        </Button>
        <Button type="button" size="sm" variant="secondary" disabled={save.isPending} onClick={onCancel}>
          {text.cancel}
        </Button>
      </div>
    </form>
  )
}

// The plugin's action buttons for Agents (those whose where in the manifest includes agent); the buttons act on this Agent.
function AgentActions({ agent, plugin }: { agent: Schemas['Agent']; plugin: AgentPlugin }) {
  const detail = usePlugin(plugin.pluginId)
  if (!detail.data) return null
  return <PluginActionButtons plugin={{ id: plugin.pluginId, displayName: localizedText(plugin.displayName) }} actions={detail.data.manifest.components.actions} page="agent" where={{ agentId: agent.id }} className="border-t border-ink/10 pt-3" />
}

// Only one plugin is expanded at a time; if the open form has unsaved changes, collapsing it or switching to another plugin asks for
// confirmation first.
function GrantList({ agent, plugins }: { agent: Schemas['Agent']; plugins: AgentPlugin[] }) {
  const [open, setOpen] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [asking, setAsking] = useState<{ next: string | null } | null>(null)
  const [saved, setSaved] = useState<string | null>(null)
  const show = (next: string | null) => {
    setOpen(next)
    if (next) setSaved(null)
  }
  const request = (next: string | null) => (dirty ? setAsking({ next }) : show(next))
  const current = plugins.find((plugin) => plugin.pluginId === open)
  return (
    <div className="relative min-w-0">
      <ul className={listClass}>
        {plugins.map((plugin) => (
          <Row
            key={plugin.pluginId}
            agent={agent}
            plugin={plugin}
            open={open === plugin.pluginId}
            saved={saved === plugin.pluginId}
            onToggle={() => request(open === plugin.pluginId ? null : plugin.pluginId)}
            onDone={(didSave) => {
              setDirty(false)
              if (didSave) setSaved(plugin.pluginId)
              show(null)
            }}
            onDirty={setDirty}
          />
        ))}
      </ul>
      <p role="status" className="sr-only">
        {saved ? text.saved : ''}
      </p>
      <ConfirmDialog
        open={asking !== null}
        onOpenChange={(value) => !value && setAsking(null)}
        title={text.discardTitle}
        body={text.discardBody(current ? localizedText(current.displayName) : '')}
        confirmLabel={text.discard}
        cancelLabel={text.keepEditing}
        onConfirm={() => {
          setDirty(false)
          show(asking?.next ?? null)
        }}
      />
    </div>
  )
}

function Row({ agent, plugin, open, saved, onToggle, onDone, onDirty }: { agent: Schemas['Agent']; plugin: AgentPlugin; open: boolean; saved: boolean; onToggle: () => void; onDone: (saved: boolean) => void; onDirty: (dirty: boolean) => void }) {
  const id = useId()
  const header = useRef<HTMLButtonElement>(null)
  // After the form collapses, focus returns to this row instead of falling to the top of the page.
  const done = (didSave: boolean) => {
    header.current?.focus()
    onDone(didSave)
  }
  return (
    <li className={cn('min-w-0', open && 'shadow-[inset_3px_0_0_var(--riso-blue)]')}>
      <h3>
        <button
          ref={header}
          type="button"
          aria-expanded={open}
          aria-controls={`${id}-body`}
          onClick={onToggle}
          className={cn(rowGrid, 'cursor-pointer outline-none hover:bg-ink/[0.03] focus-visible:shadow-[inset_0_0_0_2px_var(--riso-blue)]', open && 'bg-tint-blue/40 hover:bg-tint-blue/40')}
        >
          <span id={`${id}-name`} className={rowName} title={localizedText(plugin.displayName)}>
            {localizedText(plugin.displayName)}
          </span>
          <Summary agent={agent} plugin={plugin} />
          <span className={rowTrail}>
            {saved && <Badge tone="success">{text.saved}</Badge>}
            <ChevronDownIcon aria-hidden="true" className={cn('size-4 text-ink-500 transition-transform motion-reduce:transition-none', open && 'rotate-180')} />
          </span>
        </button>
      </h3>
      {open && (
        <div id={`${id}-body`} role="region" aria-labelledby={`${id}-name`} className="grid min-w-0 gap-3 border-t border-ink/10 px-4 pt-3.5 pb-4">
          <GrantForm agent={agent} plugin={plugin} onSaved={() => done(true)} onCancel={() => done(false)} onDirty={onDirty} />
          <AgentActions agent={agent} plugin={plugin} />
        </div>
      )}
    </li>
  )
}

// Plugins the project hasn't enabled yet are tucked away at the end: they only show whether they are currently granted, which can't be
// changed here, plus a link to the project's plugin page.
function NotEnabled({ plugins, link }: { plugins: AgentPlugin[]; link: string | null }) {
  const [open, setOpen] = useState(false)
  const id = useId()
  return (
    <div className="grid min-w-0 gap-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen((value) => !value)} className="-ml-1 inline-flex cursor-pointer items-center gap-1 rounded-input px-1 py-0.5 text-[14px] font-bold text-ink-700 outline-none focus-visible:shadow-focus">
          <ChevronRightIcon aria-hidden="true" className={cn('size-4 transition-transform motion-reduce:transition-none', open && 'rotate-90')} />
          {text.notEnabledGroup(plugins.length)}
        </button>
        {link && (
          <Link className="ml-auto text-[13px] font-bold text-riso-blue underline" to={link}>
            {text.projectPlugins}
          </Link>
        )}
      </div>
      {open && (
        <div id={id} className="grid min-w-0 gap-2">
          <p className="text-[13px] text-ink-500">{text.notEnabledHint}</p>
          <ul className={listClass}>
            {plugins.map((plugin) => (
              <li key={plugin.pluginId} aria-label={localizedText(plugin.displayName)} className={rowGrid}>
                <span className={cn(rowName, 'text-ink-700')} title={localizedText(plugin.displayName)}>
                  {localizedText(plugin.displayName)}
                </span>
                <span className={cn(rowSummary, 'text-ink-500')}>{plugin.granted ? text.granted : text.notGranted}</span>
                <span className={rowTrail}>
                  <Badge tone="neutral" className="bg-tint-yellow">
                    {text.notEnabled}
                  </Badge>
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// The plugin section of the Agent config page: grant per plugin, and a project Agent also chooses accounts; grants take effect the next
// time it starts (Pi or Claude Code).
export function AgentPlugins({ agent }: { agent: Schemas['Agent'] }) {
  const scope = useScope()
  const query = useAgentPlugins(agent.id)
  const plugins = query.data?.plugins ?? []
  const available = plugins.filter((plugin) => plugin.available)
  const unavailable = plugins.filter((plugin) => !plugin.available)
  const link = scope.kind === 'project' && scope.project.id === agent.projectId ? `${projectBase(scope.project.name)}/plugins` : null
  return (
    <Section id="plugins" title={t.plugins.agentTitle}>
      <p className="text-[13.5px] text-ink-700">{agent.runtime === 'claude-code' ? text.introClaudeCode : text.intro}</p>
      <ErrorAlert error={query.error} />
      {query.isPending && <Quiet>{t.plugins.loading}</Quiet>}
      {query.isSuccess && plugins.length === 0 && <Quiet>{text.empty}</Quiet>}
      {available.length > 0 && <GrantList key={agent.id} agent={agent} plugins={available} />}
      {unavailable.length > 0 && <NotEnabled plugins={unavailable} link={link} />}
    </Section>
  )
}
