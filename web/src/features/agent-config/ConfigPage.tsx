import { AgentPlugins } from '@/features/plugins/AgentPlugins'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { useAgentStatus } from '@/api/queries'
import { config } from '@/config'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { BasicSection } from './BasicSection'
import { ClisSection } from './ClisSection'
import { ConfigToc } from './ConfigToc'
import { DangerZone } from './DangerZone'
import { ModelCredentialSection } from './ModelCredentialSection'
import { SkillsSection } from './SkillsSection'
import { AgentPromptSection } from '@/features/prompts/AgentPromptSection'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { useScope } from '@/features/projects/scope-context'
import { ProjectModelSelection } from '@/features/project-credentials/AgentAccountSelection'

export function ConfigPage() {
  const { agent } = useAgentContext()
  const scope = useScope()
  // 配置页没有事件流，按 Agent 列表的刷新间隔查询忙闲，用于配置生效提示与删除条件。
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const current = status.data?.currentRun ?? null
  const busy = status.data?.busy ?? agent.busy
  const queued = status.data ? status.data.queued.interrupt + status.data.queued.normal : 0
  const idle = status.data !== undefined && !status.data.busy && queued === 0
  const stale = current !== null && current.configVersion < agent.configVersion

  // 宽屏（≥ 1024）时左侧为区块、右侧为吸顶的分区目录；窄屏时目录在上方。目录在 DOM 中排在前面，Tab 顺序与窄屏的视觉顺序一致。
  return (
    <DraftNavigationGuard key={agent.id} projectName={scope.kind === 'project' ? scope.project.name : undefined}><div className="h-full overflow-y-auto">
      <div className="flex flex-col gap-6 px-6 pt-5 pb-16 lg:grid lg:grid-cols-[minmax(0,800px)_220px] lg:items-start lg:gap-x-9">
        <ConfigToc className="lg:sticky lg:top-6 lg:col-start-2 lg:row-start-1" />
        <div className="flex min-w-0 flex-col gap-6 lg:col-start-1 lg:row-start-1">
          {/* “当前回复使用修改前的配置”用藏青加粗：画板中的粉色文字在黄色便签上的对比度只有 4.45:1。 */}
          <Alert variant="note" role="note" className="px-5 py-3.5">
            <AlertDescription className="flex flex-col gap-0.5 text-[14.5px] text-ink [&_p:not(:last-child)]:mb-0">
              <p className="font-bold">{t.config.effectNotice}</p>
              {stale && <p className="font-black">{t.config.staleRunNotice}</p>}
            </AlertDescription>
          </Alert>
          <BasicSection key={agent.id} agent={agent} busy={busy} />
          <AgentPromptSection agent={agent} />
          {agent.scope === 'project' ? <ProjectModelSelection key={`model-selection:${agent.id}`} agent={agent} /> : <ModelCredentialSection key={`${agent.id}:${agent.model.provider}`} agent={agent} />}
          <SkillsSection agentId={agent.id} />
          <ClisSection agent={agent} />
          <AgentPlugins agent={agent} />
          <DangerZone agent={agent} idle={idle} />
        </div>
      </div>
    </div></DraftNavigationGuard>
  )
}
