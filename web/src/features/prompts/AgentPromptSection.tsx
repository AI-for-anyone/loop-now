import { useQueryClient } from '@tanstack/react-query'
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
import { t } from '@/i18n'
import { PromptEditor } from './PromptEditor'

function InheritedPrompt({ projectId, projectName }: { projectId: string; projectName: string }) {
  const query = useProjectPrompt(projectId)
  return <div className="grid min-w-0 gap-3 rounded-card bg-tint-blue p-4" role="region" aria-label={t.prompts.inherited}>
    <h3 className="text-[15px] font-black [overflow-wrap:anywhere]">{t.prompts.source(projectName)}</h3>
    <p className="text-[12.5px] text-ink-700">{t.prompts.savedConfiguration}</p>
    <ErrorAlert error={query.error} />
    {query.error && <Button size="sm" variant="secondary" className="justify-self-start" onClick={() => void query.refetch()}>{t.common.retry}</Button>}
    {query.data ? <pre className="max-h-72 overflow-auto font-mono text-[13px] leading-[1.7] whitespace-pre-wrap [overflow-wrap:anywhere]">{query.data.prompt || t.prompts.empty}</pre> : query.isPending && <p role="status">{t.common.loading}</p>}
    <Link to={projectSettingsPath({ name: projectName })} className="justify-self-start text-[13.5px] font-black underline decoration-riso-pink decoration-2 underline-offset-4">{t.prompts.editProject}</Link>
  </div>
}
export function AgentPromptSection({ agent }: { agent: Schemas['Agent'] }) {
  const client = useQueryClient(), scope = useScope()
  // 与详情页共用Agent缓存；只在配置页观察器添加轮询，不改变对话页策略。
  useAgent(agent.id, config.agentListPollMs)
  return <Section id="prompt" title={t.prompts.sectionTitle}>
    {agent.scope === 'project' && scope.kind === 'project' && <InheritedPrompt projectId={scope.project.id} projectName={scope.project.name} />}
    <p className="text-[13px] leading-[1.7] text-ink-700">{agent.scope === 'project' ? t.prompts.agentHint : t.prompts.platformHint}</p>
    <p className="text-[13px] leading-[1.7] text-ink-700">{agent.scope === 'project' ? t.prompts.effect : t.prompts.platformEffect}</p>
    <PromptEditor key={agent.id} formId="agent-prompt" label={t.prompts.agentTitle} saveLabel={t.prompts.saveAgent} text={agent.prompt} version={agent.promptVersion}
      readLatest={async () => { const result = await getAgentPrompt(agent.id); return { text: result.prompt, version: result.promptVersion } }}
      save={async value => { const result = await putAgentPrompt(agent.id, { prompt: value.text, expectedPromptVersion: value.version }); await invalidateAgent(client, agent.id); return { text: result.prompt, version: result.promptVersion } }} />
  </Section>
}
