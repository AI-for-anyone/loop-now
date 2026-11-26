import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowRightIcon, CheckIcon, ChevronRightIcon, FolderIcon, GitBranchIcon, LoaderCircleIcon, MinusIcon, RotateCwIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { startConversationImport, startConversationScan, useConversationImport, useConversationScan } from '@/api/conversation-import'
import { keys, useProjects } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { StickyNote } from '@/components/StickyNote'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { markFirstRunImportShown, skipFirstRunImport } from './first-run'
import { counts, fits, folderCheck, groupFolders, importable, importBody, initialSelection, pickable, withConversation, withFolder, type FolderGroup, type Selection } from './selection'

type Folder = Schemas['ImportFolder']
type Conversation = Schemas['ImportConversation']

const copy = t.importing
const DAY_MS = 24 * 60 * 60 * 1000

// A riso check box: a square with a hard navy offset, yellow when checked, a dash when part of a folder is picked.
function Check({ checked, onChange, disabled, label, describedBy }: { checked: boolean | 'indeterminate'; onChange: (checked: boolean) => void; disabled?: boolean; label: string; describedBy?: string }) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = checked === 'indeterminate'
  }, [checked])
  return (
    <span className="relative inline-grid size-[22px] shrink-0 place-items-center">
      <input
        ref={ref}
        type="checkbox"
        aria-label={label}
        aria-describedby={describedBy}
        checked={checked === true}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="peer absolute inset-0 m-0 cursor-pointer appearance-none rounded-[6px] border-2 border-ink bg-paper-raised shadow-[2px_2px_0_var(--ink)] outline-none checked:bg-riso-yellow indeterminate:bg-riso-yellow focus-visible:shadow-focus disabled:cursor-not-allowed disabled:border-ink/25 disabled:bg-ink/[0.04] disabled:shadow-none"
      />
      <CheckIcon aria-hidden="true" className="pointer-events-none relative hidden size-[15px] text-ink peer-checked:block" strokeWidth={3.6} />
      <MinusIcon aria-hidden="true" className="pointer-events-none relative hidden size-[15px] text-ink peer-indeterminate:block" strokeWidth={3.6} />
    </span>
  )
}

// How far a long operation has got: a halftone bar that fills in riso blue, with the count beside it.
function Progress({ label, done, total, children }: { label: string; done: number; total: number; children?: ReactNode }) {
  const share = total > 0 ? Math.min(1, done / total) : 0
  return (
    // The label is announced once; the count is a progress bar a screen reader reads on demand, not every time it moves.
    <div className="flex flex-col gap-3 rounded-card bg-paper-raised px-5 py-4 shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--riso-blue)]">
      <p role="status" className="flex items-center gap-2 text-[15px] font-black">
        <LoaderCircleIcon aria-hidden="true" className="riso-spin size-4 text-pink-text motion-reduce:animate-none" strokeWidth={3} />
        {label}
      </p>
      <span role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} className="riso-skeleton relative block h-3.5 overflow-hidden rounded-full shadow-[inset_0_0_0_2px_var(--ink)]">
        <span className="riso-halftone absolute inset-y-0 left-0 rounded-full bg-riso-blue transition-[width] motion-reduce:transition-none" style={{ width: `${Math.max(share * 100, total > 0 ? 4 : 0)}%` }} />
      </span>
      {children && <p className="text-[12.5px] font-bold text-ink-700">{children}</p>}
    </div>
  )
}

