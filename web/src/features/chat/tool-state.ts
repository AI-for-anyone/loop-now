import type { Schemas } from '@/api/client'

// How a tool that never reported its end shows. While the turn goes on, a started tool is running. Once the turn has ended (completed,
// failed or stopped), it is “已打断” (Interrupted) when a person's interrupt cut it off, and otherwise “没有结果” (No result): the runtime
// never said how it ended (Antigravity moves a long command to the background, and its end may come after the turn), so how it went is
// unknown. The server closes such tools as no_result when the turn ends; records saved before that still have them on started.
export type UnfinishedTool = 'running' | 'interrupted' | 'no_result'
const ENDED: ReadonlySet<Schemas['RunStatus']> = new Set(['completed', 'failed', 'interrupted'])

export function unfinishedTool(run: { status: Schemas['RunStatus']; endReason?: Schemas['Run']['endReason'] }): UnfinishedTool {
  if (!ENDED.has(run.status)) return 'running'
  return run.status === 'interrupted' && run.endReason === 'user_interrupt' ? 'interrupted' : 'no_result'
}
