import { useRef, useState } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { useRemoveTaskWorkspace } from '@/api/tasks'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

const copy = t.tasks.workspace
const stateTone = { pending: 'busy', active: 'info', removed: 'neutral', kept: 'warning' } as const

// The task's git worktree, between the header and the sheet: one strip with the branch and where it started, then the repository and
// the worktree folder. A kept worktree adds a yellow notice that says in numbers what removing it would lose, with the person's Remove
// action. A task without a worktree renders nothing, so its page looks as it always did.
export function TaskWorkspace({ taskId, projectId, workspace }: { taskId: string; projectId: string; workspace: Schemas['TaskWorkspace'] | null }) {
  if (!workspace) return null
  const from = workspace.baseBranch ? copy.from(workspace.baseBranch, workspace.baseCommit?.slice(0, 7) ?? null) : null
  return <section aria-label={copy.label} className="mb-3 grid min-w-0 gap-2 rounded-card bg-paper-raised px-4 py-3 shadow-[inset_0_0_0_1.5px_var(--ink)]">
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
      <h2 className="text-[13.5px] font-black">{copy.label}</h2>
      <Badge variant="chip" tone={stateTone[workspace.state]}>{copy.states[workspace.state]}</Badge>
      <span className="min-w-0 font-mono text-[13px] font-bold break-all text-riso-blue" title={copy.branch}>{workspace.branch}</span>
      {from && <span className="font-mono text-[12px] text-ink-500">{from}</span>}
    </div>
    <dl className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-[12.5px]">
      <dt className="font-bold text-ink-700">{copy.repository}</dt>
      <dd className="min-w-0 font-mono break-all text-ink-700">{workspace.repository}</dd>
      <dt className="font-bold text-ink-700">{copy.path}</dt>
      <dd className="flex min-w-0 items-start gap-2">
        <span className={`min-w-0 font-mono break-all ${workspace.state === 'removed' ? 'text-ink-500 line-through' : 'text-ink-700 select-all'}`} title={workspace.path}>{workspace.path}</span>
        {workspace.state !== 'removed' && <CopyPath path={workspace.path} />}
      </dd>
    </dl>
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
  return <Button type="button" size="xs" variant="secondary" className="h-7 shrink-0 rounded-[8px] border-2 border-ink bg-riso-yellow px-2.5 text-[12px] hover:bg-riso-yellow" onClick={() => void write()}>
    {copied ? t.common.copied : t.common.copy}
  </Button>
}

function Kept({ taskId, projectId, workspace }: { taskId: string; projectId: string; workspace: Schemas['TaskWorkspace'] }) {
  const [asking, setAsking] = useState(false)
  const actionId = useRef('')
  const remove = useRemoveTaskWorkspace(taskId, projectId)
  const files = workspace.kept?.uncommittedFiles ?? 0
  const commits = workspace.kept?.unpushedCommits ?? 0
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
      {files || commits
        ? <><p className="font-bold">{copy.kept}</p><ul className="mt-0.5 list-disc pl-5">{files > 0 && <li>{copy.files(files)}</li>}{commits > 0 && <li>{copy.commits(commits)}</li>}</ul></>
        : <p className="font-bold">{copy.keptUnknown}</p>}
    </div>
    <Button variant="danger-outline" size="xs" className="shrink-0 justify-self-start px-4" disabled={remove.isPending} onClick={() => { actionId.current = crypto.randomUUID(); setAsking(true) }}>{copy.remove}</Button>
    <ConfirmDialog open={asking} onOpenChange={setAsking} title={copy.removeTitle} body={copy.removeBody} confirmLabel={copy.remove} destructive onConfirm={confirm}>
      {(files > 0 || commits > 0) && <ul className="grid gap-1 text-sm">
        {files > 0 && <li className="font-bold text-red-text">{copy.filesLost(files)}</li>}
        {commits > 0 && <li>{copy.commitsStay(commits, workspace.branch)}</li>}
      </ul>}
    </ConfirmDialog>
  </div>
}
