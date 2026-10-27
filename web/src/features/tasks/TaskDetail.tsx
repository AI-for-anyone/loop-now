import { TaskBadge } from './TaskBadge'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { useEffect, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'
import { useTask, tasksPath } from '@/api/tasks'
import { useScope } from '@/features/projects/scope-context'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { useTaskLive } from './useTaskLive'
import { LiveState } from './TasksPage'
import { WorkflowGraph } from './WorkflowGraph'
import { NodeRecordPanel, type NodeDraft } from './NodeRecordPanel'
import { TaskActions } from './TaskActions'
import { TaskInstanceSleeps } from '@/features/sleep/TaskInstanceSleeps'
import { SleepUntil } from '@/features/sleep/SleepUntil'

export function TaskDetail() {
  const { taskId = '' } = useParams()
  const scope = useScope()
  const task = useTask(taskId)
  const connection = useTaskLive(taskId, false)
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
  const select = (id: string) => {
    setSelection(old => old + 1)
    const latest = detail.nodes.filter((n) => n.stepId === id).sort((a, b) => a.visit - b.visit || a.attempt - b.attempt).at(-1)
    viewRecord(id, latest?.id)
  }
  return <div className="flex h-full min-h-0 flex-col overflow-y-auto p-4 sm:p-5 lg:overflow-hidden">
    <Link className="font-bold text-riso-blue underline" to={tasksPath(scope.project.name)}>{t.tasks.back}</Link>
    <header className="my-3 flex shrink-0 flex-wrap items-start justify-between gap-3">
      <div className="min-w-0 flex-[1_1_28rem]"><p className="text-sm font-bold">{scope.project.name}</p><h1 className="font-display-cn text-[30px] break-words text-riso-blue sm:text-[36px]">{detail.task.title}</h1></div>
      <div className="flex max-w-full flex-wrap items-center justify-end gap-x-4 gap-y-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1"><TaskBadge status={detail.task.status} /><p className="text-sm">{t.tasks.cleanup[detail.cleanup]}</p><LiveState state={connection} /></div>
        <TaskActions task={detail.task} disabled={connection !== 'open' || task.isError} showRetry={false} />
        <PagePluginActions page="task" where={{ taskId: detail.task.id }} projectId={scope.project.id} />
      </div>
    </header>
    <ErrorAlert error={task.error} />
    {currentSleep?.state === 'sleeping' && currentSleep.wakeAt && <div className="my-4 rounded-card bg-tint-blue p-4"><SleepUntil at={currentSleep.wakeAt} reason={currentSleep.reason} /></div>}
    <TaskInstanceSleeps detail={detail} />
    {detail.task.currentNodeRunId && node?.id !== detail.task.currentNodeRunId && <div className="my-3 flex flex-wrap items-center gap-3"><span>{t.tasks.history}</span><Button variant="secondary" size="sm" onClick={() => viewRecord(detail.task.currentStepId!, detail.task.currentNodeRunId!)}>{t.tasks.currentChanged}</Button></div>}
    <div className="mt-3 grid min-h-0 min-w-0 shrink-0 gap-4 lg:flex-1 lg:shrink lg:grid-cols-[minmax(0,45fr)_minmax(0,55fr)]"><WorkflowGraph detail={detail} selected={step} onSelect={select} /><div className="min-h-0 min-w-0">
      {explicitRecord && !node ? <p role="alert">{t.tasks.invalidRecord}</p> : node ? <NodeRecordPanel key={`${taskId}:${node.stepId}:${node.id}:${selection}`} detail={detail} node={node} disabled={connection !== 'open' || task.isError}
        draft={drafts[`${taskId}:${node.id}`] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }}
        drafts={Object.entries(drafts).filter(([key]) => key.startsWith(`${taskId}:`)).map(([, value]) => value)}
        onAction={action => setDrafts(old => { const key = `${taskId}:${node.id}`; return { ...old, [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), action } } })}
        onAccepted={(sent, ids = []) => setDrafts(old => { const key = `${taskId}:${node.id}`, value = old[key]; return value ? { ...old, [key]: { ...value, text: value.text === sent ? '' : value.text, images: value.images?.filter(image => !ids.includes(image.id)) } } : old })}
        onImages={images => setDrafts(old => { const key = `${taskId}:${node.id}`; return { ...old, [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), images } } })}
        onDraft={text => setDrafts(old => ({ ...old, [`${taskId}:${node.id}`]: { ...(old[`${taskId}:${node.id}`] ?? { agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), text } }))} /> : <p>{step ? t.tasks.noRecord : t.tasks.selectNode}</p>}
    </div></div>
  </div>
}
