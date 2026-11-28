import { useRef, useState } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { useRemoveTaskWorkspace } from '@/api/tasks'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const copy = t.tasks.workspace
const stateTone = { pending: 'busy', active: 'info', removed: 'neutral', kept: 'warning' } as const
const mergeTone = { merged: 'success', conflict: 'danger', skipped: 'warning' } as const

// The task's workspace, between the header and the sheet. Git mode: one strip with the branch and where it started, then the repository
// and the worktree folder. Copy mode: the folder that was copied and how much, the copy's folder, and a line saying the original is not
// changed. Either may carry the server's note on why the mode is not the one the folder suggests. Parallel branch workspaces (worktrees
// or copies) appear side by side below with their state and merge outcome. A kept workspace adds a yellow notice
// that says in numbers what removing it would lose, with the person's Remove action. A task without a workspace renders nothing, so its
// page looks as it always did.
export function TaskWorkspace({
  taskId,
  projectId,
  workspace,
  steps,
}: {
  taskId: string
  projectId: string
  workspace: Schemas['TaskWorkspace'] | null
  steps?: Schemas['TaskWorkflowStep'][]
}) {
  if (!workspace) return null
  const git = workspace.git
  const isCopy = workspace.mode === 'copy'
  const from = git?.baseBranch ? copy.from(git.baseBranch, git.baseCommit?.slice(0, 7) ?? null) : null
  const label = isCopy ? copy.copyLabel : copy.label
  const branches = workspace.branches ?? []
  return <section aria-label={label} className="mb-2.5 grid min-w-0 gap-2 rounded-card border border-hairline-strong bg-surface-elevated px-4 py-3 shadow-surface">
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      <h2 className="text-[13.5px] font-semibold text-ink">{label}</h2>
      <Badge variant="chip" tone={stateTone[workspace.state]}>{copy.states[workspace.state]}</Badge>
      {git && <span className="min-w-0 font-mono text-[13px] font-semibold break-all text-riso-blue" title={copy.branch}>{git.branch}</span>}
      {from && <span className="font-mono text-[12px] text-ink-500">{from}</span>}
      {isCopy && workspace.copy && <span className="font-mono text-[12px] text-ink-500">{copy.size(workspace.copy.files, t.common.bytes(workspace.copy.bytes))}</span>}
    </div>
    {workspace.modeNote && <p role="note" className="rounded-input border border-hairline bg-tint-blue px-3 py-1.5 text-[12.5px] font-medium text-ink">{workspace.modeNote}</p>}
    <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-[12.5px]">
      <dt className="font-semibold text-ink-700">{isCopy ? copy.copyOf : copy.repository}</dt>
      <dd className="min-w-0 font-mono break-all text-ink-700">{git?.repository ?? workspace.folder}</dd>
      <dt className="font-semibold text-ink-700">{copy.path}</dt>
      <dd className="flex min-w-0 items-start gap-2">
        <span className={`min-w-0 font-mono break-all ${workspace.state === 'removed' ? 'text-ink-500 line-through' : 'text-ink-700 select-all'}`} title={workspace.path}>{workspace.path}</span>
        {workspace.state !== 'removed' && <CopyPath path={workspace.path} />}
      </dd>
    </dl>
    {isCopy && workspace.state !== 'removed' && <p className="text-[12px] text-ink-500">{copy.copyHint}</p>}
    {branches.length > 0 && (
      <div data-workspace-branches className="grid min-w-0 gap-1.5 border-t border-hairline pt-2">
        <h3 className="text-[12px] font-semibold text-ink-700">{isCopy ? copy.branchCopies : copy.branchWorkspaces}</h3>
        <ul className={cn('grid min-w-0 gap-2', branches.length > 1 && 'sm:grid-cols-2')}>
          {branches.map((branch) => {
            const stepTitle = steps?.find((s) => s.id === branch.step)?.title || branch.step
            const removed = branch.state === 'removed'
            return (
              <li
                key={branch.step}
                data-workspace-branch={branch.step}
                className="grid min-w-0 gap-1 rounded-input border border-hairline bg-surface-subtle/50 px-3 py-2 text-[12px]"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="font-semibold text-ink" title={branch.step}>{stepTitle}</span>
                  <Badge variant="chip" tone={stateTone[branch.state]}>{copy.states[branch.state]}</Badge>
                  {branch.merge && <Badge variant="chip" tone={mergeTone[branch.merge]}>{copy.mergeStates[branch.merge]}</Badge>}
                </div>
                {branch.git && (
                  <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono text-[11.5px]">
                    <span className="min-w-0 font-semibold break-all text-riso-blue">{branch.git.branch}</span>
                    {branch.git.baseCommit && <span className="shrink-0 text-ink-500">@ {branch.git.baseCommit.slice(0, 7)}</span>}
                  </div>
                )}
                <div className="flex min-w-0 items-start gap-2">
                  <span className={cn('min-w-0 font-mono text-[11.5px] break-all', removed ? 'text-ink-500 line-through' : 'text-ink-700 select-all')} title={branch.path}>
                    {branch.path}
                  </span>
                  {!removed && <CopyPath path={branch.path} />}
                </div>
              </li>
            )
          })}
        </ul>
      </div>
    )}
    {workspace.state === 'kept' && <Kept taskId={taskId} projectId={projectId} workspace={workspace} />}
  </section>
}