// full: one more would not fit in this folder's import (100 conversations, 4 GB).
function ConversationRow({ folder, conversation, picked, full, onPick }: { folder: Folder; conversation: Conversation; picked: boolean; full: boolean; onPick: (checked: boolean) => void }) {
  const c = copy.conversation
  const metaId = useId()
  const done = conversation.agentId !== null
  const blocked = conversation.skipReason !== null
  const title = conversation.title.trim() || c.untitled
  return (
    <li data-conversation={conversation.sessionId} data-picked={picked || undefined} className={cn('grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-1 rounded-[10px] py-2.5 pr-1 pl-2 transition-colors motion-reduce:transition-none', picked && 'bg-tint-yellow', (done || blocked) && 'opacity-80')}>
      <span className="pt-0.5">
        <Check checked={picked} disabled={done || blocked || (!picked && full)} onChange={onPick} label={title} describedBy={metaId} />
      </span>
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 text-[14px] leading-snug font-black [overflow-wrap:anywhere]">{title}</span>
          <span className="shrink-0 rounded-stamp bg-ink px-1.5 text-[11px] leading-[18px] font-black text-white">{copy.cli[conversation.cli]}</span>
          {done && (
            <>
              <span className="riso-tilt shrink-0 rounded-stamp px-1.5 text-[11.5px] leading-[18px] font-black text-green-text [--tilt-bg:var(--tint-green)] [--tilt-shadow:inset_0_0_0_1.5px_currentColor] [--tilt:-2deg]">{c.imported}</span>
              <Link to={`/agents/${conversation.agentId}/chat`} className="inline-flex shrink-0 items-center gap-0.5 text-[12.5px] font-black text-riso-blue underline decoration-[1.5px] underline-offset-[3px]">
                {c.openAgent}
                <ArrowRightIcon aria-hidden="true" className="size-3.5" strokeWidth={2.8} />
              </Link>
            </>
          )}
        </p>
        {conversation.firstPrompt && conversation.firstPrompt !== conversation.title && <p className="line-clamp-1 text-[12.5px] text-ink-700 [overflow-wrap:anywhere]">{conversation.firstPrompt}</p>}
        <p id={metaId} className="flex flex-wrap gap-x-1.5 text-[12px] text-ink-500">
          <span>{c.lastActive(conversation.lastActiveAt)}</span>
          <span aria-hidden="true">·</span>
          <span>{c.messages(conversation.messageCount)}</span>
          <span aria-hidden="true">·</span>
          <span>{c.size(conversation.sizeBytes)}</span>
          {conversation.model && (
            <>
              <span aria-hidden="true">·</span>
              <span className="font-mono">{conversation.model}</span>
            </>
          )}
          {blocked && <span className="basis-full font-bold text-red-text">{c.skipped(copy.skipReasons[conversation.skipReason!])}</span>}
          {conversation.cwd !== folder.path && <span className="basis-full font-mono [overflow-wrap:anywhere]">{c.inFolder(conversation.cwd)}</span>}
        </p>
      </div>
    </li>
  )
}

function FolderRow({ folder, selection, onChange, projects, open, onToggle }: { folder: Folder; selection: Selection; onChange: (next: Selection) => void; projects: Schemas['Project'][]; open: boolean; onToggle: () => void }) {
  const f = copy.folder
  const listId = useId()
  const project = folder.projectId ? projects.find((p) => p.id === folder.projectId) : undefined
  const picked = selection.get(folder.path)
  // More than one import takes (100 conversations, 4 GB): the rest wait for another import.
  const limited = pickable(folder).length < folder.conversations.filter(importable).length
  const anyImportable = folder.conversations.some(importable)
  return (
    <li data-folder={folder.path} className="border-b-[1.5px] border-dashed border-ink/25 last:border-b-0">
      <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 px-1 py-3">
        <span className="pt-0.5">
          <Check checked={folderCheck(selection, folder)} disabled={!anyImportable} onChange={(checked) => onChange(withFolder(selection, folder, checked))} label={folder.path} />
        </span>
        <div className="flex min-w-0 flex-col gap-1">
          <p className="font-mono text-[13.5px] leading-snug font-bold [overflow-wrap:anywhere]">{folder.path}</p>
          <p className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12.5px] text-ink-700">
            <span className="font-bold">{f.conversations(folder.conversations.length)}</span>
            <span aria-hidden="true">·</span>
            <span>{f.lastActive(folder.lastActiveAt)}</span>
            <span aria-hidden="true">·</span>
            {project ? (
              <span className="rounded-stamp bg-tint-blue px-1.5 font-bold text-ink">{f.existingProject(project.name)}</span>
            ) : (
              <span className="rounded-stamp bg-tint-yellow px-1.5 font-bold text-ink">{f.newProject}</span>
            )}
            {picked && <span className="font-black text-pink-text">{copy.pickedInFolder(picked.size)}</span>}
          </p>
          {(folder.truncated || limited) && <p className="text-[12px] font-bold text-ink-700">{f.truncated}</p>}
        </div>
        <button
          type="button"
          aria-expanded={open}
          aria-controls={listId}
          aria-label={open ? f.hideLabel(folder.path) : f.showLabel(folder.path)}
          onClick={onToggle}
          className="inline-flex h-8 items-center gap-1 rounded-full px-2.5 text-[12.5px] font-black text-riso-blue outline-none hover:bg-tint-blue focus-visible:shadow-focus"
        >
          <ChevronRightIcon aria-hidden="true" className={cn('size-4 transition-transform motion-reduce:transition-none', open && 'rotate-90')} strokeWidth={3} />
          {open ? f.hide : f.show}
        </button>
      </div>
      {open && (
        <ul id={listId} aria-label={folder.path} className="mb-3 ml-[34px] divide-y-[1.5px] divide-dashed divide-ink/15 rounded-ticket bg-paper-raised px-2 shadow-[inset_0_0_0_1.5px_rgb(24_38_94/0.18)]">
          {folder.conversations.map((conversation) => (
            <ConversationRow
              key={conversation.sessionId}
              folder={folder}
              conversation={conversation}
              picked={picked?.has(conversation.sessionId) ?? false}
              full={!fits(selection, folder, conversation.sessionId)}
              onPick={(checked) => onChange(withConversation(selection, folder, conversation.sessionId, checked))}
            />
          ))}
        </ul>
      )}
    </li>
  )
}

