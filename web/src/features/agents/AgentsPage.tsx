import { useId, useState, useSyncExternalStore } from 'react'
import { Navigate, Outlet, useLocation, useOutletContext } from 'react-router'
import { PlusIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { reachability } from '@/api/client'
import { useAgents } from '@/api/queries'
import { useScope } from '@/features/projects/scope-context'
import { t } from '@/i18n'
import { isUnreachable } from '@/lib/errors'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { CreateAgentDialog } from './CreateAgentDialog'

// Agent 页通过路由出口把“新建 Agent”交给主区的空状态。
interface AgentsOutletContext {
  openCreate: () => void
}

// 当前项目或平台的 Agent 页。Agent 列表在左侧栏里，这里只有所选 Agent 的页面（没有选时是提示或首次使用的空状态）。
// 从项目设置的“去新建 Agent”进入时，地址的 state 中带 openCreate，打开时直接弹出新建对话框。
export function AgentsPage() {
  const scope = useScope()
  const location = useLocation()
  const [creating, setCreating] = useState(() => (location.state as { openCreate?: boolean } | null)?.openCreate === true)
  const openCreate = () => setCreating(true)
  return (
    <section className="h-full min-h-0 min-w-0">
      <Outlet context={{ openCreate } satisfies AgentsOutletContext} />
      {creating && <CreateAgentDialog scope={scope} onClose={() => setCreating(false)} />}
    </section>
  )
}

// 作用域还没确定时（项目列表加载中或加载失败）的主区：无法连接时不另作说明（全局提示与左侧栏的骨架已经说明），
// 其他错误显示出来，加载中只显示“加载中…”。
export function AgentsPlaceholder({ error }: { error: unknown }) {
  if (isUnreachable(error)) return null
  if (error) return <ErrorAlert error={error} className="mx-6 my-7 w-auto" />
  return <p className="px-6 py-7 text-[14px] text-ink-500">{t.common.loading}</p>
}

// 没有打开 Agent 时的主区：没有 Agent 时显示首次使用的空状态。项目里有 Agent 时直接进入 Manager 的对话，
// 没有 Manager 时进入列表中的第一个；平台仍提示从左侧栏选择。列表加载出来之前不显示内容。
// 只在打开时已有 Agent 才跳转：先显示了空状态、之后才出现的 Agent（例如刚新建的）由新建流程自己打开配置页。
export function AgentsIndex() {
  const { openCreate } = useOutletContext<AgentsOutletContext>()
  const scope = useScope()
  const agents = useAgents(scopeKey(scope))
  const list = agents.data?.agents
  const [sawEmpty, setSawEmpty] = useState(false)
  if (list?.length === 0 && !sawEmpty) setSawEmpty(true)
  if (list?.length === 0) return <FirstUse scope={scope} onCreate={openCreate} />
  if (scope.kind === 'project' && !sawEmpty) {
    if (!list) return null
    const target = list.find((agent) => agent.id === scope.project.managerAgentId) ?? list[0]
    return <Navigate replace to={agentPath(scope, target.id, 'chat')} />
  }
  return (
    <div className="flex h-full min-h-40 items-center justify-center p-6">
      <p className="text-ink-500">{t.agents.selectHint}</p>
    </div>
  )
}

// 首次使用的空状态：项目用蓝粉叠印，平台用蓝绿叠印，两者区分开。
function FirstUse({ scope, onCreate }: { scope: Scope; onCreate: () => void }) {
  const platform = scope.kind === 'platform'
  const titleId = useId()
  // 与列表头的新建按钮一致，无法连接时禁用。
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  return (
    <section aria-labelledby={titleId} className="flex h-full flex-col items-center justify-center gap-[22px] overflow-y-auto p-6 text-center">
      {/* 叠印插图：网点蓝圆、荧光粉（平台为绿）圆与黄圆，叠印处用 multiply 混色。 */}
      <span aria-hidden="true" className="relative h-40 w-[220px] shrink-0">
        <span className="riso-halftone absolute top-2.5 left-2.5 size-[140px] rounded-full bg-riso-blue" />
        <span className={cn('absolute top-0 left-[76px] size-[140px] rounded-full mix-blend-multiply', platform ? 'bg-green' : 'bg-riso-pink')} />
        <span className="absolute top-[92px] left-[150px] size-14 rounded-full bg-riso-yellow mix-blend-multiply" />
      </span>
      <div className="flex flex-col items-center gap-2">
        <h1 id={titleId} className="riso-misreg font-display-cn text-[56px] leading-[1.1] font-normal text-riso-blue">
          {platform ? t.projects.empty.platformTitle : t.agents.empty}
        </h1>
        <p className="text-[14.5px] text-ink-700">{platform ? t.projects.empty.platformNote : t.projects.empty.projectNote}</p>
      </div>
      <Button size="lg" className="h-12 px-[26px] text-base shadow-print-pink" disabled={!reachable} onClick={onCreate}>
        <PlusIcon strokeWidth={3} />
        {t.agents.create}
      </Button>
    </section>
  )
}
