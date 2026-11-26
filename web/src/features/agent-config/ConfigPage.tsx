import { useEffect, useRef } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { AgentPlugins } from '@/features/plugins/AgentPlugins'
import { SubNav } from '@/components/IndexTabs'
import { PageBody } from '@/components/Sheet'
import type { Schemas } from '@/api/client'
import { useAgentStatus } from '@/api/queries'
import { config } from '@/config'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useRuntimeLookup } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { BasicSection } from './BasicSection'
import { ClisSection } from './ClisSection'
import { tabRulerSections } from './ConfigToc'
import { DangerZone } from './DangerZone'
import { ModelCredentialSection } from './ModelCredentialSection'
import { LocalAccountSection } from './RuntimeNotes'
import { Section } from './Section'
import { parseConfigTab, resolveConfigHash, sectionId, sectionTitleId, tabPanelId, type ConfigTab } from './sections'
import { SkillsSection } from './SkillsSection'
import { PermissionsSection, WorkSection } from './WorkSection'
import { AgentPromptSection } from '@/features/prompts/AgentPromptSection'
import { AgentLocalDirectoriesSection } from '@/features/local-mode/LocalDirectories'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { useScope } from '@/features/projects/scope-context'
import { ProjectModelSelection } from '@/features/project-credentials/AgentAccountSelection'

export function ConfigPage() {
  const { agent } = useAgentContext()
  const scope = useScope()
  const [searchParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()
  const bodyRef = useRef<HTMLDivElement | null>(null)

  // The config page has no event stream, so poll busy/idle at the Agent list's refresh interval; used for the config-takes-effect
  // status pill and the delete condition.
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const current = status.data?.currentRun ?? null
  const busy = status.data?.busy ?? agent.busy
  const queued = status.data ? status.data.queued.interrupt + status.data.queued.normal : 0
  const idle = status.data !== undefined && !status.data.busy && queued === 0
  const stale = current !== null && current.configVersion < agent.configVersion

  // Legacy #config-* hashes activate their owning sub-tab and scroll/focus the target section.
  const hashTarget = resolveConfigHash(location.hash)
  const activeTab: ConfigTab = hashTarget?.tab ?? parseConfigTab(searchParams.get('tab'))

  const targetSection = hashTarget?.section ?? null
  useEffect(() => {
    if (!targetSection) return
    document.getElementById(sectionId(targetSection))?.scrollIntoView({ block: 'start' })
    document.getElementById(sectionTitleId(targetSection))?.focus({ preventScroll: true })
  }, [targetSection, location.hash])

  const selectTab = (next: ConfigTab) => {
    const params = new URLSearchParams(searchParams)
    params.set('tab', next)
    void navigate({ search: `?${params.toString()}`, hash: '' }, { replace: true })
    if (bodyRef.current) bodyRef.current.scrollTop = 0
  }

  const panelClass = (tab: ConfigTab) => cn('flex min-w-0 flex-col gap-6', activeTab !== tab && 'hidden')

  return (
    <DraftNavigationGuard key={agent.id} projectName={scope.kind === 'project' ? scope.project.name : undefined}>
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 border-b border-hairline bg-surface-canvas px-4 sm:px-6 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/20 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:pt-2">
          <SubNav
            aria-label={t.config.tabs.label}
            value={activeTab}
            onChange={selectTab}
            className="min-w-0 border-b-0 bg-transparent px-0 sm:px-0 [[data-skin=plate]_&]:pl-0"
            items={[
              { key: 'general', label: t.config.tabs.general, controls: tabPanelId('general') },
              { key: 'workspace', label: t.config.tabs.workspace, controls: tabPanelId('workspace') },
              { key: 'tools', label: t.config.tabs.tools, controls: tabPanelId('tools') },
              {
                key: 'danger',
                label: <span className="text-red-text">{t.config.tabs.danger}</span>,
                controls: tabPanelId('danger'),
              },
            ]}
          />
          <div
            role="note"
            title={stale ? `${t.config.effectNotice} ${t.config.staleRunNotice}` : t.config.effectNotice}
            className={cn(
              'my-1 flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[12px] font-medium whitespace-nowrap [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-bold',
              stale
                ? 'border-pink-text/30 bg-tint-yellow text-ink [[data-skin=plate]_&]:bg-riso-yellow'
                : 'border-hairline bg-surface-subtle text-ink-500 [[data-skin=plate]_&]:bg-ink/[0.06] [[data-skin=plate]_&]:text-ink-700',
            )}
          >
            <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', stale ? 'bg-riso-pink' : 'bg-ink-500/60')} />
            <span>{stale ? t.config.staleRunNotice : t.config.effectShort}</span>
            <span className="sr-only">{t.config.effectNotice}</span>
          </div>
        </div>
        {/* Dedicated positioning wrapper below SubNav so the floating vernier PageRuler aligns only with the scrollable body. */}
        <div className="relative flex min-h-0 flex-1 flex-col">
          <PageBody
            ref={bodyRef}
            width="narrow"
            ruler={{ label: t.config.toc.title, sections: tabRulerSections(activeTab) }}
            innerClassName="pb-14"
          >
            {/* All four tab panels stay mounted (hidden via CSS when inactive) so unsaved edits survive tab switches and DraftNavigationGuard guards route exits. */}
            <div role="tabpanel" id={tabPanelId('general')} data-tab-panel="general" data-state={activeTab === 'general' ? 'active' : 'inactive'} className={panelClass('general')}>
              <BasicSection key={agent.id} agent={agent} busy={busy} />
              {agent.scope === 'project' ? <ProjectModelSelection key={`model-selection:${agent.id}`} agent={agent} /> : <PlatformModelAccount agent={agent} />}
              <PermissionsSection key={`permissions:${agent.id}`} agent={agent} />
            </div>
            <div role="tabpanel" id={tabPanelId('workspace')} data-tab-panel="workspace" data-state={activeTab === 'workspace' ? 'active' : 'inactive'} className={panelClass('workspace')}>
              <AgentPromptSection agent={agent} />
              <WorkSection key={`work:${agent.id}`} agent={agent} />
              <AgentLocalDirectoriesSection key={`local-dirs:${agent.id}`} agent={agent} />
            </div>
            <div role="tabpanel" id={tabPanelId('tools')} data-tab-panel="tools" data-state={activeTab === 'tools' ? 'active' : 'inactive'} className={panelClass('tools')}>
              <SkillsSection agentId={agent.id} />
              <ClisSection agent={agent} />
              <AgentPlugins agent={agent} />
            </div>
            <div role="tabpanel" id={tabPanelId('danger')} data-tab-panel="danger" data-state={activeTab === 'danger' ? 'active' : 'inactive'} className={panelClass('danger')}>
              <DangerZone agent={agent} idle={idle} />
            </div>
          </PageBody>
        </div>
      </div>
    </DraftNavigationGuard>
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
