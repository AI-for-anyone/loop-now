import { TaskBadge } from './TaskBadge'
import { useAgents } from '@/api/queries'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { overviewPath } from '@/lib/scope'
import { narrowQuery, useMediaQuery } from '@/lib/use-media-query'
import { WorkflowChips, WorkflowRuler } from './WorkflowRuler'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { useEffect, useState, type ReactNode } from 'react'
import { useParams, useSearchParams } from 'react-router'
import { useTask, tasksPath } from '@/api/tasks'
import { useScope } from '@/features/projects/scope-context'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { useTaskLive } from './useTaskLive'
import { NodeRecordPanel, type NodeDraft } from './NodeRecordPanel'
import { TaskActions } from './TaskActions'
import { TaskWorkspace } from './TaskWorkspace'
import { TaskInstanceSleeps } from '@/features/sleep/TaskInstanceSleeps'
import { SleepUntil } from '@/features/sleep/SleepUntil'

export function TaskDetail() {
  const { taskId = '' } = useParams()
  const scope = useScope()
  const task = useTask(taskId)
  const connection = useTaskLive(taskId, false)
  const agents = useAgents(scope.kind === 'project' ? scope.project.id : null)
  const narrow = useMediaQuery(narrowQuery)
  const [params, setParams] = useSearchParams()
  const [selection, setSelection] = useState(0)
  const [drafts, setDrafts] = useState<Record<string, NodeDraft>>({})
  const detail = task.data
  const currentSleep = detail?.nodes.find(node => node.id === detail.task.currentNodeRunId)?.sleep
  const explicitRecord = params.get('record')
  const explicitStep = params.get('step')
  const records = detail?.nodes.filter((n) => !explicitStep || n.stepId === explicitStep) ?? []
  const node = explicitRecord ? detail?.nodes.find((n) => n.id === explicitRecord && (!explicitStep || n.stepId === explicitStep)) : records.find((n) => n.id === detail?.task.currentNodeRunId) ?? records.at(-1)
  const step = explicitStep ?? node?.stepId ?? null
  // Persist the initial choice; subsequent SSE updates must not take away a historical selection.
  useEffect(() => { if (detail && node && !explicitRecord && !explicitStep) setParams(previous => { const next = new URLSearchParams(previous); next.set('step', node.stepId); next.set('record', node.id); return next }, { replace: true }) }, [detail, node, explicitRecord, explicitStep, setParams])
  if (scope.kind !== 'project') return null
  const agentName = (id: string) => agents.data?.agents.find((agent) => agent.id === id)?.name ?? id
  if (isNotFound(task.error)) return <p className="p-6">{t.tasks.missing}</p>
  if (!detail) return <div className="p-6"><ErrorAlert error={task.error} />{!task.isError && t.tasks.loading}</div>
  if (detail.task.projectId !== scope.project.id) return <p role="alert" className="p-6">{t.tasks.wrongProject}</p>
  const viewRecord = (stepId: string, recordId?: string) => setParams(previous => {
    const next = new URLSearchParams(previous)
    next.set('step', stepId)
    if (recordId) next.set('record', recordId)
    else next.delete('record')
    return next
  })
  // Clicking a step on the workflow ruler: select it and view its most recent visit; the Node materials tab goes back to Conversation
  // (the panel remounts keyed by the selection count).
  const select = (id: string) => {
    setSelection(old => old + 1)
    const latest = detail.nodes.filter((n) => n.stepId === id).sort((a, b) => a.visit - b.visit || a.attempt - b.attempt).at(-1)
    viewRecord(id, latest?.id)
  }
  const project = scope.project
  const ruler = <WorkflowRuler detail={detail} selected={step} record={node?.id} onSelect={select} onRecord={viewRecord} agentName={agentName} />
  // Task details (redesign step 5): a compact page header (hollow task number, status stamp, archive status, live status, created and
  // updated times, and a red-outlined “取消任务” (Cancel task) on the right),
  // and below it one sheet filling the page: two index tabs on the sheet's top edge, the selected node's conversation or node materials
  // on the sheet, and the vertical workflow ruler in the stub on the right.
  // On narrow screens the stub is hidden, and a horizontally scrollable row of small step tags appears above the index tabs instead.
  return <PageFrame>
    <PageHeader
      crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }, { label: t.tasks.title, to: tasksPath(project.name) }]}
      current={t.tasks.numberLabel(detail.task.number)}
      title={detail.task.title}
      lead={<span aria-hidden="true" data-task-number className="shrink-0 font-display-latin text-[34px] leading-none font-extrabold tracking-[-0.02em] text-transparent [-webkit-text-stroke:1.6px_var(--riso-blue)] nav:text-[40px] nav:[-webkit-text-stroke:1.8px_var(--riso-blue)]">{t.tasks.numberLabel(detail.task.number)}</span>}
      actions={<><TaskActions task={detail.task} disabled={connection !== 'open' || task.isError} showRetry={false} /><PagePluginActions page="task" where={{ taskId: detail.task.id }} projectId={project.id} /></>}
    >
      <TaskBadge status={detail.task.status} />
      <span className="rounded-[4px] bg-paper-tab px-[9px] text-[12px] leading-[22px] font-bold text-ink-700">{t.tasks.cleanup[detail.cleanup]}</span>
      <LiveDot state={connection === 'open' ? 'open' : connection === 'connecting' ? 'connecting' : 'reconnecting'} label={t.tasks.updating} />
      <span className="font-mono text-[12px] text-ink-500">{t.tasks.page.meta(detail.task.createdAt, detail.task.updatedAt)}</span>
    </PageHeader>
    <div className="flex shrink-0 flex-col px-3.5 nav:px-6 empty:hidden">
      <ErrorAlert error={task.error} />
      <TaskWorkspace taskId={detail.task.id} projectId={project.id} workspace={detail.workspace} />
      {currentSleep?.state === 'sleeping' && currentSleep.wakeAt && <div className="mb-3 rounded-card bg-tint-blue p-4"><SleepUntil at={currentSleep.wakeAt} reason={currentSleep.reason} /></div>}
      <TaskInstanceSleeps detail={detail} />
      {detail.task.currentNodeRunId && node?.id !== detail.task.currentNodeRunId && <div className="mb-2.5 flex flex-wrap items-center gap-3 text-[13px] font-bold"><span>{t.tasks.history}</span><Button variant="secondary" size="sm" onClick={() => viewRecord(detail.task.currentStepId!, detail.task.currentNodeRunId!)}>{t.tasks.currentChanged}</Button></div>}
      {narrow && <WorkflowChips detail={detail} selected={step} onSelect={select} />}
    </div>
    {explicitRecord && !node ? <EmptySheet stub={ruler}><p role="alert">{t.tasks.invalidRecord}</p></EmptySheet> : node ? <NodeRecordPanel key={`${taskId}:${node.stepId}:${node.id}:${selection}`} detail={detail} node={node} disabled={connection !== 'open' || task.isError} stub={ruler}
        draft={drafts[`${taskId}:${node.id}`] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }}
        drafts={Object.entries(drafts).filter(([key]) => key.startsWith(`${taskId}:`)).map(([, value]) => value)}
        onAction={action => setDrafts(old => { const key = `${taskId}:${node.id}`; return { ...old, [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), action } } })}
        onAccepted={(sent, ids = []) => setDrafts(old => { const key = `${taskId}:${node.id}`, value = old[key]; return value ? { ...old, [key]: { ...value, text: value.text === sent ? '' : value.text, images: value.images?.filter(image => !ids.includes(image.id)) } } : old })}
        onImages={images => setDrafts(old => { const key = `${taskId}:${node.id}`; return { ...old, [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), images } } })}
        onDraft={text => setDrafts(old => ({ ...old, [`${taskId}:${node.id}`]: { ...(old[`${taskId}:${node.id}`] ?? { agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), text } }))} /> : <EmptySheet stub={ruler}><p>{step ? t.tasks.noRecord : t.tasks.selectNode}</p></EmptySheet>}
  </PageFrame>
}

// When the selected step hasn't been visited yet (or the requested record doesn't exist): the sheet holds just one line of explanation,
// and the stub still shows the workflow ruler.
function EmptySheet({ stub, children }: { stub: ReactNode; children: ReactNode }) {
  return <Sheet className="min-h-0 flex-1">
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 items-center justify-center p-8 text-center font-display-cn text-[26px] text-riso-blue">{children}</div>
      {stub}
    </div>
  </Sheet>
}
