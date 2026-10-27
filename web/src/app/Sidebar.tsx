import { useId, useState, type ReactNode, type Ref } from 'react'
import { Link, useLocation } from 'react-router'
import type { Schemas } from '@/api/client'
import { useInboxPending, type PendingCounts } from '@/api/console-inbox'
import { tasksPath } from '@/api/tasks'
import { NavIcon, type NavIconName } from '@/components/NavIcon'
import { AgentNavGroup } from '@/features/agents/AgentList'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { ScopeSwitcher } from '@/features/projects/ScopeSwitcher'
import { t } from '@/i18n'
import { shellText as text } from '@/i18n/shell'
import { navGroupClass, navRowClass } from '@/lib/nav-row'
import { lastScope, overviewPath, projectBase, type ResolvedShellScope, type Scope } from '@/lib/scope'
import { ConnectionStatus } from './ConnectionStatus'

function NavItem({ to, icon, label, current, badge, onClick }: { to: string; icon: NavIconName; label: string; current: boolean; badge?: ReactNode; onClick?: () => void }) {
  return (
    <Link to={to} onClick={onClick} aria-current={current ? 'page' : undefined} className={navRowClass(current)}>
      <NavIcon name={icon} />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {badge}
    </Link>
  )
}

// 地址落在哪一项：项目与平台的地址取范围之后的第一段与 Agent ID，其余取第一段。
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

// 全局范围的“项目”分组：点一个项目即切换到它的范围（进入它的概览）。
function ProjectGroup({ projects, search }: { projects: Schemas['Project'][] | undefined; search: string }) {
  const headingId = useId()
  const keyword = search.trim().toLowerCase()
  const shown = (projects ?? []).filter((p) => p.name.toLowerCase().includes(keyword))
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-[3px]">
      <h2 id={headingId} className={navGroupClass}>
        {text.groups.projects}
      </h2>
      {!projects && <p className="px-2.5 text-[12.5px] text-white/75">{t.common.loading}</p>}
      {projects?.length === 0 && <p className="px-2.5 text-[12.5px] text-white/75">{text.noProjects}</p>}
      {projects && projects.length > 0 && shown.length === 0 && (
        <p role="status" className="px-2.5 text-[12.5px] text-white/75">
          {text.scope.noMatch}
        </p>
      )}
      <ul aria-labelledby={headingId} className="flex flex-col gap-[3px]">
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

// 左侧栏：品牌蓝网点底、白字。顶部是标志、范围切换与搜索；中间是当前范围的入口与 Agent；
// 底部（虚线以下）是任何范围都能到达的全局入口与连接状态。宽屏常驻，窄屏放在抽屉里（onClose 为抽屉的关闭按钮）。
export function Sidebar({
  scope,
  projects,
  projectsError,
  searchRef,
  onClose,
}: {
  scope: ResolvedShellScope
  projects: Schemas['Project'][] | undefined
  projectsError?: unknown
  searchRef?: Ref<HTMLInputElement>
  onClose?: () => void
}) {
  const { pathname } = useLocation()
  const here = place(pathname)
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState<Scope | null>(null)
  const pending = useInboxPending().data
  const project = scope.kind === 'project' ? scope.project : undefined
  // 待你处理的条数：项目里按发件 Agent 所属项目统计，全局为全部。
  const mine = (item: Schemas['InboxPendingItem']) => scope.kind === 'global' || (project !== undefined && item.projectId === project.id)
  const overviewCounts = pending && { questions: pending.questions.filter(mine).length, unread: pending.unread.filter(mine).length }
  const taskCounts = pending && project && sum(pending.byTask.filter((row) => row.projectId === project.id))
  const searchLabel = scope.kind === 'project' ? text.search.project : text.search.global

  return (
    <nav aria-label={text.nav} className="riso-halftone flex h-full w-full flex-col bg-riso-blue text-white">
      <div className="flex shrink-0 flex-col gap-2.5 px-3 pt-4 pb-2">
        <div className="flex items-center justify-between gap-2 px-1.5">
          <Link
            to="/"
            className="rounded-sm font-display-latin text-[23px] leading-none font-extrabold tracking-[-0.5px] text-white outline-none [text-shadow:2px_2px_0_var(--riso-pink)] focus-visible:shadow-focus"
          >
            {t.app.brand}
          </Link>
          {onClose && (
            <button
              type="button"
              aria-label={text.closeMenu}
              onClick={onClose}
              className="inline-flex size-8 items-center justify-center rounded-full bg-riso-yellow text-ink outline-none focus-visible:shadow-focus"
            >
              <NavIcon name="close" className="size-4 [stroke-width:2.8]" />
            </button>
          )}
        </div>
        <ScopeSwitcher scope={scope} projects={projects} />
        <label className="flex h-8 items-center gap-2 rounded-ticket bg-ink/35 px-2.5 text-white/80 focus-within:shadow-focus">
          <NavIcon name="search" className="size-3.5" />
          <input
            ref={searchRef}
            type="search"
            aria-label={searchLabel}
            placeholder={text.search.placeholder}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="min-w-0 flex-1 bg-transparent text-[13px] text-white outline-none placeholder:text-white/80"
          />
          <kbd aria-hidden="true" className="font-sans text-[11px]">
            {text.search.shortcut}
          </kbd>
        </label>
      </div>

      {/* 滚动区本身是定位容器：待你处理角标里的 sr-only 说明随列表滚动并被裁剪，不会按未滚动时的位置撑高整页。 */}
      <div className="relative flex min-h-0 flex-1 flex-col gap-[3px] overflow-y-auto px-3 pt-1 pb-3 [scrollbar-width:thin]">
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
              badge={<PendingBadge sticker counts={taskCounts} />}
            />
            <NavItem to={`${projectBase(scope.name)}/folder`} icon="folder" label={text.items.folder} current={here.section === 'folder'} />
            <NavItem
              to={`${projectBase(scope.name)}/settings`}
              icon="projectSettings"
              label={text.items.projectSettings}
              current={here.section === 'settings' || here.section === 'plugins'}
            />
            {/* 地址里的项目找不到时没有 Agent 分组（主区说明找不到）；项目列表还没加载出来时分组里是加载中。 */}
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
      </div>

      <div className="flex shrink-0 flex-col gap-0.5 border-t-2 border-dashed border-white/30 px-3 pt-2.5 pb-3">
        <NavItem to="/plugins" icon="plugins" label={text.items.plugins} current={here.global === 'plugins'} />
        <NavItem to="/skills" icon="skills" label={text.items.skills} current={here.global === 'skills'} />
        <NavItem to="/stats" icon="stats" label={text.items.stats} current={here.global === 'stats'} />
        <NavItem to="/identities" icon="identities" label={text.items.identities} current={here.global === 'identities'} />
        <NavItem to="/settings" icon="settings" label={text.items.settings} current={here.global === 'settings'} />
        <ConnectionStatus className="mt-1.5 ml-2.5 self-start text-[11.5px]" />
      </div>
      {creating && <CreateAgentDialog scope={creating} onClose={() => setCreating(null)} />}
    </nav>
  )
}
