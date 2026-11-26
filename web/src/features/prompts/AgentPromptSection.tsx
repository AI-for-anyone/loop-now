import { useId, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronRightIcon } from 'lucide-react'
import { Link } from 'react-router'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import type { Schemas } from '@/api/client'
import { getAgentPrompt, putAgentPrompt, useProjectPrompt } from '@/api/prompts'
import { invalidateAgent, useAgent } from '@/api/queries'
import { config } from '@/config'
import { Section } from '@/features/agent-config/Section'
import { useScope } from '@/features/projects/scope-context'
import { projectSettingsPath } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'
import { PromptEditor } from './PromptEditor'

// Collapsed by default into a single compact disclosure row so the Agent's own prompt editor sits near the top of the tab.
function InheritedPrompt({ projectId, projectName }: { projectId: string; projectName: string }) {
  const query = useProjectPrompt(projectId)
  const [open, setOpen] = useState(false)
  const bodyId = useId()
  const isEmpty = query.data !== undefined && !query.data.prompt

  return (
    <div
      role="region"
      aria-label={t.prompts.inherited}
      className="min-w-0 overflow-hidden rounded-card border border-hairline bg-surface-subtle [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue"
    >
      <div className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 px-3.5 py-1.5">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={bodyId}
          onClick={() => setOpen((value) => !value)}
          className="inline-flex min-w-0 flex-1 cursor-pointer items-center gap-1.5 text-left text-[13px] font-semibold text-ink outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:font-black"
        >
          <ChevronRightIcon aria-hidden="true" className={cn('size-4 shrink-0 text-ink-500 transition-transform motion-reduce:transition-none', open && 'rotate-90')} />
          <span className="min-w-0 truncate">{t.prompts.source(projectName)}</span>
          {isEmpty && <span className="shrink-0 text-[12px] font-normal text-ink-500">{t.prompts.empty}</span>}
        </button>
        <Link
          to={projectSettingsPath({ name: projectName }, 'context')}
          className="shrink-0 text-[12.5px] font-medium underline decoration-ink decoration-1 underline-offset-4 hover:text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-2"
        >
          {t.prompts.editProject}
        </Link>
      </div>
      {open && (
        <div id={bodyId} className="grid min-w-0 gap-2.5 border-t border-hairline px-3.5 py-3 [[data-skin=plate]_&]:border-ink/10">
          <p className="text-[12px] text-ink-700">{t.prompts.savedConfiguration}</p>
          <ErrorAlert error={query.error} />
          {query.error && (
            <Button size="sm" variant="secondary" className="justify-self-start" onClick={() => void query.refetch()}>
              {t.common.retry}
            </Button>
          )}
          {query.data ? (
            <pre className="max-h-72 overflow-auto font-mono text-[13px] leading-[1.65] whitespace-pre-wrap [overflow-wrap:anywhere]">
              {query.data.prompt || t.prompts.empty}
            </pre>
          ) : (
            query.isPending && <p role="status" className="text-[12.5px] text-ink-500">{t.common.loading}</p>
          )}
        </div>
      )}
    </div>
  )
}

export function AgentPromptSection({ agent }: { agent: Schemas['Agent'] }) {
  const client = useQueryClient()
  const scope = useScope()
  // Shares the Agent cache with the detail page; polling is added only through the Config tab's observer, without changing
  // the Chat tab's strategy.
  useAgent(agent.id, config.agentListPollMs)
  return (
    <Section id="prompt" title={t.prompts.sectionTitle}>
      {agent.scope === 'project' && scope.kind === 'project' && <InheritedPrompt projectId={scope.project.id} projectName={scope.project.name} />}
      <p className="text-[13px] leading-snug text-ink-700">{agent.scope === 'project' ? t.prompts.agentHint : t.prompts.platformHint}</p>
      <PromptEditor
        key={agent.id}
        formId="agent-prompt"
        label={t.prompts.agentTitle}
        saveLabel={t.prompts.saveAgent}
        text={agent.prompt}
        version={agent.promptVersion}
        ask={{ target: 'agent', agentId: agent.id }}
        readLatest={async () => {
          const result = await getAgentPrompt(agent.id)
          return { text: result.prompt, version: result.promptVersion }
        }}
        save={async (value) => {
          const result = await putAgentPrompt(agent.id, { prompt: value.text, expectedPromptVersion: value.version })
          await invalidateAgent(client, agent.id)
          return { text: result.prompt, version: result.promptVersion }
        }}
      />
    </Section>
  )
}
