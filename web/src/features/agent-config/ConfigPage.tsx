import { AgentPlugins } from '@/features/plugins/AgentPlugins'
import { StickyNote } from '@/components/StickyNote'
import type { Schemas } from '@/api/client'
import { useAgentStatus } from '@/api/queries'
import { config } from '@/config'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useRuntimeLookup } from '@/lib/runtime'
import { BasicSection } from './BasicSection'
import { ClisSection } from './ClisSection'
import { ConfigToc } from './ConfigToc'
import { DangerZone } from './DangerZone'
import { ModelCredentialSection } from './ModelCredentialSection'
import { LocalAccountSection } from './RuntimeNotes'
import { Section } from './Section'
import { SkillsSection } from './SkillsSection'
import { AgentPromptSection } from '@/features/prompts/AgentPromptSection'
import { AgentLocalDirectoriesSection } from '@/features/local-mode/LocalDirectories'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { useScope } from '@/features/projects/scope-context'
import { ProjectModelSelection } from '@/features/project-credentials/AgentAccountSelection'

export function ConfigPage() {
  const { agent } = useAgentContext()
  const scope = useScope()
  // The config page has no event stream, so poll busy/idle at the Agent list's refresh interval; used for the config-takes-effect
  // notice and the delete condition.
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const current = status.data?.currentRun ?? null
  const busy = status.data?.busy ?? agent.busy
  const queued = status.data ? status.data.queued.interrupt + status.data.queued.normal : 0
  const idle = status.data !== undefined && !status.data.busy && queued === 0
  const stale = current !== null && current.configVersion < agent.configVersion

  // Inside the sheet, the numbered sections are on the left (scrolling on their own) and the section ruler in the stub is on the right
  // (it doesn't scroll with the sections; hidden on narrow screens).
  // The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text) scroll with the content
  // instead of stretching the whole page based on their unscrolled position.
  return (
    <DraftNavigationGuard key={agent.id} projectName={scope.kind === 'project' ? scope.project.name : undefined}><div className="flex h-full min-h-0">
      <div className="relative h-full min-w-0 flex-1 overflow-y-auto">
        <div className="flex max-w-[960px] flex-col gap-6 pt-[26px] pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]">
          {/* “当前回复使用修改前的配置” (The current reply uses the config from before the change) is bold navy: the artboard's pink
              text on the yellow sticky note has a contrast ratio of only 4.45:1. */}
          <StickyNote>
            <p className="font-bold">{t.config.effectNotice}</p>
            {stale && <p className="font-black">{t.config.staleRunNotice}</p>}
          </StickyNote>
          <BasicSection key={agent.id} agent={agent} busy={busy} />
          <AgentPromptSection agent={agent} />
          <AgentLocalDirectoriesSection key={`local-dirs:${agent.id}`} agent={agent} />
          {agent.scope === 'project' ? <ProjectModelSelection key={`model-selection:${agent.id}`} agent={agent} />
            : <PlatformModelAccount agent={agent} />}
          <SkillsSection agentId={agent.id} />
          <ClisSection agent={agent} />
          <AgentPlugins agent={agent} />
          <DangerZone agent={agent} idle={idle} />
        </div>
      </div>
      <ConfigToc />
    </div></DraftNavigationGuard>
  )
}

// The model account section of a platform Agent: a runtime with its own login on this computer needs no account; the others log in to
// their model provider here. Shown once the runtime definitions are read; if they cannot be read, the section says why.
function PlatformModelAccount({ agent }: { agent: Schemas['Agent'] }) {
  const lookup = useRuntimeLookup()
  const runtime = lookup.get(agent.runtime)
  if (!runtime) return lookup.error ? <Section id="model-credential" title={t.config.modelCredential.title}><ErrorAlert error={lookup.error} /></Section> : null
  return runtime.capabilities.accounts === 'local' ? <LocalAccountSection runtime={runtime} /> : <ModelCredentialSection key={`${agent.id}:${agent.model.provider}`} agent={agent} />
}
