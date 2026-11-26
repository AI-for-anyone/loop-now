import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  CheckIcon,
  ChevronRightIcon,
  FolderIcon,
  GitBranchIcon,
  InfoIcon,
  LoaderCircleIcon,
  MinusIcon,
  RotateCwIcon,
  SearchIcon,
} from 'lucide-react'
import type { Schemas } from '@/api/client'
import { startConversationImport, startConversationScan, useConversationImport, useConversationScan } from '@/api/conversation-import'
import { keys, useProjects } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { overviewPath } from '@/lib/scope'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { narrowQuery, useMediaQuery } from '@/lib/use-media-query'
import { cn } from '@/lib/utils'
import { markFirstRunImportShown, skipFirstRunImport } from './first-run'
import {
  baseName,
  counts,
  filterFolders,
  fits,
  folderCheck,
  groupFolders,
  importable,
  importBody,
  initialSelection,
  pickable,
  withConversation,
  withFolder,
  type CliFilter,
  type FolderGroup,
  type Selection,
} from './selection'

type Folder = Schemas['ImportFolder']
type Conversation = Schemas['ImportConversation']

const copy = t.importing
const DAY_MS = 24 * 60 * 60 * 1000
const CLI_FILTERS: CliFilter[] = ['all', 'claude', 'codex']

// Custom checkbox: crisp monochrome check in mono skin; hard navy offset with yellow fill in Plate skin.
function Check({
  checked,
  onChange,
  disabled,
  label,
  describedBy,
}: {
  checked: boolean | 'indeterminate'
  onChange: (checked: boolean) => void
  disabled?: boolean
  label: string
  describedBy?: string
}) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = checked === 'indeterminate'
  }, [checked])
  return (
    <span className="relative inline-grid size-[16px] shrink-0 place-items-center [[data-skin=plate]_&]:size-[20px]">
      <input
        ref={ref}
        type="checkbox"
        aria-label={label}
        aria-describedby={describedBy}
        checked={checked === true}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        onClick={(event) => event.stopPropagation()}
        className="peer absolute inset-0 m-0 cursor-pointer appearance-none rounded-[4px] border border-hairline-strong bg-surface-control outline-none checked:border-cta-bg checked:bg-cta-bg indeterminate:border-cta-bg indeterminate:bg-cta-bg focus-visible:shadow-focus disabled:cursor-not-allowed disabled:border-hairline disabled:bg-surface-subtle [[data-skin=plate]_&]:rounded-[5px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)] [[data-skin=plate]_&]:checked:border-ink [[data-skin=plate]_&]:checked:bg-riso-yellow [[data-skin=plate]_&]:indeterminate:border-ink [[data-skin=plate]_&]:indeterminate:bg-riso-yellow [[data-skin=plate]_&]:disabled:border-ink/25 [[data-skin=plate]_&]:disabled:bg-ink/[0.04] [[data-skin=plate]_&]:disabled:shadow-none"
      />
      <CheckIcon
        aria-hidden="true"
        className="pointer-events-none relative hidden size-[12px] text-cta-fg peer-checked:block [[data-skin=plate]_&]:size-[14px] [[data-skin=plate]_&]:text-ink"
        strokeWidth={3.2}
      />
      <MinusIcon
        aria-hidden="true"
        className="pointer-events-none relative hidden size-[12px] text-cta-fg peer-indeterminate:block [[data-skin=plate]_&]:size-[14px] [[data-skin=plate]_&]:text-ink"
        strokeWidth={3.2}
      />
    </span>
  )
}

