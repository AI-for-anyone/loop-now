import { useId, useState, type ReactNode, type Ref } from 'react'
import { Link, useLocation } from 'react-router'
import { ChevronRight, PanelLeftClose, Settings } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useInboxPending, type PendingCounts } from '@/api/console-inbox'
import { tasksPath, useProjectTaskCounts } from '@/api/tasks'
import { BrandWordmark } from '@/components/BrandWordmark'
import { NavIcon, type NavIconName } from '@/components/NavIcon'
import { AgentNavGroup } from '@/features/agents/AgentList'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { mac } from '@/features/chat/composer-style'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import { ProjectAvatar, ScopeSwitcher } from '@/features/projects/ScopeSwitcher'
import { RunningTaskBadge } from '@/features/tasks/RunningTaskBadge'
import { t } from '@/i18n'
import { navGroupClass, navRowClass } from '@/lib/nav-row'
import { lastScope, overviewPath, projectBase, type ResolvedShellScope, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { ConnectionStatus } from './ConnectionStatus'
import { useConnectionStatus } from './connection-status'

const text = t.shell

export const SIDEBAR_PLATFORM_OPEN_KEY = 'loop-now.sidebar-platform-open'

const PLATFORM_ROUTES = new Set(['plugins', 'skills', 'stats', 'identities'])

const platformEntries: readonly { to: string; key: string; icon: NavIconName; label: string }[] = [
  { to: '/plugins', key: 'plugins', icon: 'plugins', label: text.items.plugins },
  { to: '/skills', key: 'skills', icon: 'skills', label: text.items.skills },
  { to: '/stats', key: 'stats', icon: 'stats', label: text.items.stats },
  { to: '/identities', key: 'identities', icon: 'identities', label: text.items.identities },
]

function readPlatformOpen(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_PLATFORM_OPEN_KEY) !== 'false'
  } catch {
    return true
  }
}

function writePlatformOpen(open: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_PLATFORM_OPEN_KEY, String(open))
  } catch {
    // When browser storage is unavailable, disclosure state still works for the current session.
  }
}

function NavItem({ to, icon, label, current, badge, onClick, compact = false }: { to: string; icon: NavIconName; label: string; current: boolean; badge?: ReactNode; onClick?: () => void; compact?: boolean }) {
  return (
    <Link to={to} onClick={onClick} aria-current={current ? 'page' : undefined} className={navRowClass(current, compact ? 'compact' : 'nav')}>
      <NavIcon name={icon} />
      <span title={label} className="min-w-0 flex-1 truncate">{label}</span>
      {badge}
    </Link>
  )
}

// Which entry the URL falls on: for project and platform URLs, the first segment after the scope plus the Agent ID; otherwise the first
// segment.
function place(pathname: string) {
  const parts = pathname.split('/').filter(Boolean)
  const scoped = parts[0] === 'p' ? parts.slice(2) : parts[0] === 'platform' ? parts.slice(1) : null
  return {
    overview: pathname === '/',
    section: scoped ? (scoped[0] ?? null) : null,
    agentId: scoped?.[0] === 'agents' ? (scoped[1] ?? null) : null,
    global: scoped ? null : (parts[0] ?? null),
  }
}

