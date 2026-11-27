import { useEffect, useState, type ReactNode } from 'react'
import { useParams, useSearchParams } from 'react-router'
import { useAgents } from '@/api/queries'
import { tasksPath, useTask } from '@/api/tasks'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { useScope } from '@/features/projects/scope-context'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { TaskInstanceSleeps } from '@/features/sleep/TaskInstanceSleeps'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { overviewPath } from '@/lib/scope'
import { narrowQuery, useMediaQuery } from '@/lib/use-media-query'
import { NodeRecordPanel, type NodeDraft, type NodePanelTab } from './NodeRecordPanel'
import { StepPrompt, WithConversationBadge } from './StepPrompt'
import { TaskActions } from './TaskActions'
import { TaskBadge } from './TaskBadge'
import { TaskWorkspace } from './TaskWorkspace'
import { useTaskLive } from './useTaskLive'
import { stepNumber } from './workflow-steps'
import { WorkflowChips, WorkflowRuler } from './WorkflowRuler'

// Breakpoint matching Tailwind's lg (1024px) where the 216px left step rail collapses to horizontal WorkflowChips.
const compactRailQuery = '(width < 64rem)'

export function TaskDetail() {
  const { taskId = '' } = useParams()
  const scope = useScope()
  const task = useTask(taskId)
  const connection = useTaskLive(taskId, false)
  const agents = useAgents(scope.kind === 'project' ? scope.project.id : null)
  const belowLg = useMediaQuery(compactRailQuery)
  const belowNav = useMediaQuery(narrowQuery)
  const narrow = belowLg || belowNav
  const [params, setParams] = useSearchParams()
  const [tabState, setTabState] = useState<{ taskId: string; tab: NodePanelTab }>({ taskId, tab: 'conversation' })
  const tab: NodePanelTab = tabState.taskId === taskId ? tabState.tab : 'conversation'
  const setTab = (next: NodePanelTab) => setTabState({ taskId, tab: next })
  const [drafts, setDrafts] = useState<Record<string, NodeDraft>>({})
  const detail = task.data
  const currentSleep = detail?.nodes.find((node) => node.id === detail.task.currentNodeRunId)?.sleep
  const explicitRecord = params.get('record')
  const explicitStep = params.get('step')
  const records = detail?.nodes.filter((n) => !explicitStep || n.stepId === explicitStep) ?? []
  const node = explicitRecord
    ? detail?.nodes.find((n) => n.id === explicitRecord && (!explicitStep || n.stepId === explicitStep))
    : records.find((n) => n.id === detail?.task.currentNodeRunId) ?? records.at(-1)
  const step = explicitStep ?? node?.stepId ?? null

  // Persist the initial choice; subsequent SSE updates must not take away a historical selection.
  useEffect(() => {
    if (detail && node && !explicitRecord && !explicitStep) {
      setParams(
        (previous) => {
          const next = new URLSearchParams(previous)
          next.set('step', node.stepId)
          next.set('record', node.id)
          return next
        },
        { replace: true },
      )
    }
  }, [detail, node, explicitRecord, explicitStep, setParams])

  if (scope.kind !== 'project') return null
  const agentName = (id: string) => agents.data?.agents.find((agent) => agent.id === id)?.name ?? id
  if (isNotFound(task.error)) return <p className="p-6">{t.tasks.missing}</p>
  if (!detail) {
    return (
      <div className="p-6">
        <ErrorAlert error={task.error} />
        {!task.isError && t.tasks.loading}
      </div>
    )
  }
  if (detail.task.projectId !== scope.project.id) {
    return (
      <p role="alert" className="p-6">
        {t.tasks.wrongProject}
      </p>
    )
  }

  const viewRecord = (stepId: string, recordId?: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous)
      next.set('step', stepId)
      if (recordId) next.set('record', recordId)
      else next.delete('record')
      return next
    })

  // Selecting a step jumps to its latest visit while preserving the active Conversation/Materials tab.
  const select = (id: string) => {
    const latest = detail.nodes
      .filter((n) => n.stepId === id)
      .sort((a, b) => a.visit - b.visit || a.attempt - b.attempt)
      .at(-1)
    viewRecord(id, latest?.id)
  }

  const project = scope.project
  const selectedStepIndex = step ? detail.workflow.steps.findIndex((s) => s.id === step) : -1
  const selectedStep = selectedStepIndex >= 0 ? detail.workflow.steps[selectedStepIndex] : undefined
  const nodeSleep =
    node?.sleep?.state === 'sleeping' && node.sleep.nodeRunId === node.id && node.sleep.runtimeId === node.runtimeId && node.sleep.wakeAt
      ? node.sleep
      : null
  const activeSleep = nodeSleep ?? (currentSleep?.state === 'sleeping' && currentSleep.wakeAt ? currentSleep : null)
  const metaTooltip = t.tasks.page.meta(detail.task.createdAt, detail.task.updatedAt)

  return (
    <PageFrame>
      <PageHeader
        crumbs={[
          { label: project.name, to: overviewPath({ kind: 'project', name: project.name }) },
          { label: t.tasks.title, to: tasksPath(project.name) },
        ]}
        current={t.tasks.numberLabel(detail.task.number)}
        title={detail.task.title}
        lead={
          <span
            aria-hidden="true"
            data-task-number
            className="shrink-0 font-mono text-[15px] leading-none font-semibold tracking-tight text-ink-500 tabular-nums"
          >
            {t.tasks.numberLabel(detail.task.number)}
          </span>
        }
        actions={
          <>
            <TaskActions task={detail.task} disabled={connection !== 'open' || task.isError} showRetry={false} />
            <PagePluginActions page="task" where={{ taskId: detail.task.id }} projectId={project.id} />
          </>
        }
      >
        <TaskBadge status={detail.task.status} />
        <LiveDot state={connection === 'open' ? 'open' : connection === 'connecting' ? 'connecting' : 'reconnecting'} label={t.tasks.updating} />
        <span
          title={metaTooltip}
          className="rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 text-[11.5px] leading-4 font-medium text-ink-500"
        >
          {t.tasks.cleanup[detail.cleanup]}
        </span>
        <time dateTime={detail.task.updatedAt} title={metaTooltip} className="hidden font-mono text-[11.5px] text-ink-500 tabular-nums sm:inline">
          {t.tasks.updatedAt(detail.task.updatedAt)}
        </time>
      </PageHeader>
      <div className="flex shrink-0 flex-col px-4 pt-2.5 sm:px-6 empty:hidden">
        <ErrorAlert error={task.error} />
        <TaskWorkspace taskId={detail.task.id} projectId={project.id} workspace={detail.workspace} />
      </div>
      {activeSleep?.wakeAt && (
        <div
          data-task-sleep
          className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hairline bg-surface-subtle/60 px-4 py-2 text-[12px] sm:px-6"
        >
          <SleepUntil compact at={activeSleep.wakeAt} reason={activeSleep.reason} />
        </div>
      )}
      <TaskInstanceSleeps detail={detail} />
      {narrow && <WorkflowChips detail={detail} selected={step} record={node?.id} onSelect={select} onRecord={viewRecord} />}
      <Sheet className="min-h-0 flex-1 flex-row">
        <WorkflowRuler detail={detail} selected={step} record={node?.id} onSelect={select} onRecord={viewRecord} agentName={agentName} />
        {explicitRecord && !node ? (
          <EmptyPane>
            <p role="alert">{t.tasks.invalidRecord}</p>
          </EmptyPane>
        ) : node ? (
          <NodeRecordPanel
            key={taskId}
            detail={detail}
            node={node}
            tab={tab}
            onTabChange={setTab}
            onSelectRecord={viewRecord}
            disabled={connection !== 'open' || task.isError}
            draft={drafts[`${taskId}:${node.id}`] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }}
            drafts={Object.entries(drafts)
              .filter(([key]) => key.startsWith(`${taskId}:`))
              .map(([, value]) => value)}
            onAction={(action) =>
              setDrafts((old) => {
                const key = `${taskId}:${node.id}`
                return {
                  ...old,
                  [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), action },
                }
              })
            }
            onAccepted={(sent, ids = []) =>
              setDrafts((old) => {
                const key = `${taskId}:${node.id}`,
                  value = old[key]
                return value
                  ? { ...old, [key]: { ...value, text: value.text === sent ? '' : value.text, images: value.images?.filter((image) => !ids.includes(image.id)) } }
                  : old
              })
            }
            onImages={(images) =>
              setDrafts((old) => {
                const key = `${taskId}:${node.id}`
                return {
                  ...old,
                  [key]: { ...(old[key] ?? { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }), images },
                }
              })
            }
            onDraft={(text) =>
              setDrafts((old) => ({
                ...old,
                [`${taskId}:${node.id}`]: {
                  ...(old[`${taskId}:${node.id}`] ?? { agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }),
                  text,
                },
              }))
            }
          />
        ) : selectedStep && (selectedStep.prompt?.trim() || selectedStep.withConversation) ? (
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div data-node-status className="flex shrink-0 flex-col">
              <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-x-2.5 gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:px-5">
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="shrink-0 font-mono text-[12px] font-semibold text-ink-500 tabular-nums">
                    {stepNumber(selectedStepIndex)}
                  </span>
                  <h2 title={selectedStep.id} className="min-w-0 truncate text-[14px] leading-snug font-semibold tracking-tight text-ink">
                    {selectedStep.title || selectedStep.id}
                  </h2>
                  <span className="inline-flex shrink-0 items-center gap-1.5 text-[12.5px] font-medium text-ink-700">
                    <AgentAvatar size="sm" id={selectedStep.agentId} />
                    <span className="max-w-[140px] truncate">{agentName(selectedStep.agentId)}</span>
                  </span>
                  {selectedStep.withConversation && <WithConversationBadge />}
                </div>
              </div>
              <StepPrompt key={selectedStep.id} prompt={selectedStep.prompt} />
            </div>
            <EmptyPane>
              <p>{t.tasks.noRecord}</p>
            </EmptyPane>
          </div>
        ) : (
          <EmptyPane>
            <p>{step ? t.tasks.noRecord : t.tasks.selectNode}</p>
          </EmptyPane>
        )}
      </Sheet>
    </PageFrame>
  )
}

// Right-pane empty state when the selected step has not been visited yet or the requested record does not exist.
function EmptyPane({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-1 items-center justify-center p-8 text-center text-[14.5px] font-medium text-ink-500">
      {children}
    </div>
  )
}