function Group({ group, ...rest }: { group: FolderGroup; selection: Selection; onChange: (next: Selection) => void; projects: Schemas['Project'][]; openFolders: ReadonlySet<string>; onToggle: (path: string) => void }) {
  const titleId = useId()
  const other = group.name === null
  return (
    <section aria-labelledby={titleId} data-group={group.key} className="flex flex-col">
      <h3 id={titleId} className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-b-2 border-ink pb-1.5">
        <span aria-hidden="true" className={cn('grid size-7 place-items-center rounded-[8px]', other ? 'bg-ink/10 text-ink-700' : 'bg-ink text-riso-yellow shadow-[2px_2px_0_var(--riso-pink)]')}>
          {other ? <FolderIcon className="size-4" strokeWidth={2.4} /> : <GitBranchIcon className="size-4" strokeWidth={2.6} />}
        </span>
        <span className={cn('min-w-0 [overflow-wrap:anywhere]', other ? 'text-[16px] font-black' : 'font-display-latin text-[19px] font-extrabold')}>{group.name ?? copy.groups.other}</span>
        {other && <span className="text-[12px] font-bold text-ink-500">{copy.otherHint}</span>}
      </h3>
      <ul className="flex flex-col">
        {group.folders.map((folder) => (
          <FolderRow key={folder.path} folder={folder} selection={rest.selection} onChange={rest.onChange} projects={rest.projects} open={rest.openFolders.has(folder.path)} onToggle={() => rest.onToggle(folder.path)} />
        ))}
      </ul>
    </section>
  )
}

// What the scan found, as three printed tiles: conversations (by CLI), folders (how many suggested), and those already in Loop Now.
function FoundTiles({ folders }: { folders: Folder[] }) {
  const all = folders.flatMap((folder) => folder.conversations)
  const byCli = (cli: Conversation['cli']) => all.filter((c) => c.cli === cli).length
  const tiles = [
    { key: 'conversations', value: all.length, label: copy.tiles.conversations, note: copy.tiles.byCli(byCli('claude'), byCli('codex')), bg: 'bg-tint-yellow', shadow: 'shadow-[4px_4px_0_var(--ink)]' },
    { key: 'folders', value: folders.length, label: copy.tiles.folders, note: copy.tiles.suggested(folders.filter((f) => f.preChecked).length), bg: 'bg-tint-blue', shadow: 'shadow-[4px_4px_0_var(--riso-blue)]' },
    { key: 'imported', value: all.filter((c) => c.agentId !== null).length, label: copy.tiles.imported, note: copy.tiles.importedNote, bg: 'bg-tint-green', shadow: 'shadow-[4px_4px_0_var(--green)]' },
  ]
  return (
    <ul aria-label={copy.tiles.label} className="grid grid-cols-3 gap-2 sm:gap-3.5">
      {tiles.map((tile) => (
        <li key={tile.key} data-tile={tile.key} className={cn('mr-1 mb-1 flex min-w-0 flex-col gap-0.5 rounded-card border-2 border-ink px-3 pt-2.5 pb-2 sm:px-4 sm:pt-3 sm:pb-2.5', tile.bg, tile.shadow)}>
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-display-latin text-[30px] leading-none font-extrabold sm:text-[40px]">{tile.value}</span>
            <span className="text-[12.5px] font-black sm:text-[14px]">{tile.label}</span>
          </span>
          <span className="min-h-[18px] text-[12px] leading-[18px] text-ink-700 max-sm:hidden">{tile.note}</span>
        </li>
      ))}
    </ul>
  )
}