function CopyPath({ path }: { path: string }) {
  const [copied, setCopied] = useState(false)
  const write = async () => {
    try {
      await navigator.clipboard.writeText(path)
    } catch {
      // The browser refused the clipboard; the path is still selectable by hand.
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return <Button type="button" size="xs" variant="secondary" className="h-7 shrink-0 px-2.5 text-[12px]" onClick={() => void write()}>
    {copied ? t.common.copied : t.common.copy}
  </Button>
}

function Kept({ taskId, projectId, workspace }: { taskId: string; projectId: string; workspace: Schemas['TaskWorkspace'] }) {
  const [asking, setAsking] = useState(false)
  const actionId = useRef('')
  const remove = useRemoveTaskWorkspace(taskId, projectId)
  const isCopy = workspace.mode === 'copy'
  const files = workspace.kept?.uncommittedFiles ?? 0
  const commits = workspace.kept?.unpushedCommits ?? 0
  const changed = isCopy ? workspace.kept?.changedFiles ?? 0 : 0
  const confirm = async () => {
    try {
      await remove.mutateAsync(actionId.current)
    } catch (error) {
      // Someone else removed it, or the task changed: the mutation reads the task again, so there is nothing to report.
      if (error instanceof ApiError && error.code === 'task_workspace_not_kept') return
      throw error
    }
  }
  return <div role="status" className="grid gap-2 rounded-input bg-tint-yellow px-3 py-2.5 text-[13px] sm:flex sm:items-center sm:justify-between">
    <div className="min-w-0">
      {files || commits || changed
        ? <><p className="font-bold">{copy.kept}</p><ul className="mt-0.5 list-disc pl-5">{files > 0 && <li>{copy.files(files)}</li>}{commits > 0 && <li>{copy.commits(commits)}</li>}{changed > 0 && <li>{copy.changed(changed)}</li>}</ul></>
        : <p className="font-bold">{copy.keptUnknown}</p>}
    </div>
    <Button variant="danger-outline" size="xs" className="shrink-0 justify-self-start px-4" disabled={remove.isPending} onClick={() => { actionId.current = crypto.randomUUID(); setAsking(true) }}>{isCopy ? copy.removeCopy : copy.remove}</Button>
    <ConfirmDialog open={asking} onOpenChange={setAsking} title={isCopy ? copy.removeCopyTitle : copy.removeTitle} body={isCopy ? copy.removeCopyBody : copy.removeBody} confirmLabel={isCopy ? copy.removeCopy : copy.remove} destructive onConfirm={confirm}>
      {(files > 0 || commits > 0 || changed > 0) && <ul className="grid gap-1 text-sm">
        {changed > 0 && <li className="font-bold text-red-text">{copy.changedLost(changed)}</li>}
        {files > 0 && <li className="font-bold text-red-text">{copy.filesLost(files)}</li>}
        {commits > 0 && <li>{copy.commitsStay(commits, workspace.git?.branch ?? '')}</li>}
      </ul>}
    </ConfirmDialog>
  </div>
}
