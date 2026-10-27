import { useQueryClient } from '@tanstack/react-query'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { getProjectPrompt, projectPromptKey, putProjectPrompt, useProjectPrompt } from '@/api/prompts'
import { t } from '@/i18n'
import { PromptEditor } from './PromptEditor'
export function ProjectPromptForm({ projectId }: { projectId: string }) {
  const query = useProjectPrompt(projectId), client = useQueryClient()
  return <>
    <p className="text-[14px] leading-[1.7] text-ink-700">{t.prompts.projectHint}</p>
    <p className="text-[13px] leading-[1.7] text-ink-700">{t.prompts.effect}</p>
    <ErrorAlert error={query.error} />
    {query.error && <Button size="sm" variant="secondary" className="self-start" onClick={() => void query.refetch()}>{t.common.retry}</Button>}
    {query.data ? <PromptEditor key={projectId} formId="project-prompt" label={t.prompts.projectTitle} saveLabel={t.prompts.saveProject}
      text={query.data.prompt} version={query.data.promptVersion}
      readLatest={async () => { const result = await getProjectPrompt(projectId); return { text: result.prompt, version: result.promptVersion } }}
      save={async value => { const result = await putProjectPrompt(projectId, { prompt: value.text, expectedPromptVersion: value.version }); await client.invalidateQueries({ queryKey: projectPromptKey(projectId) }); return { text: result.prompt, version: result.promptVersion } }} />
      : query.isPending && <p role="status">{t.common.loading}</p>}
  </>
}