function FoundFolders({ scan, projectId, starting, onImport }: { scan: Schemas['ConversationScan']; projectId: string | null; starting: boolean; onImport: (body: Schemas['ConversationImportInput']) => void }) {
  const projects = useProjects().data?.projects ?? []
  const [selection, setSelection] = useState<Selection>(() => (projectId ? new Map(scan.folders.flatMap((folder) => [...withFolder(new Map(), folder, true)])) : initialSelection(scan.folders)))
  const suggested = scan.folders.filter((folder) => folder.preChecked)
  // T3: the suggested folders first; everything found on asking. A project's own entry shows all of its folder's conversations.
  const [all, setAll] = useState(projectId !== null || suggested.length === 0)
  // The suggested folders start open, so what an Import takes is in view; a project's own folder too.
  const [openFolders, setOpenFolders] = useState<ReadonlySet<string>>(() => new Set((projectId ? scan.folders : suggested).map((folder) => folder.path)))
  const shown = all ? scan.folders : suggested
  const total = counts(selection)
  const toggle = (path: string) => setOpenFolders((old) => {
    const next = new Set(old)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    return next
  })
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-[clamp(14px,3vw,40px)] pt-6 pb-8">
        <FoundTiles folders={scan.folders} />
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <p className="text-[14.5px] font-black">{all ? copy.found(scan.folders.length) : suggested.length > 0 ? copy.suggested(suggested.length) : copy.noneSuggested}</p>
          {!projectId && scan.folders.length > suggested.length && (
            <Button type="button" size="xs" variant="ghost" onClick={() => setAll((value) => !value)}>
              {all ? copy.suggestedOnly : copy.reviewAll(scan.folders.length)}
            </Button>
          )}
          {all && (
            <span className="ml-auto flex gap-2">
              <Button type="button" size="xs" variant="secondary" onClick={() => setSelection(shown.reduce<Selection>((next, folder) => withFolder(next, folder, true), selection))}>
                {copy.selectAll}
              </Button>
              <Button type="button" size="xs" variant="secondary" onClick={() => setSelection(new Map())}>
                {copy.selectNone}
              </Button>
            </span>
          )}
        </div>
        {groupFolders(shown).map((group) => (
          <Group key={group.key} group={group} selection={selection} onChange={setSelection} projects={projects} openFolders={openFolders} onToggle={toggle} />
        ))}
      </div>
      {/* The bar at the bottom of the sheet: what is picked, and Import. */}
      <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-t-2 border-ink bg-tint-yellow px-[clamp(14px,3vw,40px)] py-3">
        <p role="status" className="min-w-0 flex-1 text-[14px] font-black">
          {total.conversations > 0 ? copy.selected(total.folders, total.conversations) : copy.nothingSelected}
        </p>
        {/* Sent once: the button waits while the import starts (a second one could make a second project for the same folder). */}
        <Button type="button" size="lg" disabled={total.conversations === 0 || starting} onClick={() => onImport(importBody(scan.operationId, scan.folders, selection, projectId))}>
          {starting && <LoaderCircleIcon aria-hidden="true" className="riso-spin motion-reduce:animate-none" strokeWidth={3} />}
          {copy.start}
        </Button>
      </div>
    </div>
  )
}

