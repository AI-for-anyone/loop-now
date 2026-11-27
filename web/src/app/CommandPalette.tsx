import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { Dialog as DialogPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { useAgents } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { AgentAvatar } from '@/components/AgentAvatar'
import { NavIcon, type NavIconName } from '@/components/NavIcon'
import { t } from '@/i18n'
import { agentPath, GLOBAL, lastScope, overviewPath, PLATFORM, projectBase, scopeKey, type ResolvedShellScope, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'

const text = t.shell

type PaletteItem = {
  id: string
  group: 'projects' | 'agents' | 'pages'
  label: string
  meta?: string
  keywords: string[]
  icon?: NavIconName
  agentId?: string
  isCurrent?: boolean
  select: () => void
}

function matchesQuery(item: PaletteItem, keyword: string): boolean {
  if (keyword === '') return true
  return item.label.toLowerCase().includes(keyword) || item.keywords.some((entry) => entry.toLowerCase().includes(keyword))
}

function CommandPaletteBody({
  scope,
  projects,
  onClose,
}: {
  scope: ResolvedShellScope
  projects: Schemas['Project'][] | undefined
  onClose: () => void
}) {
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listId = useId()
  const groupIds = {
    projects: `${listId}-group-projects`,
    agents: `${listId}-group-agents`,
    pages: `${listId}-group-pages`,
  }

  const currentScope = useMemo<Scope | null>(
    () => (scope.kind === 'global' ? { kind: 'platform' } : scope.project ? { kind: 'project', project: scope.project } : null),
    [scope],
  )
  const agents = useAgents(currentScope ? scopeKey(currentScope) : null)

  const allItems = useMemo<PaletteItem[]>(() => {
    const items: PaletteItem[] = []

    const go = (to: string, before?: () => void) => {
      before?.()
      onClose()
      void navigate(to)
    }

    // Projects (plus Global scope switch)
    for (const project of projects ?? []) {
      const isCurrent = scope.kind === 'project' && scope.project?.id === project.id
      items.push({
        id: `project:${project.id}`,
        group: 'projects',
        label: project.name,
        meta: isCurrent ? text.palette.hints.current : t.projects.switcher.agentCount(project.agentCount),
        keywords: project.isDefault ? [t.projects.switcher.isDefault] : [],
        icon: 'folder',
        isCurrent,
        select: () => go(overviewPath({ kind: 'project', name: project.name }), () => lastScope.set(project.id)),
      })
    }
    items.push({
      id: 'project:global',
      group: 'projects',
      label: text.global,
      meta: scope.kind === 'global' ? text.palette.hints.current : undefined,
      keywords: [],
      icon: 'settings',
      isCurrent: scope.kind === 'global',
      select: () => go(overviewPath(GLOBAL), () => lastScope.set(PLATFORM)),
    })

    // Agents in the current scope
    if (currentScope) {
      for (const agent of agents.data?.agents ?? []) {
        const address = 'address' in agent && typeof agent.address === 'string' ? agent.address : undefined
        items.push({
          id: `agent:${agent.id}`,
          group: 'agents',
          label: agent.name,
          meta: agent.busy ? text.chips.busy : agent.isManager ? t.agents.manager : address,
          keywords: address ? [address] : [],
          agentId: agent.id,
          select: () => go(agentPath(currentScope, agent.id, 'chat')),
        })
      }
    }

    // Navigation pages
    const pages: { id: string; label: string; to: string; icon: NavIconName; keywords?: string[] }[] = [
      { id: 'page:overview', label: text.items.overview, to: overviewPath(scope), icon: 'overview' },
      ...(scope.kind === 'project'
        ? [
            { id: 'page:tasks', label: text.items.tasks, to: tasksPath(scope.name), icon: 'tasks' as const },
            { id: 'page:folder', label: text.items.folder, to: `${projectBase(scope.name)}/folder`, icon: 'folder' as const },
            { id: 'page:project-plugins', label: t.plugins.project.title, to: `${projectBase(scope.name)}/plugins`, icon: 'plugins' as const },
            { id: 'page:project-settings', label: text.items.projectSettings, to: `${projectBase(scope.name)}/settings`, icon: 'projectSettings' as const },
          ]
        : []),
      { id: 'page:import', label: t.importing.title, to: '/import', icon: 'folder', keywords: [t.importing.cli.claude, t.importing.cli.codex, t.importing.entry.open] },
      { id: 'page:plugins', label: text.items.plugins, to: '/plugins', icon: 'plugins' },
      { id: 'page:skills', label: text.items.skills, to: '/skills', icon: 'skills' },
      { id: 'page:stats', label: text.items.stats, to: '/stats', icon: 'stats' },
      { id: 'page:identities', label: text.items.identities, to: '/identities', icon: 'identities' },
      { id: 'page:settings', label: text.items.settings, to: '/settings', icon: 'settings' },
    ]
    for (const page of pages) {
      items.push({
        id: page.id,
        group: 'pages',
        label: page.label,
        keywords: page.keywords ?? [],
        icon: page.icon,
        select: () => go(page.to),
      })
    }

    return items
  }, [agents.data?.agents, currentScope, navigate, onClose, projects, scope])

  const keyword = query.trim().toLowerCase()
  const filtered = useMemo(() => allItems.filter((item) => matchesQuery(item, keyword)), [allItems, keyword])
  const activeIndex = filtered.length === 0 ? -1 : Math.min(active, filtered.length - 1)
  const optionId = (index: number) => `${listId}-opt-${index}`

  useEffect(() => {
    if (activeIndex < 0) return
    document.getElementById(`${listId}-opt-${activeIndex}`)?.scrollIntoView?.({ block: 'nearest' })
  }, [activeIndex, listId])

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      if (filtered.length > 0) setActive((index) => Math.min(index + 1, filtered.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (filtered.length > 0) setActive((index) => Math.max(index - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      if (activeIndex >= 0) filtered[activeIndex]?.select()
    }
  }

  const groups: { key: 'projects' | 'agents' | 'pages'; heading: string; entries: { item: PaletteItem; index: number }[] }[] = [
    { key: 'projects', heading: text.palette.groups.projects, entries: [] },
    { key: 'agents', heading: text.palette.groups.agents, entries: [] },
    { key: 'pages', heading: text.palette.groups.pages, entries: [] },
  ]
  const byKey = new Map(groups.map((group) => [group.key, group]))
  filtered.forEach((item, index) => {
    byKey.get(item.group)?.entries.push({ item, index })
  })
  const visibleGroups = groups.filter((group) => group.entries.length > 0)

  return (
    <DialogPrimitive.Content
      aria-describedby={undefined}
      onOpenAutoFocus={(event) => {
        event.preventDefault()
        inputRef.current?.focus()
      }}
      className="fixed top-[14vh] left-1/2 z-50 flex w-[min(560px,calc(100vw-32px))] -translate-x-1/2 flex-col overflow-hidden rounded-dialog border border-hairline-strong bg-surface-elevated text-ink shadow-dialog outline-none"
    >
      <DialogPrimitive.Title className="sr-only">{text.palette.title}</DialogPrimitive.Title>
      <div className="flex items-center gap-2.5 border-b border-hairline px-4 py-3">
        <NavIcon name="search" className="size-4 text-ink-500" />
        <input
          ref={inputRef}
          type="search"
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          aria-controls={listId}
          aria-activedescendant={activeIndex >= 0 ? optionId(activeIndex) : undefined}
          aria-label={text.palette.placeholder}
          placeholder={text.palette.placeholder}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value)
            setActive(0)
          }}
          onKeyDown={onKeyDown}
          className="min-w-0 flex-1 bg-transparent text-[14.5px] text-ink outline-none placeholder:text-ink-500"
        />
        <kbd
          aria-hidden="true"
          className="shrink-0 rounded-stamp border border-hairline bg-surface-subtle px-1.5 py-0.5 font-mono text-[11px] leading-none text-ink-500"
        >
          Esc
        </kbd>
      </div>

      <div id={listId} role="listbox" aria-label={text.palette.title} className="riso-scrollbar max-h-[min(380px,55vh)] overflow-y-auto p-2">
        {visibleGroups.length === 0 ? (
          <p role="status" className="px-3 py-8 text-center text-[13.5px] text-ink-500">
            {text.palette.empty}
          </p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {visibleGroups.map((group) => (
              <div key={group.key} role="group" aria-labelledby={groupIds[group.key]} className="flex flex-col gap-0.5">
                <div
                  id={groupIds[group.key]}
                  className="px-2.5 pt-1.5 pb-1 text-[11px] font-semibold tracking-[0.06em] text-ink-500 uppercase"
                >
                  {group.heading}
                </div>
                {group.entries.map(({ item, index }) => {
                  const isActive = index === activeIndex
                  return (
                    <div
                      key={item.id}
                      id={optionId(index)}
                      role="option"
                      aria-selected={isActive}
                      onMouseDown={(event) => event.preventDefault()}
                      onMouseEnter={() => setActive(index)}
                      onClick={() => item.select()}
                      className={cn(
                        'flex min-h-[38px] cursor-pointer items-center gap-2.5 rounded-ticket px-2.5 py-1.5 text-[13.5px] transition-colors',
                        isActive
                          ? 'bg-surface-active font-medium text-ink'
                          : 'text-ink-700 hover:bg-surface-hover hover:text-ink',
                      )}
                    >
                      {item.agentId ? (
                        <AgentAvatar size="sm" id={item.agentId} />
                      ) : (
                        item.icon && <NavIcon name={item.icon} className="size-4 shrink-0 text-ink-500" />
                      )}
                      <span className="min-w-0 flex-1 truncate">{item.label}</span>
                      {item.meta && (
                        <span className="max-w-[40%] shrink-0 truncate text-[12px] text-ink-500">
                          {item.meta}
                        </span>
                      )}
                    </div>
                  )
                })}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="flex items-center gap-4 border-t border-hairline bg-surface-subtle px-4 py-2 text-[11.5px] text-ink-500">
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-stamp border border-hairline bg-surface-control px-1 font-mono text-[10.5px]">↑↓</kbd>
          {text.palette.hints.navigate}
        </span>
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-stamp border border-hairline bg-surface-control px-1 font-mono text-[10.5px]">↵</kbd>
          {text.palette.hints.select}
        </span>
        <span className="inline-flex items-center gap-1">
          <kbd className="rounded-stamp border border-hairline bg-surface-control px-1 font-mono text-[10.5px]">Esc</kbd>
          {text.palette.hints.close}
        </span>
      </div>
    </DialogPrimitive.Content>
  )
}

// Command palette opened with ⌘K / Ctrl+K (or the sidebar shortcut button).
// Searches projects, Agents in the current scope, and navigation pages with full keyboard navigation.
export function CommandPalette({
  open,
  onOpenChange,
  scope,
  projects,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  scope: ResolvedShellScope
  projects: Schemas['Project'][] | undefined
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/45 backdrop-blur-[1px]" />
        {open && <CommandPaletteBody scope={scope} projects={projects} onClose={() => onOpenChange(false)} />}
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}