// Progress card for scanning or importing.
function Progress({ label, done, total, children }: { label: string; done: number; total: number; children?: ReactNode }) {
  const share = total > 0 ? Math.min(1, done / total) : 0
  return (
    <div className="flex flex-col gap-3 rounded-card border border-hairline-strong bg-surface-elevated px-5 py-4 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--riso-blue)]">
      <p role="status" className="flex items-center gap-2 text-[14.5px] font-semibold [[data-skin=plate]_&]:font-black">
        <LoaderCircleIcon
          aria-hidden="true"
          className="riso-spin size-4 text-ink motion-reduce:animate-none [[data-skin=plate]_&]:text-pink-text"
          strokeWidth={2.5}
        />
        {label}
      </p>
      <span
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
        className="riso-skeleton relative block h-2 overflow-hidden rounded-full border border-hairline bg-surface-subtle [[data-skin=plate]_&]:h-3 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]"
      >
        <span
          className="riso-halftone absolute inset-y-0 left-0 rounded-full bg-cta-bg transition-[width] motion-reduce:transition-none [[data-skin=plate]_&]:bg-riso-blue"
          style={{ width: `${Math.max(share * 100, total > 0 ? 4 : 0)}%` }}
        />
      </span>
      {children && <p className="text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{children}</p>}
    </div>
  )
}

// Compact 44–48px conversation row.
function ConversationRow({
  folder,
  conversation,
  picked,
  full,
  onPick,
}: {
  folder: Folder
  conversation: Conversation
  picked: boolean
  full: boolean
  onPick: (checked: boolean) => void
}) {
  const c = copy.conversation
  const metaId = useId()
  const done = conversation.agentId !== null
  const blocked = conversation.skipReason !== null
  const title = conversation.title.trim() || c.untitled
  return (
    <li
      data-conversation={conversation.sessionId}
      data-picked={picked || undefined}
      className={cn(
        'grid min-h-[44px] grid-cols-[auto_minmax(0,1fr)] items-start gap-x-2.5 rounded-ticket px-2 py-1.5 transition-colors motion-reduce:transition-none [[data-skin=plate]_&]:rounded-[8px]',
        picked && 'bg-surface-active/65 [[data-skin=plate]_&]:bg-tint-yellow',
        (done || blocked) && 'opacity-80',
      )}
    >
      <span className="pt-0.5">
        <Check checked={picked} disabled={done || blocked || (!picked && full)} onChange={onPick} label={title} describedBy={metaId} />
      </span>
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
          <span className="min-w-0 truncate text-[13.5px] leading-snug font-semibold [[data-skin=plate]_&]:font-black">{title}</span>
          <span className="shrink-0 rounded-stamp bg-cta-bg px-1.5 text-[10.5px] leading-[17px] font-semibold text-cta-fg [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white">
            {copy.cli[conversation.cli]}
          </span>
          {done && (
            <>
              <span className="shrink-0 rounded-stamp bg-tint-green px-1.5 text-[11px] leading-[17px] font-semibold text-green-text [[data-skin=plate]_&]:font-black">
                {c.imported}
              </span>
              <Link
                to={`/agents/${conversation.agentId}/chat`}
                className="inline-flex shrink-0 items-center gap-0.5 text-[12px] font-medium text-ink underline decoration-1 underline-offset-[2px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue"
              >
                {c.openAgent}
                <ArrowRightIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />
              </Link>
            </>
          )}
        </p>
        {conversation.firstPrompt && conversation.firstPrompt !== conversation.title && (
          <p className="line-clamp-1 text-[12px] leading-snug text-ink-700 [overflow-wrap:anywhere]">{conversation.firstPrompt}</p>
        )}
        <p id={metaId} className="flex flex-wrap gap-x-1.5 text-[11.5px] leading-snug text-ink-500">
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
          {blocked && <span className="basis-full font-semibold text-red-text">{c.skipped(copy.skipReasons[conversation.skipReason!])}</span>}
          {conversation.cwd !== folder.path && <span className="basis-full font-mono [overflow-wrap:anywhere]">{c.inFolder(conversation.cwd)}</span>}
        </p>
      </div>
    </li>
  )
}

function ProjectBadge({ project }: { project: Schemas['Project'] | undefined }) {
  const f = copy.folder
  return project ? (
    <span className="truncate rounded-stamp border border-hairline bg-surface-subtle px-1.5 text-[11px] leading-[18px] font-medium text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink">
      {f.existingProject(project.name)}
    </span>
  ) : (
    <span className="shrink-0 rounded-stamp border border-hairline bg-tint-yellow px-1.5 text-[11px] leading-[18px] font-medium text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-bold">
      {f.newProject}
    </span>
  )
}

// Desktop left-pane folder item: tri-state checkbox, folder path, project badge, and picked/total counter.
function MasterFolderRow({
  folder,
  fullFolder,
  selection,
  onChange,
  projects,
  active,
  onSelect,
}: {
  folder: Folder
  fullFolder: Folder
  selection: Selection
  onChange: (next: Selection) => void
  projects: Schemas['Project'][]
  active: boolean
  onSelect: () => void
}) {
  const project = folder.projectId ? projects.find((p) => p.id === folder.projectId) : undefined
  const pickedCount = folder.conversations.filter((c) => selection.get(folder.path)?.has(c.sessionId)).length
  const totalCount = folder.conversations.length
  const anyImportable = folder.conversations.some(importable)
  const checkState = folderCheck(selection, folder)

  return (
    <li data-folder={folder.path} data-active={active || undefined}>
      <div
        className={cn(
          'group flex items-start gap-2 rounded-ticket px-2 py-2 transition-colors',
          active
            ? 'bg-surface-active text-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:shadow-[inset_0_0_0_2px_var(--ink)]'
            : 'hover:bg-surface-hover [[data-skin=plate]_&]:hover:bg-tint-blue/70',
        )}
      >
        <span className="pt-0.5">
          <Check
            checked={checkState}
            disabled={!anyImportable}
            onChange={(checked) => onChange(withFolder(selection, fullFolder === folder ? fullFolder : folder, checked))}
            label={folder.path}
          />
        </span>
        <button
          type="button"
          onClick={onSelect}
          aria-current={active ? 'true' : undefined}
          className="flex min-w-0 flex-1 cursor-pointer flex-col gap-0.5 text-left outline-none focus-visible:shadow-focus"
        >
          <span className="flex min-w-0 items-center justify-between gap-2">
            <span className="truncate font-mono text-[12.5px] leading-snug font-semibold text-ink [[data-skin=plate]_&]:font-bold" title={folder.path}>
              {baseName(folder.path)}
            </span>
            <span
              className={cn(
                'shrink-0 font-mono text-[11px] tabular-nums',
                pickedCount > 0 ? 'font-semibold text-ink [[data-skin=plate]_&]:text-pink-text' : 'text-ink-500',
              )}
            >
              {copy.folderCount(pickedCount, totalCount)}
            </span>
          </span>
          <span className="truncate font-mono text-[11px] leading-tight text-ink-500">{folder.path}</span>
          <span className="mt-0.5 flex min-w-0 items-center gap-1.5">
            <ProjectBadge project={project} />
          </span>
        </button>
      </div>
    </li>
  )
}

// Narrow-viewport collapsible accordion row fallback.
function AccordionFolderRow({
  folder,
  fullFolder,
  selection,
  onChange,
  projects,
  open,
  onToggle,
}: {
  folder: Folder
  fullFolder: Folder
  selection: Selection
  onChange: (next: Selection) => void
  projects: Schemas['Project'][]
  open: boolean
  onToggle: () => void
}) {
  const f = copy.folder
  const listId = useId()
  const project = folder.projectId ? projects.find((p) => p.id === folder.projectId) : undefined
  const picked = selection.get(folder.path)
  const limited = pickable(fullFolder).length < fullFolder.conversations.filter(importable).length
  const anyImportable = folder.conversations.some(importable)
  return (
    <li
      data-folder={folder.path}
      className="border-b border-hairline last:border-b-0 [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25 [[data-skin=plate]_&]:last:border-b-0"
    >
      <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-2.5 px-1 py-2.5">
        <span className="pt-0.5">
          <Check
            checked={folderCheck(selection, folder)}
            disabled={!anyImportable}
            onChange={(checked) => onChange(withFolder(selection, fullFolder === folder ? fullFolder : folder, checked))}
            label={folder.path}
          />
        </span>
        <div className="flex min-w-0 flex-col gap-0.5">
          <p className="font-mono text-[13px] leading-snug font-medium [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-bold">{folder.path}</p>
          <p className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12px] text-ink-700">
            <span className="font-medium [[data-skin=plate]_&]:font-bold">{f.conversations(folder.conversations.length)}</span>
            <span aria-hidden="true">·</span>
            <span>{f.lastActive(folder.lastActiveAt)}</span>
            <span aria-hidden="true">·</span>
            <ProjectBadge project={project} />
            {picked && <span className="font-semibold text-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-pink-text">{copy.pickedInFolder(picked.size)}</span>}
          </p>
          {(fullFolder.truncated || limited) && <p className="text-[11.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{f.truncated}</p>}
        </div>
        <button
          type="button"
          aria-expanded={open}
          aria-controls={listId}
          aria-label={open ? f.hideLabel(folder.path) : f.showLabel(folder.path)}
          onClick={onToggle}
          className="inline-flex h-7 items-center gap-1 rounded-full px-2 text-[12px] font-medium text-ink-700 outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue"
        >
          <ChevronRightIcon aria-hidden="true" className={cn('size-3.5 transition-transform motion-reduce:transition-none', open && 'rotate-90')} strokeWidth={2.4} />
          {open ? f.hide : f.show}
        </button>
      </div>
      {open && (
        <ul
          id={listId}
          aria-label={folder.path}
          className="mb-2.5 ml-6 divide-y divide-hairline rounded-ticket border border-hairline bg-surface-subtle px-1.5 [[data-skin=plate]_&]:divide-y-[1.5px] [[data-skin=plate]_&]:divide-dashed [[data-skin=plate]_&]:divide-ink/15 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[inset_0_0_0_1.5px_rgb(24_38_94/0.18)]"
        >
          {folder.conversations.map((conversation) => (
            <ConversationRow
              key={conversation.sessionId}
              folder={fullFolder}
              conversation={conversation}
              picked={picked?.has(conversation.sessionId) ?? false}
              full={!fits(selection, fullFolder, conversation.sessionId)}
              onPick={(checked) => onChange(withConversation(selection, fullFolder, conversation.sessionId, checked))}
            />
          ))}
        </ul>
      )}
    </li>
  )
}

function MasterGroup({
  group,
  folderByPath,
  selection,
  onChange,
  projects,
  activePath,
  onSelect,
}: {
  group: FolderGroup
  folderByPath: ReadonlyMap<string, Folder>
  selection: Selection
  onChange: (next: Selection) => void
  projects: Schemas['Project'][]
  activePath: string | null
  onSelect: (path: string) => void
}) {
  const titleId = useId()
  const other = group.name === null
  return (
    <section aria-labelledby={titleId} data-group={group.key} className="flex flex-col gap-1">
      <h3
        id={titleId}
        className="flex items-center gap-1.5 px-2 pt-2 pb-1 text-[11px] font-semibold tracking-[0.04em] text-ink-500 uppercase [[data-skin=plate]_&]:font-black"
      >
        {other ? <FolderIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} /> : <GitBranchIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />}
        <span className="truncate">{group.name ?? copy.groups.other}</span>
      </h3>
      <ul className="flex flex-col gap-0.5">
        {group.folders.map((folder) => (
          <MasterFolderRow
            key={folder.path}
            folder={folder}
            fullFolder={folderByPath.get(folder.path) ?? folder}
            selection={selection}
            onChange={onChange}
            projects={projects}
            active={folder.path === activePath}
            onSelect={() => onSelect(folder.path)}
          />
        ))}
      </ul>
    </section>
  )
}

