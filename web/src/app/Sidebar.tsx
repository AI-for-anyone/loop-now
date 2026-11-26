import { useId, useState, type ReactNode, type Ref } from 'react'
import { Link, useLocation } from 'react-router'
import { ChevronRight, PanelLeftClose, Settings } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useInboxPending, type PendingCounts } from '@/api/console-inbox'
import { tasksPath, useProjectTaskCounts } from '@/api/tasks'
import { RunningTaskBadge } from '@/features/tasks/RunningTaskBadge'
import { BrandWordmark } from '@/components/BrandWordmark'
import { NavIcon, type NavIconName } from '@/components/NavIcon'
import { AgentNavGroup } from '@/features/agents/AgentList'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import { ProjectAvatar, ScopeSwitcher } from '@/features/projects/ScopeSwitcher'
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
      <span className="min-w-0 flex-1 truncate">{label}</span>
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

// The “项目” (Projects) group in the global scope: clicking a project switches to its scope (opens its overview).
function ProjectGroup({ projects, search }: { projects: Schemas['Project'][] | undefined; search: string }) {
  const headingId = useId()
  const keyword = search.trim().toLowerCase()
  const shown = (projects ?? []).filter((p) => p.name.toLowerCase().includes(keyword))
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-1">
      <h2 id={headingId} className={navGroupClass}>
        {text.groups.projects}
      </h2>
      {!projects && <p className="px-2.5 text-[12.5px] text-sidebar-muted">{t.common.loading}</p>}
      {projects?.length === 0 && <p className="px-2.5 text-[12.5px] text-sidebar-muted">{text.noProjects}</p>}
      {projects && projects.length > 0 && shown.length === 0 && (
        <p role="status" className="px-2.5 text-[12.5px] text-sidebar-muted">
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
              <span className="min-w-0 flex-1 truncate">{project.name}</span>
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

// Decoration in the empty space below the list in Plate skin: a halftone-dot circle sticking out past the left edge, with two small dots
// beside it, one pink and one yellow. Hidden in mono skin and whenever the remaining height cannot fit the whole decoration.
function SidebarDeco() {
  return (
    <div className="sidebar-deco-slot hidden [[data-skin=plate]_&]:block">
      <div aria-hidden="true" data-sidebar-deco className="sidebar-deco pointer-events-none absolute bottom-3 left-0 h-[178px] w-full max-nav:hidden">
        <span className="absolute bottom-0 -left-[72px] size-[178px] rounded-full bg-[radial-gradient(rgb(255_255_255/.36)_2.2px,transparent_2.6px)] bg-size-[11px_11px]" />
        <span className="absolute bottom-[126px] left-[108px] size-[34px] rounded-full border-2 border-ink bg-riso-pink" />
        <span className="absolute bottom-[108px] left-[132px] size-5 rounded-full border-2 border-ink bg-riso-yellow" />
      </div>
    </div>
  )
}

// The sidebar: semantic chrome tokens in mono, brand-blue halftone in Plate.
// - Top: 48px header bar (on macOS inset title bar, collapse toggle + brand sit directly right of traffic lights so the toggle never shifts
//   when collapsing/expanding) + compact search / ⌘K bar.
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
      title={`${text.collapseNav} (⌘B)`}
      onClick={onCollapse}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-ticket text-sidebar-muted transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus [[data-skin=plate]_&]:text-white/85 [[data-skin=plate]_&]:hover:bg-white/15 [[data-skin=plate]_&]:hover:text-white"
    >
      <PanelLeftClose className="size-4" strokeWidth={1.8} />
    </button>
  )

  const closeButton = onClose && (
    <button
      type="button"
      aria-label={text.closeMenu}
      onClick={onClose}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-ticket border border-sidebar-border bg-surface-control text-sidebar-fg outline-none hover:bg-sidebar-hover focus-visible:shadow-focus [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink"
    >
      <NavIcon name="close" className="size-3.5 [stroke-width:2.6]" />
    </button>
  )

  return (
    <nav
      aria-label={text.nav}
      className="riso-halftone flex h-full w-full flex-col border-r border-sidebar-border bg-sidebar-bg text-sidebar-fg [[data-skin=plate]_&]:border-r-0"
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

      {/* Compact search / ⌘K trigger */}
      <div className="shrink-0 px-3 pb-2.5">
        <div className="flex h-[32px] items-center gap-2 rounded-ticket border border-sidebar-border bg-surface-control px-2.5 text-sidebar-fg transition-colors focus-within:border-ink/30 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.42] [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:focus-within:shadow-focus">
          <NavIcon name="search" className="size-3.5 text-sidebar-muted [[data-skin=plate]_&]:text-white" />
          <input
            ref={searchRef}
            type="search"
            aria-label={searchLabel}
            placeholder={text.search.placeholder}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="min-w-0 flex-1 bg-transparent text-[13px] text-sidebar-fg outline-none placeholder:text-sidebar-muted [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:placeholder:text-white/80"
          />
          {onOpenPalette ? (
            <button
              type="button"
              aria-label={text.palette.open}
              title={`${text.palette.title} (${text.search.shortcut})`}
              onClick={onOpenPalette}
              className="shrink-0 rounded-stamp border border-sidebar-border bg-sidebar-hover px-[5px] font-sans text-[11px] leading-4 text-sidebar-muted transition-colors outline-none hover:text-sidebar-fg focus-visible:shadow-focus [[data-skin=plate]_&]:border-white/45 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:text-white"
            >
              <kbd className="font-sans">{text.search.shortcut}</kbd>
            </button>
          ) : (
            <kbd
              aria-hidden="true"
              className="shrink-0 rounded-stamp border border-sidebar-border bg-sidebar-hover px-[5px] font-sans text-[11px] leading-4 text-sidebar-muted [[data-skin=plate]_&]:border-white/45 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:text-white"
            >
              {text.search.shortcut}
            </kbd>
          )}
        </div>
      </div>

      {/* Scrollable main navigation: current scope entries, Agents, and collapsible Platform group. The container is positioned so sr-only
          descriptions inside "needs you" badges scroll with the list instead of stretching the page. */}
      <div className="relative flex min-h-0 flex-1 flex-col gap-0.5 overflow-x-hidden overflow-y-auto px-3 pt-0.5 pb-3 [scrollbar-color:var(--hairline-strong)_transparent] [scrollbar-width:thin] [[data-skin=plate]_&]:[scrollbar-color:rgb(255_255_255/.45)_transparent]">
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
                // The running count comes first and the “待你回复” (Needs your reply) sticker after it; when neither is present the container
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

        <SidebarDeco />

        {/* Collapsible Platform section pinned to the bottom of the scroll area, expanded by default. */}
        <section className="mt-auto pt-3">
          <div className="border-t border-sidebar-border pt-2 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-white/30">
            <button
              type="button"
              aria-expanded={platformExpanded}
              aria-controls={platformPanelId}
              onClick={togglePlatform}
              className="flex w-full items-center gap-1.5 rounded-ticket px-2 py-1.5 text-[11px] font-medium tracking-wider text-sidebar-group uppercase transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus [[data-skin=plate]_&]:text-[11.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white/85 [[data-skin=plate]_&]:hover:bg-white/12 [[data-skin=plate]_&]:hover:text-white"
            >
              <ChevronRight
                className={cn('size-3.5 shrink-0 transition-transform duration-150', platformExpanded && 'rotate-90')}
                strokeWidth={2}
              />
              <span className="flex-1 truncate text-left">{text.groups.platform}</span>
              {!platformExpanded && (
                <span className="text-[10.5px] font-normal text-sidebar-muted tabular-nums [[data-skin=plate]_&]:text-white/75">
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
      <div className="flex shrink-0 flex-col gap-1.5 border-t border-sidebar-border px-3 py-2.5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-white/35">
        <ConnectionStatus compact className="w-full" />
        <div className="flex items-center gap-1.5">
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
            title={`${text.items.settings} (${text.settingsShortcut})`}
            className={cn(
              'inline-flex size-8 shrink-0 items-center justify-center rounded-ticket border-0 transition-colors outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:size-10 [[data-skin=plate]_&]:border',
              here.global === 'settings'
                ? 'bg-sidebar-active-bg text-sidebar-active-fg [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]'
                : 'bg-transparent text-sidebar-muted hover:bg-sidebar-hover hover:text-sidebar-fg [[data-skin=plate]_&]:border-white/30 [[data-skin=plate]_&]:bg-white/10 [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:hover:bg-white/18',
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