// The Projects group in the global scope: clicking a project switches to its scope (opens its overview).
function ProjectGroup({ projects, search }: { projects: Schemas['Project'][] | undefined; search: string }) {
  const headingId = useId()
  const keyword = search.trim().toLowerCase()
  const shown = (projects ?? []).filter((p) => p.name.toLowerCase().includes(keyword))
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-1">
      <h2 id={headingId} className={navGroupClass}>
        <span title={text.groups.projects} className="min-w-0 truncate">
          {text.groups.projects}
        </span>
      </h2>
      {!projects && <p className="px-2.5 text-[13.5px] text-sidebar-muted">{t.common.loading}</p>}
      {projects?.length === 0 && <p className="px-2.5 text-[13.5px] text-sidebar-muted">{text.noProjects}</p>}
      {projects && projects.length > 0 && shown.length === 0 && (
        <p role="status" className="px-2.5 text-[13.5px] text-sidebar-muted">
          {text.scope.noMatch}
        </p>
      )}
      <ul aria-labelledby={headingId} className="flex flex-col gap-0.5">
        {shown.map((project) => (
          <li key={project.id} className="min-w-0">
            <Link
              to={overviewPath({ kind: 'project', name: project.name })}
              onClick={() => lastScope.set(project.id)}
              className={navRowClass(false, 'nav')}
            >
              <ProjectAvatar id={project.id} name={project.name} size="xs" />
              <span title={project.name} className="min-w-0 flex-1 truncate">{project.name}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}

const sum = (rows: PendingCounts[]): PendingCounts => ({
  questions: rows.reduce((total, row) => total + row.questions, 0),
  unread: rows.reduce((total, row) => total + row.unread, 0),
})

// The sidebar: themed via semantic sidebar tokens.
// - Top: 48px header bar (on macOS inset title bar, collapse toggle + brand sit directly right of traffic lights) + compact filter / ⌘K bar.
// - Middle (scrollable): current scope entries, Agents list, and a collapsible Platform group (Plugins, Skills, Stats, Identities).
// - Bottom (fixed single-row dock): upward-opening ScopeSwitcher (carrying the live connection dot) and Settings button,
//   plus an expanded connection status pill whenever degraded or unreachable.
export function Sidebar({
  scope,
  projects,
  projectsError,
  searchRef,
  onClose,
  onCollapse,
  onOpenPalette,
}: {
  scope: ResolvedShellScope
  projects: Schemas['Project'][] | undefined
  projectsError?: unknown
  searchRef?: Ref<HTMLInputElement>
  onClose?: () => void
  onCollapse?: () => void
  onOpenPalette?: () => void
}) {
  const { pathname } = useLocation()
  const connection = useConnectionStatus()
  const here = place(pathname)
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState<Scope | null>(null)
  const [platformOpen, setPlatformOpen] = useState(readPlatformOpen)
  const platformPanelId = useId()
  const pending = useInboxPending().data
  const project = scope.kind === 'project' ? scope.project : undefined
  // The number of items that need you: in a project, counted by the project of the sending Agent; in global, all of them.
  const mine = (item: Schemas['InboxPendingItem']) => scope.kind === 'global' || (project !== undefined && item.projectId === project.id)
  const overviewCounts = pending && { questions: pending.questions.filter(mine).length, unread: pending.unread.filter(mine).length }
  const taskCounts = pending && project && sum(pending.byTask.filter((row) => row.projectId === project.id))
  // The running task count only counts running; read only in project scope.
  const running = useProjectTaskCounts(project?.id).data?.running
  const searchLabel = scope.kind === 'project' ? text.search.project : text.search.global
  const desktop = hasInsetTitleBar()
  const paletteShortcut = text.paletteShortcut(mac)

  const keyword = search.trim().toLowerCase()
  const matchingPlatform = keyword ? platformEntries.filter((entry) => entry.label.toLowerCase().includes(keyword)) : platformEntries
  const inPlatform = here.global !== null && PLATFORM_ROUTES.has(here.global)
  const platformExpanded = platformOpen || inPlatform || (keyword.length > 0 && matchingPlatform.length > 0)

  const togglePlatform = () => {
    setPlatformOpen((prev) => {
      const next = !prev
      writePlatformOpen(next)
      return next
    })
  }

  const brandLink = (
    <Link
      to="/"
      className="truncate rounded-sm outline-none focus-visible:shadow-focus"
    >
      <BrandWordmark />
    </Link>
  )

  const collapseButton = onCollapse && (
    <button
      type="button"
      aria-label={text.collapseNav}
      title={`${text.collapseNav} (${text.sidebarShortcut(mac)})`}
      onClick={onCollapse}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-ticket text-sidebar-muted transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus"
    >
      <PanelLeftClose className="size-4" strokeWidth={1.8} />
    </button>
  )

  const closeButton = onClose && (
    <button
      type="button"
      aria-label={text.closeMenu}
      onClick={onClose}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-ticket border border-sidebar-border bg-sidebar-control-bg text-sidebar-control-fg outline-none hover:bg-sidebar-hover focus-visible:shadow-focus"
    >
      <NavIcon name="close" className="size-3.5 [stroke-width:2.6]" />
    </button>
  )

  return (
    <nav
      aria-label={text.nav}
      className="flex h-full w-full flex-col border-r border-sidebar-border bg-sidebar-bg text-sidebar-fg"
    >
      {/* Top 48px bar: on macOS inset title bar, place the collapse/close toggle + brand immediately to the right of the traffic lights. */}
      <div
        className={cn(
          'flex h-12 shrink-0 items-center gap-2',
          desktop ? 'window-drag pr-3 pl-[84px]' : 'justify-between px-3.5',
        )}
      >
        {desktop ? (
          <>
            {collapseButton}
            {closeButton}
            {brandLink}
          </>
        ) : (
          <>
            {brandLink}
            {collapseButton}
            {closeButton}
          </>
        )}
      </div>

      {/* Compact inline sidebar filter with quick command-palette trigger */}
      <div className="shrink-0 px-3 pb-2.5">
        <div className="flex h-[34px] items-center gap-2 rounded-ticket border border-sidebar-border bg-sidebar-control-bg px-2.5 text-sidebar-control-fg transition-colors focus-within:border-hairline-strong">
          <NavIcon name="search" className="size-3.5 shrink-0 text-sidebar-muted" />
          <input
            ref={searchRef}
            type="search"
            aria-label={searchLabel}
            placeholder={text.search.placeholder}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="min-w-0 flex-1 bg-transparent text-[14px] text-sidebar-control-fg outline-none placeholder:text-sidebar-muted"
          />
          {onOpenPalette ? (
            <button
              type="button"
              aria-label={text.palette.open}
              title={`${text.palette.title} (${paletteShortcut})`}
              onClick={onOpenPalette}
              className="shrink-0 cursor-pointer rounded-stamp border border-sidebar-border bg-sidebar-hover px-[5px] font-sans text-[11.5px] leading-4 text-sidebar-muted transition-colors outline-none hover:text-sidebar-fg focus-visible:shadow-focus"
            >
              <kbd className="font-sans">{paletteShortcut}</kbd>
            </button>
          ) : (
            <kbd
              aria-hidden="true"
              className="shrink-0 rounded-stamp border border-sidebar-border bg-sidebar-hover px-[5px] font-sans text-[11.5px] leading-4 text-sidebar-muted"
            >
              {paletteShortcut}
            </kbd>
          )}
        </div>
      </div>

      {/* Scrollable main navigation: current scope entries, Agents, and collapsible Platform group. The container is positioned so sr-only
          descriptions inside "needs you" badges scroll with the list instead of stretching the page. */}
      <div className="relative flex min-h-0 flex-1 flex-col gap-0.5 overflow-x-hidden overflow-y-auto px-3 pt-0.5 pb-3 [scrollbar-color:var(--hairline-strong)_transparent] [scrollbar-width:thin]">
        <NavItem
          to={overviewPath(scope)}
          icon="overview"
          label={text.items.overview}
          current={here.overview}
          badge={<PendingBadge sticker counts={overviewCounts} />}
        />
        {scope.kind === 'project' ? (
          <>
            <NavItem
              to={tasksPath(scope.name)}
              icon="tasks"
              label={text.items.tasks}
              current={here.section === 'tasks'}
              badge={
                // The running count comes first and the "Needs your reply" sticker after it; when neither is present the container
                // takes no space.
                <span className="flex shrink-0 items-center gap-1.5 empty:hidden">
                  <RunningTaskBadge count={running} />
                  <PendingBadge sticker counts={taskCounts} />
                </span>
              }
            />
            <NavItem to={`${projectBase(scope.name)}/folder`} icon="folder" label={text.items.folder} current={here.section === 'folder'} />
            <NavItem
              to={`${projectBase(scope.name)}/settings`}
              icon="projectSettings"
              label={text.items.projectSettings}
              current={here.section === 'settings' || here.section === 'plugins'}
            />
            {/* No Agent group when the project in the URL cannot be found (the main area says so); while the project list has not loaded yet,
                the group shows loading. */}
            {(project || !projects) && (
              <AgentNavGroup
                scope={project ? { kind: 'project', project } : null}
                title={text.groups.agents}
                search={search}
                currentAgentId={here.agentId}
                onCreate={() => project && setCreating({ kind: 'project', project })}
                listError={projectsError}
              />
            )}
          </>
        ) : (
          <>
            <ProjectGroup projects={projects} search={search} />
            <AgentNavGroup
              scope={{ kind: 'platform' }}
              title={text.groups.platformAgents}
              search={search}
              currentAgentId={here.agentId}
              onCreate={() => setCreating({ kind: 'platform' })}
            />
          </>
        )}

        {/* Collapsible Platform section pinned to the bottom of the scroll area, expanded by default. */}
        <section className="mt-auto pt-3">
          <div className="border-t border-sidebar-border pt-2">
            <button
              type="button"
              aria-expanded={platformExpanded}
              aria-controls={platformPanelId}
              onClick={togglePlatform}
              className="flex w-full items-center gap-1.5 rounded-ticket px-2 py-1.5 text-[12px] font-medium tracking-wider text-sidebar-group uppercase transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus"
            >
              <ChevronRight
                className={cn('size-3.5 shrink-0 transition-transform duration-150', platformExpanded && 'rotate-90')}
                strokeWidth={2}
              />
              <span className="flex-1 truncate text-left">{text.groups.platform}</span>
              {!platformExpanded && (
                <span className="text-[11.5px] font-normal text-sidebar-muted tabular-nums">
                  {platformEntries.length}
                </span>
              )}
            </button>
            {platformExpanded && (
              <div id={platformPanelId} className="mt-0.5 flex flex-col gap-0.5">
                {matchingPlatform.map((entry) => (
                  <NavItem
                    key={entry.key}
                    compact
                    to={entry.to}
                    icon={entry.icon}
                    label={entry.label}
                    current={here.global === entry.key}
                  />
                ))}
              </div>
            )}
          </div>
        </section>
      </div>

      {/* Fixed single-row bottom dock: upward-opening scope switcher (with live connection dot) + Settings gear. */}
      <div className="flex min-w-0 shrink-0 flex-col gap-1.5 border-t border-sidebar-border px-3 py-2.5">
        <ConnectionStatus compact className="w-full" />
        <div className="flex min-w-0 items-center gap-1.5">
          <ScopeSwitcher
            scope={scope}
            projects={projects}
            side="top"
            className="min-w-0 flex-1"
            statusDotClass={connection.style.dot}
            statusTitle={`${connection.style.label} · ${connection.host}`}
          />
          <Link
            to="/settings"
            aria-current={here.global === 'settings' ? 'page' : undefined}
            aria-label={text.items.settings}
            title={`${text.items.settings} (${text.settingsShortcut(mac)})`}
            className={cn(
              'inline-flex size-8 shrink-0 items-center justify-center rounded-ticket border-0 transition-colors outline-none focus-visible:shadow-focus',
              here.global === 'settings'
                ? 'bg-sidebar-active-bg text-sidebar-active-fg shadow-surface'
                : 'bg-transparent text-sidebar-muted hover:bg-sidebar-hover hover:text-sidebar-fg',
            )}
          >
            <Settings className="size-4" strokeWidth={1.8} />
            <span className="sr-only">{text.items.settings}</span>
          </Link>
        </div>
      </div>
      {creating && <CreateAgentDialog scope={creating} onClose={() => setCreating(null)} />}
    </nav>
  )
}