function AccordionGroup({
  group,
  folderByPath,
  selection,
  onChange,
  projects,
  openFolders,
  onToggle,
}: {
  group: FolderGroup
  folderByPath: ReadonlyMap<string, Folder>
  selection: Selection
  onChange: (next: Selection) => void
  projects: Schemas['Project'][]
  openFolders: ReadonlySet<string>
  onToggle: (path: string) => void
}) {
  const titleId = useId()
  const other = group.name === null
  return (
    <section aria-labelledby={titleId} data-group={group.key} className="flex flex-col">
      <h3
        id={titleId}
        className="flex flex-wrap items-center gap-x-2 gap-y-0.5 border-b border-hairline-strong pb-1.5 text-[13px] font-semibold text-ink [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:font-black"
      >
        {other ? <FolderIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} /> : <GitBranchIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />}
        <span className="min-w-0 [overflow-wrap:anywhere]">{group.name ?? copy.groups.other}</span>
        {other && <span className="text-[11.5px] font-normal text-ink-500">{copy.otherHint}</span>}
      </h3>
      <ul className="flex flex-col">
        {group.folders.map((folder) => (
          <AccordionFolderRow
            key={folder.path}
            folder={folder}
            fullFolder={folderByPath.get(folder.path) ?? folder}
            selection={selection}
            onChange={onChange}
            projects={projects}
            open={openFolders.has(folder.path)}
            onToggle={() => onToggle(folder.path)}
          />
        ))}
      </ul>
    </section>
  )
}

