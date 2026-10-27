import { useContext, useEffect, useState } from 'react'
import { Link, Navigate, Outlet, useLocation, useNavigate, useParams, type Location } from 'react-router'
import { Button } from '@/components/ui/button'
import { useAgent, useProjects } from '@/api/queries'
import { AgentsPlaceholder } from '@/features/agents/AgentsPage'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, agentsPath, lastScope, projectBase, renamedHere, resolveLastScope, type Scope } from '@/lib/scope'
import { ScopeContext } from './scope-context'

// 地址中的项目名换成新名称，其后的路径、查询参数与锚点不变。
function withProjectName(location: Location, name: string) {
  const rest = location.pathname.split('/').slice(3)
  return `${projectBase(name)}${rest.length ? `/${rest.join('/')}` : ''}${location.search}${location.hash}`
}

// 找不到项目、项目已删除时整块内容换成的说明（交接说明第 11.5 节）。没有左侧列表，地址保持原样。
function ScopeNotice({ kind, name, defaultScope }: { kind: 'missing' | 'deleted'; name: string; defaultScope: Scope | null }) {
  return (
    <div role="alert" className="flex h-full min-h-80 flex-col items-center justify-center gap-3.5 overflow-y-auto p-6 text-center">
      {kind === 'missing' ? (
        // 网点蓝圆加一个粉色虚线空圆：要找的项目不在了。
        <span aria-hidden="true" className="relative h-[130px] w-[200px] shrink-0">
          <span className="riso-halftone absolute top-[5px] left-2.5 size-[120px] rounded-full bg-riso-blue" />
          <span className="absolute top-[5px] left-[76px] size-[116px] rounded-full border-[3px] border-dashed border-pink-text" />
        </span>
      ) : (
        <span
          aria-hidden="true"
          className="riso-tilt rounded-stamp px-[18px] py-1 font-display-cn text-[34px] leading-[1.2] text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_3px_var(--red-text)] [--tilt:-3deg]"
        >
          {t.projects.deleted.stamp}
        </span>
      )}
      <p className="riso-misreg font-display-cn text-[48px] leading-[1.15] text-riso-blue">
        {kind === 'missing' ? t.projects.notFound.title(name) : t.projects.deleted.title(name)}
      </p>
      <p className="text-[15px] text-ink-700">{kind === 'missing' ? t.projects.notFound.body : t.projects.deleted.body}</p>
      {defaultScope && (
        <Button asChild size="lg" className="h-12 px-[26px] text-base shadow-print-pink">
          <Link to={agentsPath(defaultScope)}>{t.projects.toDefault}</Link>
        </Button>
      )}
    </div>
  )
}

// 停留期间项目在别处被改名时的黄色便签，保留到点“知道了”或离开这个项目为止。
function RenamedNote({ from, to, onDismiss }: { from: string; to: string; onDismiss: () => void }) {
  return (
    <div
      role="status"
      className="riso-tilt mx-4 mt-4 flex max-w-[820px] shrink-0 flex-wrap items-center gap-x-[18px] gap-y-2.5 py-3.5 pr-[18px] pl-5 [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [--tilt:-0.5deg] sm:mx-[30px]"
    >
      {/* 胶带：只作装饰。 */}
      <span aria-hidden="true" className="absolute -top-[9px] left-7 h-[18px] w-16 -rotate-4 bg-white/60" />
      <span className="flex-[1_1_420px] text-[15px] font-bold">{t.projects.renamed(from, to)}</span>
      <Button variant="ink" size="sm" className="text-white" onClick={onDismiss}>
        {t.projects.renamedDismiss}
      </Button>
    </div>
  )
}