function ImportResult({ first, scan, onAgain }: { first: Schemas['ConversationImport']; scan: Schemas['ConversationScan']; onAgain: () => void }) {
  const result = useConversationImport(first)
  const client = useQueryClient()
  const projects = useProjects()
  const operation = result.data ?? first
  // A read that fails (the server restarted and forgot the import) stops the polling; it says so and can be tried again.
  const lost = result.isError && operation.state === 'running'
  const finished = operation.state !== 'running'
  useEffect(() => {
    // New projects and Agents: the sidebar and the lists read them again.
    if (finished) void Promise.all([client.invalidateQueries({ queryKey: keys.projects }), client.invalidateQueries({ queryKey: ['agents'] })])
  }, [finished, client])
  const titleOf = (sessionId: string) => scan.folders.flatMap((folder) => folder.conversations).find((c) => c.sessionId === sessionId)?.title ?? sessionId
  const nameOf = (projectId: string) => projects.data?.projects.find((p) => p.id === projectId)?.name
  return (
    <div className="flex flex-col gap-5 overflow-y-auto px-[clamp(14px,3vw,40px)] pt-6 pb-10">
      {lost ? (
        <div role="alert" className="flex flex-col items-start gap-2 rounded-card bg-tint-red px-5 py-4 shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--red)]">
          <p className="text-[15px] font-black text-red-text">{copy.progressLost}</p>
          <ErrorAlert error={result.error} />
          <Button type="button" variant="secondary" onClick={() => void result.refetch()}>
            <RotateCwIcon aria-hidden="true" strokeWidth={2.8} />
            {copy.readAgain}
          </Button>
        </div>
      ) : !finished ? (
        <Progress label={copy.importing(operation.done, operation.total)} done={operation.done} total={operation.total} />
      ) : operation.state === 'failed' ? (
        <div role="alert" className="flex flex-col gap-2 rounded-card bg-tint-red px-5 py-4 shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--red)]">
          <p className="text-[16px] font-black text-red-text">{copy.failed}</p>
          {operation.error && <p className="text-[13.5px] [overflow-wrap:anywhere]">{operation.error}</p>}
        </div>
      ) : (
        <div role="status" className="flex flex-col gap-3 rounded-card bg-tint-green px-5 py-4 shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--green)]">
          <p className="flex items-center gap-2 font-display-cn text-[26px] leading-tight text-green-text">
            <CheckIcon aria-hidden="true" className="size-6" strokeWidth={3.4} />
            {copy.done}
          </p>
          <p className="text-[14px] font-bold">{copy.doneSummary(operation.agents.length)}</p>
          <ul className="flex flex-wrap gap-2.5">
            {operation.projects.map((project) => {
              const name = nameOf(project.projectId)
              return (
                <li key={project.projectId}>
                  {name ? (
                    <Link to={`/p/${encodeURIComponent(name)}/agents`} className="inline-flex h-9 items-center gap-1.5 rounded-full bg-riso-blue px-4 text-[13.5px] font-black text-white shadow-[3px_3px_0_var(--riso-pink)] outline-none focus-visible:shadow-focus">
                      {copy.openProject(name)}
                      <ArrowRightIcon aria-hidden="true" className="size-4" strokeWidth={2.8} />
                    </Link>
                  ) : (
                    <span className="font-mono text-[12.5px]">{project.path}</span>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      )}
      {operation.errors.length > 0 && (
        <section className="flex flex-col gap-2">
          <h3 className="text-[14px] font-black text-red-text">{copy.errors(operation.errors.length)}</h3>
          <ul className="flex flex-col gap-1.5">
            {operation.errors.map((error) => (
              <li key={error.sessionId} className="rounded-ticket bg-tint-red/60 px-3 py-2 text-[13px]">
                <span className="font-black">{titleOf(error.sessionId)}</span>
                <span className="text-ink-700"> — {error.message}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {finished && (
        <Button type="button" variant="secondary" className="self-start" onClick={onAgain}>
          {copy.again}
        </Button>
      )}
    </div>
  )
}

// The import page (as T3's welcome page). /import; ?first=1 when it opened by itself on a fresh install (it can be skipped);
// ?path=<folder>&project=<project id> from a project, for that folder only and into that project.
export function ImportPage() {
  const [params] = useSearchParams()
  const first = params.get('first') === '1'
  const path = params.get('path')
  const projectId = params.get('project')
  const [since, setSince] = useState<'recent' | 'all'>('recent')
  const [attempt, setAttempt] = useState(0)
  const [started, setStarted] = useState<{ key: string; scan: Schemas['ConversationScan'] } | null>(null)
  const [startError, setStartError] = useState<unknown>(null)
  const [imported, setImported] = useState<Schemas['ConversationImport'] | null>(null)
  const [importError, setImportError] = useState<unknown>(null)
  const [starting, setStarting] = useState(false)
  const navigate = useNavigate()
  useEffect(() => {
    if (first) markFirstRunImportShown()
  }, [first])
  const request = `${since}:${path ?? ''}:${attempt}`
  const asked = useRef<string | null>(null)

  useEffect(() => {
    if (asked.current === request) return
    asked.current = request
    setStartError(null)
    // A new scan starts over: nothing imported from it yet.
    setImported(null)
    setImportError(null)
    // The newest 30 days by default (T3); “All” reads the whole history.
    startConversationScan({ since: since === 'recent' ? new Date(Date.now() - 30 * DAY_MS).toISOString() : null, path })
      .then((scan) => setStarted({ key: request, scan }))
      .catch((error: unknown) => setStartError(error))
  }, [request, since, path])

  const live = useConversationScan(started?.key === request ? started.scan : null)
  const scan = started?.key === request ? (live.data ?? started.scan) : null
  const importNow = (body: Schemas['ConversationImportInput']) => {
    if (starting) return
    setStarting(true)
    setImportError(null)
    startConversationImport(body)
      .then(setImported)
      .catch((error: unknown) => setImportError(error))
      .finally(() => setStarting(false))
  }

  return (
    <PageFrame>
      <PageHeader
        crumbs={[{ label: t.shell.global, to: '/' }]}
        title={copy.title}
        note={<span className="max-w-[760px]">{first ? copy.firstRun : copy.intro}</span>}
      />
      <Sheet className="flex-1">
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex shrink-0 flex-wrap items-start gap-x-6 gap-y-3 px-[clamp(14px,3vw,40px)] pt-5">
            <StickyNote className="min-w-0 flex-[1_1_420px] text-[13.5px] leading-relaxed">
              <p className="font-black">{copy.keepRunning}</p>
              <p>{copy.sameFolder}</p>
            </StickyNote>
            {first && !imported && (
              // A fresh install can always leave: while it looks, after a failed look, with nothing found.
              <Button type="button" variant="ghost" onClick={() => { skipFirstRunImport(); void navigate('/') }}>
                {copy.skip}
              </Button>
            )}
            {!imported && (
              <ToggleGroup aria-label={copy.sinceLabel} className={cn(segmentedClass, 'h-9')} value={since} onValueChange={(value) => value && setSince(value as 'recent' | 'all')}>
                <ToggleGroupItem value="recent" className={segmentedItemClass}>
                  {copy.since.recent}
                </ToggleGroupItem>
                <ToggleGroupItem value="all" className={segmentedItemClass}>
                  {copy.since.all}
                </ToggleGroupItem>
              </ToggleGroup>
            )}
          </div>
          {imported && scan ? (
            <ImportResult first={imported} scan={scan} onAgain={() => { setImported(null); setAttempt((n) => n + 1) }} />
          ) : startError !== null || (live.isError && scan?.state !== 'done') ? (
            <div className="flex flex-col items-start gap-3 px-[clamp(14px,3vw,40px)] pt-6">
              <p className="text-[15px] font-black text-red-text">{copy.scanFailed}</p>
              <ErrorAlert error={startError ?? live.error} />
              <Button type="button" variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
                <RotateCwIcon aria-hidden="true" strokeWidth={2.8} />
                {copy.retry}
              </Button>
            </div>
          ) : !scan || scan.state === 'running' ? (
            <div className="px-[clamp(14px,3vw,40px)] pt-6">
              <Progress label={copy.scanning} done={scan?.filesDone ?? 0} total={scan?.filesTotal ?? 0}>
                {scan && scan.filesTotal > 0 ? copy.scanProgress(scan.filesDone, scan.filesTotal) : null}
              </Progress>
            </div>
          ) : scan.state === 'failed' ? (
            <div className="flex flex-col items-start gap-3 px-[clamp(14px,3vw,40px)] pt-6">
              <p className="text-[15px] font-black text-red-text">{copy.scanFailed}</p>
              {scan.error && <p className="text-[13.5px] [overflow-wrap:anywhere]">{scan.error}</p>}
              <Button type="button" variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
                <RotateCwIcon aria-hidden="true" strokeWidth={2.8} />
                {copy.retry}
              </Button>
            </div>
          ) : scan.folders.length === 0 ? (
            <div className="flex flex-col items-start gap-3 px-[clamp(14px,3vw,40px)] pt-6">
              <p className="text-[15px] font-black">{since === 'recent' ? copy.nothingRecent : copy.nothing}</p>
            </div>
          ) : (
            <>
              {importError !== null && <ErrorAlert error={importError} className="mx-[clamp(14px,3vw,40px)] mt-4 w-auto" />}
              <FoundFolders key={scan.operationId} scan={scan} projectId={projectId} starting={starting} onImport={importNow} />
            </>
          )}
        </div>
      </Sheet>
    </PageFrame>
  )
}