function FoundFolders({
  scan,
  projectId,
  starting,
  selection,
  onSelectionChange,
  onImport,
}: {
  scan: Schemas['ConversationScan']
  projectId: string | null
  starting: boolean
  selection: Selection
  onSelectionChange: (next: Selection) => void
  onImport: (body: Schemas['ConversationImportInput']) => void
}) {
  const projects = useProjects().data?.projects ?? []
  const narrow = useMediaQuery(narrowQuery)
  const suggested = useMemo(() => scan.folders.filter((folder) => folder.preChecked), [scan.folders])
  const [all, setAll] = useState(projectId !== null || suggested.length === 0)
  const [search, setSearch] = useState('')
  const [cliFilter, setCliFilter] = useState<CliFilter>('all')
  const [activePath, setActivePath] = useState<string | null>(() => (projectId ? scan.folders : suggested.length > 0 ? suggested : scan.folders)[0]?.path ?? null)
  const [openFolders, setOpenFolders] = useState<ReadonlySet<string>>(() => new Set((projectId ? scan.folders : suggested).map((folder) => folder.path)))

  const leftScrollRef = useRef<HTMLDivElement | null>(null)
  const rightScrollRef = useRef<HTMLDivElement | null>(null)
  const narrowScrollRef = useRef<HTMLDivElement | null>(null)

  const folderByPath = useMemo(() => new Map(scan.folders.map((folder) => [folder.path, folder])), [scan.folders])
  const scopedFolders = all ? scan.folders : suggested
  const filteredFolders = useMemo(() => filterFolders(scopedFolders, { query: search, cli: cliFilter }), [scopedFolders, search, cliFilter])
  const groups = useMemo(() => groupFolders(filteredFolders), [filteredFolders])

  const allConversations = useMemo(() => scan.folders.flatMap((folder) => folder.conversations), [scan.folders])
  const importedCount = useMemo(() => allConversations.filter((c) => c.agentId !== null).length, [allConversations])
  const total = counts(selection)

  // Keep an active folder selected in desktop 2-pane mode whenever the filtered list changes.
  const activeFolder = useMemo(
    () => filteredFolders.find((folder) => folder.path === activePath) ?? filteredFolders[0] ?? null,
    [filteredFolders, activePath],
  )
  const activeFullFolder = activeFolder ? (folderByPath.get(activeFolder.path) ?? activeFolder) : null
  const activeProject = activeFolder?.projectId ? projects.find((p) => p.id === activeFolder.projectId) : undefined
  const activeLimited = activeFullFolder ? pickable(activeFullFolder).length < activeFullFolder.conversations.filter(importable).length : false

  const toggleAccordion = (path: string) =>
    setOpenFolders((old) => {
      const next = new Set(old)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })

  const hasFilters = search.trim() !== '' || cliFilter !== 'all'

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Compact 36px summary + inline filter toolbar */}
      <div className="flex min-h-9 shrink-0 flex-wrap items-center gap-x-2.5 gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:px-6 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink/20 [[data-skin=plate]_&]:bg-transparent">
        <span className="text-[12px] font-medium text-ink-700 tabular-nums [[data-skin=plate]_&]:font-bold">
          {copy.summaryBar(scan.folders.length, allConversations.length, importedCount)}
        </span>

        <label className="relative flex min-w-[160px] flex-1 items-center sm:max-w-[240px]">
          <SearchIcon aria-hidden="true" className="pointer-events-none absolute left-2.5 size-3.5 text-ink-500" strokeWidth={2.2} />
          <input
            type="search"
            aria-label={copy.searchLabel}
            placeholder={copy.searchPlaceholder}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="h-7 w-full rounded-ticket border border-hairline-strong bg-surface-control pr-2.5 pl-7 text-[12px] text-ink outline-none placeholder:text-ink-500 focus-visible:shadow-focus [[data-skin=plate]_&]:border-ink/40 [[data-skin=plate]_&]:bg-paper-raised"
          />
        </label>

        <ToggleGroup
          aria-label={copy.cliFilterLabel}
          className={cn(segmentedClass, 'h-7')}
          value={cliFilter}
          onValueChange={(value) => value && setCliFilter(value as CliFilter)}
        >
          {CLI_FILTERS.map((cli) => (
            <ToggleGroupItem key={cli} value={cli} className={cn(segmentedItemClass, 'px-2.5 text-[11.5px]')}>
              {cli === 'all' ? copy.cliAll : copy.cli[cli]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        {!projectId && scan.folders.length > suggested.length && (
          <ToggleGroup
            aria-label={copy.scopeToggleLabel}
            className={cn(segmentedClass, 'h-7')}
            value={all ? 'all' : 'suggested'}
            onValueChange={(value) => {
              if (!value) return
              setAll(value === 'all')
            }}
          >
            <ToggleGroupItem value="suggested" className={cn(segmentedItemClass, 'px-2.5 text-[11.5px]')}>
              {copy.suggestedTab(suggested.length)}
            </ToggleGroupItem>
            <ToggleGroupItem value="all" className={cn(segmentedItemClass, 'px-2.5 text-[11.5px]')}>
              {copy.allFoldersTab(scan.folders.length)}
            </ToggleGroupItem>
          </ToggleGroup>
        )}

        <span className="ml-auto flex shrink-0 items-center gap-1.5">
          <Button
            type="button"
            size="xs"
            variant="secondary"
            className="h-7 px-2.5 text-[12px]"
            disabled={filteredFolders.length === 0}
            onClick={() => onSelectionChange(filteredFolders.reduce<Selection>((next, folder) => withFolder(next, folder, true), selection))}
          >
            {copy.selectAll}
          </Button>
          <Button
            type="button"
            size="xs"
            variant="secondary"
            className="h-7 px-2.5 text-[12px]"
            disabled={total.conversations === 0}
            onClick={() => onSelectionChange(new Map())}
          >
            {copy.selectNone}
          </Button>
        </span>
      </div>

      {/* Main split / accordion workspace */}
      {filteredFolders.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2.5 px-6 py-10 text-center">
          <p className="text-[13.5px] font-medium text-ink-700">{copy.noMatches}</p>
          {hasFilters && (
            <Button
              type="button"
              size="xs"
              variant="secondary"
              onClick={() => {
                setSearch('')
                setCliFilter('all')
              }}
            >
              {copy.clearFilters}
            </Button>
          )}
        </div>
      ) : narrow ? (
        <div className="relative flex min-h-0 flex-1 flex-col">
          <div ref={narrowScrollRef} className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
            <div className="flex flex-col gap-4">
              {groups.map((group) => (
                <AccordionGroup
                  key={group.key}
                  group={group}
                  folderByPath={folderByPath}
                  selection={selection}
                  onChange={onSelectionChange}
                  projects={projects}
                  openFolders={openFolders}
                  onToggle={toggleAccordion}
                />
              ))}
            </div>
          </div>
          <ScrollRuler targetRef={narrowScrollRef} label={copy.panes.folders} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1">
          {/* Left pane: grouped folder list */}
          <div className="relative flex w-[300px] shrink-0 flex-col border-r border-hairline [[data-skin=plate]_&]:border-r-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
            <div ref={leftScrollRef} aria-label={copy.panes.folders} className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
              <div className="flex flex-col gap-2">
                {groups.map((group) => (
                  <MasterGroup
                    key={group.key}
                    group={group}
                    folderByPath={folderByPath}
                    selection={selection}
                    onChange={onSelectionChange}
                    projects={projects}
                    activePath={activeFolder?.path ?? null}
                    onSelect={setActivePath}
                  />
                ))}
              </div>
            </div>
            <ScrollRuler targetRef={leftScrollRef} label={copy.panes.folders} />
          </div>

          {/* Right pane: compact conversation rows for the active folder */}
          <div className="relative flex min-w-0 flex-1 flex-col">
            {activeFolder && activeFullFolder ? (
              <>
                <div className="flex shrink-0 flex-wrap items-center gap-x-2.5 gap-y-1 border-b border-hairline bg-surface-subtle/50 px-4 py-2 sm:px-5">
                  <span className="min-w-0 truncate font-mono text-[12.5px] font-semibold text-ink [[data-skin=plate]_&]:font-bold" title={activeFolder.path}>
                    {activeFolder.path}
                  </span>
                  <ProjectBadge project={activeProject} />
                  <span className="ml-auto shrink-0 text-[11.5px] text-ink-500">
                    {copy.folder.conversations(activeFolder.conversations.length)} · {copy.folder.lastActive(activeFolder.lastActiveAt)}
                  </span>
                  {(activeFullFolder.truncated || activeLimited) && (
                    <p className="basis-full text-[11.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{copy.folder.truncated}</p>
                  )}
                </div>
                <div ref={rightScrollRef} className="min-h-0 flex-1 overflow-y-auto px-4 py-2 sm:px-5">
                  <ul aria-label={activeFolder.path} className="flex flex-col divide-y divide-hairline [[data-skin=plate]_&]:divide-y-[1.5px] [[data-skin=plate]_&]:divide-dashed [[data-skin=plate]_&]:divide-ink/15">
                    {activeFolder.conversations.map((conversation) => (
                      <ConversationRow
                        key={conversation.sessionId}
                        folder={activeFullFolder}
                        conversation={conversation}
                        picked={selection.get(activeFolder.path)?.has(conversation.sessionId) ?? false}
                        full={!fits(selection, activeFullFolder, conversation.sessionId)}
                        onPick={(checked) => onSelectionChange(withConversation(selection, activeFullFolder, conversation.sessionId, checked))}
                      />
                    ))}
                  </ul>
                </div>
                <ScrollRuler targetRef={rightScrollRef} label={copy.panes.conversations} />
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center px-6 text-[13px] text-ink-500">{copy.selectFolderPrompt}</div>
            )}
          </div>
        </div>
      )}

      {/* Compact 48px sticky bottom action bar */}
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-hairline-strong bg-surface-elevated px-4 py-2 sm:px-6 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow">
        <p role="status" className="min-w-0 flex-1 text-[13px] font-semibold text-ink [[data-skin=plate]_&]:font-black">
          {total.conversations > 0 ? copy.selected(total.folders, total.conversations) : copy.nothingSelected}
        </p>
        <Button
          type="button"
          size="sm"
          className="h-9 px-4"
          disabled={total.conversations === 0 || starting}
          onClick={() => onImport(importBody(scan.operationId, scan.folders, selection, projectId))}
        >
          {starting && <LoaderCircleIcon aria-hidden="true" className="riso-spin motion-reduce:animate-none" strokeWidth={3} />}
          {copy.start}
        </Button>
      </div>
    </div>
  )
}

function ImportResult({
  first,
  scan,
  onAgain,
  onBack,
}: {
  first: Schemas['ConversationImport']
  scan: Schemas['ConversationScan']
  onAgain: () => void
  onBack: () => void
}) {
  const result = useConversationImport(first)
  const client = useQueryClient()
  const projects = useProjects()
  const operation = result.data ?? first
  const lost = result.isError && operation.state === 'running'
  const finished = operation.state !== 'running'
  const hasErrors = operation.state === 'failed' || operation.errors.length > 0

  useEffect(() => {
    if (finished) void Promise.all([client.invalidateQueries({ queryKey: keys.projects }), client.invalidateQueries({ queryKey: ['agents'] })])
  }, [finished, client])

  const titleOf = (sessionId: string) => scan.folders.flatMap((folder) => folder.conversations).find((c) => c.sessionId === sessionId)?.title ?? sessionId
  const nameOf = (projectId: string) => projects.data?.projects.find((p) => p.id === projectId)?.name

  return (
    <PageBody width="default" innerClassName="flex flex-col gap-5">
      {lost ? (
        <div
          role="alert"
          className="flex flex-col items-start gap-2 rounded-card border border-red/25 bg-tint-red px-5 py-4 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--red)]"
        >
          <p className="text-[15px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{copy.progressLost}</p>
          <ErrorAlert error={result.error} />
          <Button type="button" variant="secondary" size="sm" onClick={() => void result.refetch()}>
            <RotateCwIcon aria-hidden="true" strokeWidth={2.4} />
            {copy.readAgain}
          </Button>
        </div>
      ) : !finished ? (
        <Progress label={copy.importing(operation.done, operation.total)} done={operation.done} total={operation.total} />
      ) : operation.state === 'failed' ? (
        <div
          role="alert"
          className="flex flex-col gap-2 rounded-card border border-red/25 bg-tint-red px-5 py-4 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--red)]"
        >
          <p className="text-[16px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{copy.failed}</p>
          {operation.error && <p className="text-[13.5px] [overflow-wrap:anywhere]">{operation.error}</p>}
        </div>
      ) : (
        <div
          role="status"
          className="flex flex-col gap-3 rounded-card border border-green/25 bg-tint-green px-5 py-4 shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--green)]"
        >
          <p className="flex items-center gap-2 font-display-cn text-[20px] leading-snug font-semibold text-green-text [[data-skin=plate]_&]:text-[26px] [[data-skin=plate]_&]:leading-tight [[data-skin=plate]_&]:font-normal">
            <CheckIcon aria-hidden="true" className="size-5 [[data-skin=plate]_&]:size-6" strokeWidth={2.8} />
            {copy.done}
          </p>
          <p className="text-[14px] font-medium [[data-skin=plate]_&]:font-bold">{copy.doneSummary(operation.agents.length)}</p>
          <ul className="flex flex-wrap gap-2.5">
            {operation.projects.map((project) => {
              const name = nameOf(project.projectId)
              return (
                <li key={project.projectId}>
                  {name ? (
                    <Link
                      to={`/p/${encodeURIComponent(name)}/agents`}
                      className="inline-flex h-9 items-center gap-1.5 rounded-full bg-cta-bg px-4 text-[13.5px] font-semibold text-cta-fg outline-none hover:bg-cta-hover focus-visible:shadow-focus [[data-skin=plate]_&]:bg-riso-blue [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--riso-pink)]"
                    >
                      {copy.openProject(name)}
                      <ArrowRightIcon aria-hidden="true" className="size-4" strokeWidth={2.4} />
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
          <h3 className="text-[13.5px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{copy.errors(operation.errors.length)}</h3>
          <ul className="flex flex-col gap-1.5">
            {operation.errors.map((error) => (
              <li key={error.sessionId} className="rounded-ticket border border-red/20 bg-tint-red/60 px-3 py-2 text-[13px] [[data-skin=plate]_&]:border-0">
                <span className="font-semibold [[data-skin=plate]_&]:font-black">{titleOf(error.sessionId)}</span>
                <span className="text-ink-700"> — {error.message}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {finished && (
        <div className="flex flex-wrap items-center gap-2">
          {hasErrors && (
            <Button type="button" variant="secondary" size="sm" onClick={onBack}>
              <ArrowLeftIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
              {copy.backToSelection}
            </Button>
          )}
          <Button type="button" variant="secondary" size="sm" onClick={onAgain}>
            {copy.again}
          </Button>
        </div>
      )}
    </PageBody>
  )
}

function defaultSelectionFor(scan: Schemas['ConversationScan'], projectId: string | null): Selection {
  return projectId
    ? new Map(scan.folders.flatMap((folder) => [...withFolder(new Map(), folder, true)]))
    : initialSelection(scan.folders)
}

// The import page (/import): ?first=1 opens on fresh install (skippable); ?path=<folder>&project=<project id> targets a project's directory.
export function ImportPage() {
  const [params] = useSearchParams()
  const first = params.get('first') === '1'
  const path = params.get('path')
  const projectId = params.get('project')
  const projects = useProjects().data?.projects ?? []
  const targetProject = projectId ? projects.find((p) => p.id === projectId) : undefined

  const [since, setSince] = useState<'recent' | 'all'>('recent')
  const [attempt, setAttempt] = useState(0)
  const [started, setStarted] = useState<{ key: string; scan: Schemas['ConversationScan'] } | null>(null)
  const [startError, setStartError] = useState<unknown>(null)
  const [imported, setImported] = useState<Schemas['ConversationImport'] | null>(null)
  const [importError, setImportError] = useState<unknown>(null)
  const [starting, setStarting] = useState(false)
  const [savedSelection, setSavedSelection] = useState<{ scanId: string; selection: Selection } | null>(null)
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
    setImported(null)
    setImportError(null)
    startConversationScan({ since: since === 'recent' ? new Date(Date.now() - 30 * DAY_MS).toISOString() : null, path })
      .then((scan) => setStarted({ key: request, scan }))
      .catch((error: unknown) => setStartError(error))
  }, [request, since, path])

  const live = useConversationScan(started?.key === request ? started.scan : null)
  const scan = started?.key === request ? (live.data ?? started.scan) : null

  const activeSelection = useMemo(() => {
    if (!scan || scan.state !== 'done') return new Map<string, ReadonlySet<string>>()
    if (savedSelection?.scanId === scan.operationId) return savedSelection.selection
    return defaultSelectionFor(scan, projectId)
  }, [scan, savedSelection, projectId])

  const setSelection = (next: Selection) => {
    if (!scan) return
    setSavedSelection({ scanId: scan.operationId, selection: next })
  }

  const importNow = (body: Schemas['ConversationImportInput']) => {
    if (starting) return
    setStarting(true)
    setImportError(null)
    startConversationImport(body)
      .then(setImported)
      .catch((error: unknown) => setImportError(error))
      .finally(() => setStarting(false))
  }

  const crumbs = targetProject
    ? [{ label: targetProject.name, to: overviewPath({ kind: 'project', name: targetProject.name }) }]
    : [{ label: t.shell.global, to: '/' }]

  return (
    <PageFrame>
      <PageHeader
        crumbs={crumbs}
        title={copy.title}
        note={first ? <span className="max-w-[760px]">{copy.firstRun}</span> : undefined}
        actions={
          !imported ? (
            <>
              <ToggleGroup
                aria-label={copy.sinceLabel}
                className={segmentedClass}
                value={since}
                onValueChange={(value) => value && setSince(value as 'recent' | 'all')}
              >
                <ToggleGroupItem value="recent" className={segmentedItemClass}>
                  {copy.since.recent}
                </ToggleGroupItem>
                <ToggleGroupItem value="all" className={segmentedItemClass}>
                  {copy.since.all}
                </ToggleGroupItem>
              </ToggleGroup>
              {first && (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  className="h-8"
                  onClick={() => {
                    skipFirstRunImport()
                    void navigate('/')
                  }}
                >
                  {copy.skip}
                </Button>
              )}
            </>
          ) : undefined
        }
      />
      <Sheet className="flex-1 [[data-skin=plate]_&]:mt-2">
        <div className="flex min-h-0 flex-1 flex-col">
          {/* Compact 1-line info bar shown only during scan/select phases (never during ImportResult). */}
          {!imported && (
            <div className="flex shrink-0 items-center gap-2 border-b border-hairline bg-surface-subtle/60 px-4 py-1.5 text-[12px] text-ink-700 sm:px-6 [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/20 [[data-skin=plate]_&]:bg-tint-yellow/50">
              <InfoIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-500" strokeWidth={2.2} />
              <span className="min-w-0">{copy.infoBar}</span>
            </div>
          )}

          {imported && scan ? (
            <ImportResult
              first={imported}
              scan={scan}
              onBack={() => setImported(null)}
              onAgain={() => {
                setImported(null)
                setSavedSelection(null)
                setAttempt((n) => n + 1)
              }}
            />
          ) : startError !== null || (live.isError && scan?.state !== 'done') ? (
            <PageBody width="default" innerClassName="flex flex-col items-start gap-3">
              <p className="text-[14.5px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{copy.scanFailed}</p>
              <ErrorAlert error={startError ?? live.error} />
              <Button type="button" variant="secondary" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                <RotateCwIcon aria-hidden="true" strokeWidth={2.4} />
                {copy.retry}
              </Button>
            </PageBody>
          ) : !scan || scan.state === 'running' ? (
            <PageBody width="default">
              <Progress label={copy.scanning} done={scan?.filesDone ?? 0} total={scan?.filesTotal ?? 0}>
                {scan && scan.filesTotal > 0 ? copy.scanProgress(scan.filesDone, scan.filesTotal) : null}
              </Progress>
            </PageBody>
          ) : scan.state === 'failed' ? (
            <PageBody width="default" innerClassName="flex flex-col items-start gap-3">
              <p className="text-[14.5px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">{copy.scanFailed}</p>
              {scan.error && <p className="text-[13.5px] [overflow-wrap:anywhere]">{scan.error}</p>}
              <Button type="button" variant="secondary" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                <RotateCwIcon aria-hidden="true" strokeWidth={2.4} />
                {copy.retry}
              </Button>
            </PageBody>
          ) : scan.folders.length === 0 ? (
            <PageBody width="default" innerClassName="flex flex-col items-start gap-3">
              <p className="text-[14.5px] font-semibold [[data-skin=plate]_&]:font-black">{since === 'recent' ? copy.nothingRecent : copy.nothing}</p>
            </PageBody>
          ) : (
            <>
              {importError !== null && <ErrorAlert error={importError} className="mx-4 mt-3 w-auto sm:mx-6" />}
              <FoundFolders
                key={scan.operationId}
                scan={scan}
                projectId={projectId}
                starting={starting}
                selection={activeSelection}
                onSelectionChange={setSelection}
                onImport={importNow}
              />
            </>
          )}
        </div>
      </Sheet>
    </PageFrame>
  )
}