// /p/:projectName/…：按项目列表精确解析名称（区分大小写），列表加载完成前不判断找不到。
// 解析到的项目 ID 记在本页：停留期间项目在别处被改名，按 ID 换到新名称的地址并贴便签；被删除时说明已删除。
// 在本控制台改名时（renamedHere 中有它）只换地址，不贴便签，由项目设置页自己说明。
export function ProjectScopeRoute() {
  const { projectName = '' } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const projects = useProjects()
  // 本页解析到的项目 ID 与改名便签；按 React 的做法在渲染中根据上一次的结果更新。
  const [resolvedId, setResolvedId] = useState<string | null>(null)
  const [note, setNote] = useState<{ id: string; from: string; to: string } | null>(null)
  const list = projects.data?.projects
  const project = list?.find((p) => p.name === projectName)
  if (project && project.id !== resolvedId) setResolvedId(project.id)
  const followed = !project && resolvedId ? list?.find((p) => p.id === resolvedId) : undefined
  if (followed && !renamedHere.has(followed.id) && note?.to !== followed.name) setNote({ id: followed.id, from: projectName, to: followed.name })
  const current = project ?? followed
  if (note && current && note.id !== current.id) setNote(null)

  useEffect(() => {
    if (project) lastScope.set(project.id)
  }, [project])

  useEffect(() => {
    if (!followed) return
    renamedHere.delete(followed.id)
    void navigate(withProjectName(location, followed.name), { replace: true })
  }, [followed, location, navigate])

  if (!list) return <AgentsPlaceholder error={projects.error} />
  const defaultProject = list.find((p) => p.isDefault)
  const defaultScope: Scope | null = defaultProject ? { kind: 'project', project: defaultProject } : null
  // 改名后、地址换成新名称之前，页面按 ID 继续显示同一个项目，不卸载页面内容。
  if (!current) return <ScopeNotice kind={resolvedId ? 'deleted' : 'missing'} name={projectName} defaultScope={defaultScope} />

  return (
    <ScopeContext.Provider value={{ kind: 'project', project: current }}>
      <div className="flex h-full min-h-0 flex-col">
        {note && <RenamedNote from={note.from} to={note.to} onDismiss={() => setNote(null)} />}
        <div className="min-h-0 flex-1">
          <Outlet />
        </div>
      </div>
    </ScopeContext.Provider>
  )
}

const platformScope: Scope = { kind: 'platform' }

// /platform/…：平台持有的 Agent，与项目共用 Agent 页、对话页与配置页。
export function PlatformScopeRoute() {
  useEffect(() => {
    lastScope.set('platform')
  }, [])
  return (
    <ScopeContext.Provider value={platformScope}>
      <Outlet />
    </ScopeContext.Provider>
  )
}

// 旧地址 /agents：进入最近使用的项目或平台；没有记录或记录的项目已不存在时，进入默认项目。
export function LegacyAgentsRedirect() {
  const location = useLocation()
  const projects = useProjects()
  const scope = projects.data ? resolveLastScope(projects.data.projects) : null
  if (!scope) return <AgentsPlaceholder error={projects.error} />
  return <Navigate replace to={`${agentsPath(scope)}${location.search}${location.hash}`} />
}

// 旧地址 /agents/:agentId/…：读取 Agent，改用它实际所在的地址，保留其后的页签、查询参数与锚点。
// Agent 不存在时，在最近使用的项目或平台中打开同一地址，由详情页说明该 Agent 不存在。
export function LegacyAgentRedirect() {
  const { agentId = '', '*': rest = '' } = useParams()
  const location = useLocation()
  const projects = useProjects()
  const agent = useAgent(agentId)
  const list = projects.data?.projects
  const target = list && agent.data ? agentScope(agent.data, list) : null
  const fallback = list && isNotFound(agent.error) ? resolveLastScope(list) : null
  const scope = target ?? fallback
  if (!scope) return <AgentsPlaceholder error={projects.error ?? (isNotFound(agent.error) ? null : agent.error)} />
  return <Navigate replace to={`${agentsPath(scope)}/${agentId}${rest ? `/${rest}` : ''}${location.search}${location.hash}`} />
}

// /p/<项目名>、/platform 进入 Agent 页；/…/agents/<Agent ID> 进入对话页。
// 用编码后的绝对地址跳转：React Router 按解码后的路径解析相对地址，项目名含“?”“%”时相对地址会出错。
export function ScopeIndexRedirect() {
  const scope = useContext(ScopeContext)
  return scope ? <Navigate replace to={agentsPath(scope)} /> : null
}

export function AgentIndexRedirect() {
  const scope = useContext(ScopeContext)
  const { agentId = '' } = useParams()
  const location = useLocation()
  return scope ? <Navigate replace to={`${agentPath(scope, agentId, 'chat')}${location.search}${location.hash}`} /> : null
}
