import { useId, useState, type ReactNode, type Ref } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'
import { Moon, PanelLeftClose, Sun } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useInboxPending, type PendingCounts } from '@/api/console-inbox'
import { tasksPath, useProjectTaskCounts } from '@/api/tasks'
import { RunningTaskBadge } from '@/features/tasks/RunningTaskBadge'
import { NavIcon, type NavIconName } from '@/components/NavIcon'
import { AgentNavGroup } from '@/features/agents/AgentList'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import { ScopeSwitcher } from '@/features/projects/ScopeSwitcher'
import { t } from '@/i18n'
import { navGroupClass, navRowClass } from '@/lib/nav-row'
import { lastScope, overviewPath, projectBase, type ResolvedShellScope, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'
import { ConnectionStatus } from './ConnectionStatus'

const text = t.shell

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
            <NavItem
              to={overviewPath({ kind: 'project', name: project.name })}
              icon="folder"
              label={project.name}
              current={false}
              onClick={() => lastScope.set(project.id)}
            />
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

// Decoration in the empty space below the list: a halftone-dot circle sticking out past the left edge, with two small dots beside it, one
// pink and one yellow. It takes up the remaining height below the list and is hidden when that height cannot fit the whole decoration (see
// .sidebar-deco in index.css), so it never covers text; screen readers skip it and it does not block clicks. Not shown in the drawer on
// narrow screens.
function SidebarDeco() {
  return (
    <div className="sidebar-deco-slot">
      <div aria-hidden="true" data-sidebar-deco className="sidebar-deco pointer-events-none absolute bottom-3 left-0 h-[178px] w-full max-nav:hidden">
        <span className="absolute bottom-0 -left-[72px] size-[178px] rounded-full bg-[radial-gradient(rgb(255_255_255/.36)_2.2px,transparent_2.6px)] bg-size-[11px_11px]" />
        <span className="absolute bottom-[126px] left-[108px] size-[34px] rounded-full border-2 border-ink bg-riso-pink" />
        <span className="absolute bottom-[108px] left-[132px] size-5 rounded-full border-2 border-ink bg-riso-yellow" />
      </div>
    </div>
  )
}

// The sidebar: semantic chrome tokens in mono, brand-blue halftone in Plate. At the top: the logo, collapse/close control, the scope
// switcher and search; in the middle: the current scope's entries and Agents; at the bottom: the global entries reachable from any scope,
// and the connection status. Always visible on wide screens (unless collapsed), in a drawer on narrow ones (onClose is the drawer's close
// button). In a desktop shell window with an inset title bar, there is an extra strip at the very top for dragging the window where the
// system's traffic-light buttons sit (see desktop/src/window.ts).
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
  const navigate = useNavigate()
  const { resolvedTheme, supportsDark, setThemeMode } = useTheme()
  const here = place(pathname)
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState<Scope | null>(null)
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

  return (
    <nav
      aria-label={text.nav}
      className="riso-halftone flex h-full w-full flex-col border-r border-sidebar-border bg-sidebar-bg text-sidebar-fg [[data-skin=plate]_&]:border-r-0"
    >
      {desktop && <div aria-hidden="true" className="window-drag h-12 shrink-0" />}
      <div className={cn('flex shrink-0 flex-col gap-3.5 px-3.5 pb-3 [[data-skin=plate]_&]:gap-4', desktop ? 'pt-1' : 'pt-5')}>
        <div className="flex items-center justify-between gap-2 px-1.5">
          <Link
            to="/"
            className="rounded-sm font-display-latin text-[20px] leading-none font-semibold tracking-tight text-sidebar-fg outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:text-[26px] [[data-skin=plate]_&]:font-extrabold [[data-skin=plate]_&]:tracking-[-0.5px] [[data-skin=plate]_&]:[text-shadow:2.5px_2.5px_0_var(--riso-pink)]"
          >
            {t.app.brand}
          </Link>
          {onCollapse && (
            <button
              type="button"
              aria-label={text.collapseNav}
              title={`${text.collapseNav} (⌘B)`}
              onClick={onCollapse}
              className="inline-flex size-7 items-center justify-center rounded-ticket text-sidebar-muted transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus [[data-skin=plate]_&]:text-white/85 [[data-skin=plate]_&]:hover:bg-white/15 [[data-skin=plate]_&]:hover:text-white"
            >
              <PanelLeftClose className="size-4" strokeWidth={1.8} />
            </button>
          )}
          {onClose && (
            <button
              type="button"
              aria-label={text.closeMenu}
              onClick={onClose}
              className="inline-flex size-8 items-center justify-center rounded-ticket border border-sidebar-border bg-surface-control text-sidebar-fg outline-none hover:bg-sidebar-hover focus-visible:shadow-focus [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink"
            >
              <NavIcon name="close" className="size-4 [stroke-width:2.8]" />
            </button>
          )}
        </div>
        <ScopeSwitcher scope={scope} projects={projects} />
        <div className="flex h-[34px] items-center gap-2 rounded-ticket border border-sidebar-border bg-surface-control px-2.5 text-sidebar-fg focus-within:border-hairline-strong focus-within:shadow-focus [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.42] [[data-skin=plate]_&]:text-white">
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

      {/* The scroll area itself is the positioning container: the sr-only description in the "needs you" badge scrolls with the list and is
          clipped, instead of stretching the whole page from its unscrolled position. */}
      <div className="relative flex min-h-0 flex-1 flex-col gap-0.5 overflow-x-hidden overflow-y-auto px-3.5 pt-1 pb-3 [scrollbar-color:var(--hairline-strong)_transparent] [scrollbar-width:thin] [[data-skin=plate]_&]:[scrollbar-color:rgb(255_255_255/.45)_transparent]">
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
      </div>

      <div className="flex shrink-0 flex-col gap-0.5 border-t border-sidebar-border px-3.5 pt-2.5 pb-3.5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-white/35">
        <NavItem compact to="/plugins" icon="plugins" label={text.items.plugins} current={here.global === 'plugins'} />
        <NavItem compact to="/skills" icon="skills" label={text.items.skills} current={here.global === 'skills'} />
        <NavItem compact to="/stats" icon="stats" label={text.items.stats} current={here.global === 'stats'} />
        <NavItem compact to="/identities" icon="identities" label={text.items.identities} current={here.global === 'identities'} />
        <div className="mt-2 flex items-center gap-1.5 border-t border-sidebar-border pt-2.5 [[data-skin=plate]_&]:border-white/25">
          <Link
            to="/settings"
            aria-current={here.global === 'settings' ? 'page' : undefined}
            title={`${text.items.settings} (${text.settingsShortcut})`}
            className={cn(
              'flex h-9 min-w-0 flex-1 items-center gap-2 rounded-ticket border px-2.5 text-[13.5px] font-medium transition-colors outline-none focus-visible:shadow-focus',
              here.global === 'settings'
                ? 'border-hairline-strong bg-sidebar-active-bg font-semibold text-sidebar-active-fg [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)]'
                : 'border-sidebar-border bg-surface-control text-sidebar-fg hover:bg-sidebar-hover [[data-skin=plate]_&]:border-white/30 [[data-skin=plate]_&]:bg-white/10 [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:hover:bg-white/18',
            )}
          >
            <NavIcon name="settings" />
            <span className="min-w-0 flex-1 truncate">{text.items.settings}</span>
            <span
              aria-hidden="true"
              data-shortcut={text.settingsShortcut}
              className="shrink-0 rounded-stamp border border-sidebar-border px-1 font-sans text-[10.5px] leading-4 text-sidebar-muted after:content-[attr(data-shortcut)] [[data-skin=plate]_&]:border-current/35 [[data-skin=plate]_&]:text-current/80"
            />
          </Link>
          <button
            type="button"
            aria-label={resolvedTheme === 'dark' ? text.switchToLight : text.switchToDark}
            title={supportsDark ? (resolvedTheme === 'dark' ? text.switchToLight : text.switchToDark) : t.settings.appearance.title}
            onClick={() => {
              if (!supportsDark) void navigate('/settings#settings-appearance')
              else setThemeMode(resolvedTheme === 'dark' ? 'light' : 'dark')
            }}
            className="inline-flex size-9 shrink-0 items-center justify-center rounded-ticket border border-sidebar-border bg-surface-control text-sidebar-fg transition-colors outline-none hover:bg-sidebar-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-white/30 [[data-skin=plate]_&]:bg-white/10 [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:hover:bg-white/18"
          >
            {resolvedTheme === 'dark' ? <Sun className="size-4" strokeWidth={1.8} /> : <Moon className="size-4" strokeWidth={1.8} />}
          </button>
        </div>
        <ConnectionStatus className="mt-2 ml-1 self-start text-[11.5px] font-medium [&.text-white]:text-sidebar-muted [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:[&.text-white]:text-white" />
      </div>
      {creating && <CreateAgentDialog scope={creating} onClose={() => setCreating(null)} />}
    </nav>
  )
}
