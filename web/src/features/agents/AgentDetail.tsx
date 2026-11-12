import { useCallback, useState } from 'react'
import { Link, Navigate, Outlet, useLocation, useParams } from 'react-router'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IndexTabLink, IndexTabs } from '@/components/IndexTabs'
import { ManagerBadge } from '@/components/ManagerBadge'
import { OverprintDeco } from '@/components/OverprintDeco'
import { RuntimeBadge } from '@/components/RuntimeBadge'
import { Sheet } from '@/components/Sheet'
import type { Schemas } from '@/api/client'
import { useAgent, useAgentStatus, useProjects } from '@/api/queries'
import { config } from '@/config'
import { runPhaseText } from '@/features/chat/run-status-text'
import { t } from '@/i18n'
import { useScope } from '@/features/projects/scope-context'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, GLOBAL, overviewPath, sameScope } from '@/lib/scope'
import { sheetTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'
import type { AgentOutletContext, AgentStickerState } from './agent-context'
import { AgentAddress } from './AgentAddress'
import { SleepUntil } from '@/features/sleep/SleepUntil'

// 标题旁的状态标签（描边、略倾斜的胶囊），按会话丢失、正忙（加当前阶段）、空闲的优先级显示。对话页交来的实时状态优先；
// 没有实时状态时取定期查询的状态接口，接口结果到达前取 Agent 详情中的忙闲与会话状态。
function StatusChip({ agent, live }: { agent: Schemas['Agent']; live: AgentStickerState | null }) {
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const polled: AgentStickerState = status.data
    ? {
        sessionMissing: status.data.session.state === 'missing',
        busy: status.data.busy,
        phase: status.data.currentRun ? runPhaseText(status.data.currentRun) : null,
        sleep: status.data.sleep,
      }
    : { sessionMissing: agent.session.state === 'missing', busy: agent.busy, phase: null }
  const state = live ?? polled
  const [tone, label] = state.sessionMissing
    ? (['danger', t.agents.sessionMissing] as const)
    : state.busy
      ? (['busy', t.agents.busy] as const)
      : (['success', t.agents.idle] as const)
  const phase = !state.sessionMissing && state.busy ? state.phase : null
  return (
    <>
      <span
        role="status"
        data-agent-status
        data-tone={tone}
        className={cn(
          'riso-tilt inline-flex min-h-[26px] max-w-full flex-wrap items-center gap-x-1.5 rounded-full px-3 text-[12.5px] leading-[26px] font-black [--tilt-shadow:inset_0_0_0_2px_var(--ink)] [--tilt:-3deg]',
          chipTone[tone],
        )}
      >
        <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', chipDot[tone])} />
        <span>{label}</span>
        {phase && <span className="font-medium">{phase}</span>}
      </span>
      {!state.busy && state.sleep?.state === 'sleeping' && state.sleep.wakeAt &&
        <SleepUntil at={state.sleep.wakeAt} reason={state.sleep.reason} />}
    </>
  )
}

const chipTone = { success: 'text-green-text [--tilt-bg:var(--tint-green)]', busy: 'text-ink [--tilt-bg:var(--tint-pink)]', danger: 'text-red-text [--tilt-bg:var(--tint-red)]' }
const chipDot = { success: 'bg-green', busy: 'bg-riso-pink', danger: 'bg-red' }

export function AgentDetail() {
  const { agentId = '' } = useParams()
  const location = useLocation()
  const scope = useScope()
  const projects = useProjects()
  const agent = useAgent(agentId)
  // 对话页交来的实时状态，记下所属的 Agent，切换 Agent 时不会用到上一个 Agent 的状态。
  const [live, setLive] = useState<{ agentId: string; state: AgentStickerState } | null>(null)
  const reportLiveStatus = useCallback((state: AgentStickerState | null) => setLive(state ? { agentId, state } : null), [agentId])

  if (isNotFound(agent.error)) {
    return <p className="px-6 py-7 text-[15px] text-ink-500" role="status">{t.agents.notFound}</p>
  }
  // 已有数据时，后台刷新失败只在页签上方提示，不卸载页签内容，避免丢失未保存的编辑。
  if (!agent.data) {
    if (agent.isError) return <ErrorAlert error={agent.error} className="mx-6 my-7 w-auto" />
    return <p className="px-6 py-7 text-[15px] text-ink-500">{t.common.loading}</p>
  }

  // 地址中的项目或平台与 Agent 的实际归属不一致时，改用实际所在的地址，页签、查询参数与锚点不变。
  const actual = projects.data ? agentScope(agent.data, projects.data.projects) : null
  if (actual && !sameScope(actual, scope)) {
    const tab = location.pathname.split('/').at(-1)
    const target = agentPath(actual, agentId, tab === 'chat' || tab === 'config' || tab === 'folder' || tab === 'inbox' ? tab : undefined)
    return <Navigate replace to={`${target}${location.search}${location.hash}`} />
  }

  return (
    // 页头与索引标签在上，页签内容在下面那张纸里。左右与底部的留白随窗口宽度变化，底部给纸张的投影留出位置。
    <div className="flex h-full min-h-0 flex-col px-[clamp(14px,2.4vw,34px)] pb-[26px] max-nav:pb-4">
      <header className="@container relative flex shrink-0 flex-col gap-3 pt-[22px] pb-[18px]" data-agent-header>
        <OverprintDeco />
        <nav aria-label={t.shell.breadcrumb} className="relative">
          <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-bold text-ink-500">
            <li className="min-w-0">
              <Link to={overviewPath(scope.kind === 'project' ? { kind: 'project', name: scope.project.name } : GLOBAL)} className="[overflow-wrap:anywhere] hover:text-ink hover:underline">
                {scope.kind === 'project' ? scope.project.name : t.shell.global}
              </Link>
            </li>
            <li aria-hidden="true">›</li>
            <li>{scope.kind === 'project' ? t.shell.agentCrumb : t.shell.platformAgentCrumb}</li>
            <li aria-hidden="true">›</li>
            <li aria-current="page" className="min-w-0 [overflow-wrap:anywhere] text-riso-blue">
              {agent.data.name}
            </li>
          </ol>
        </nav>
        <div className="relative flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2.5 @[820px]:pr-[316px]">
          <h1 className={cn(sheetTitleClass, 'mr-1.5')}>{agent.data.name}</h1>
          {agent.data.isManager && <ManagerBadge size="md" className="px-3 text-[12.5px] leading-6" />}
          <RuntimeBadge runtime={agent.data.runtime} />
          <StatusChip agent={agent.data} live={live?.agentId === agentId ? live.state : null} />
        </div>
        {/* 模型与地址一行：模型是浅蓝底的等宽标签，地址在虚线框里。 */}
        <div className="relative flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-2 text-[12px] @[820px]:pr-[316px]">
          <span className="min-w-0 rounded-stamp bg-tint-blue px-[9px] font-mono leading-[22px] [overflow-wrap:anywhere]">{`${agent.data.model.provider} · ${agent.data.model.modelId}`}</span>
          <AgentAddress address={agent.data.address} />
        </div>
      </header>
      <IndexTabs aria-label={agent.data.name}>
        <IndexTabLink to={agentPath(scope, agentId, 'chat')}>{t.agents.tabs.chat}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'config')}>{t.agents.tabs.config}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'folder')}>{t.agents.tabs.folder}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'inbox')}>{t.agents.tabs.inbox}</IndexTabLink>
      </IndexTabs>
      <Sheet className="flex-1">
        {agent.isError && <ErrorAlert error={agent.error} className="mx-6 mt-3 w-auto" />}
        <div className="min-h-0 flex-1">
          {/* 按 Agent 重新挂载页签：草稿、对话框、实时状态与滚动位置都不能带到另一个 Agent。 */}
          <Outlet key={agentId} context={{ agent: agent.data, reportLiveStatus } satisfies AgentOutletContext} />
        </div>
      </Sheet>
    </div>
  )
}
