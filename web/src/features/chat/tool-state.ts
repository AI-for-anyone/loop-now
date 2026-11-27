import type { Schemas } from '@/api/client'

// How a tool that never reported its end shows. While the turn goes on, a started tool is running. Once the turn has ended (completed,
// failed or stopped), it is “已打断” (Interrupted) when a person's interrupt cut it off, and otherwise “没有结果” (No result): the runtime
// never said how it ended (Antigravity moves a long command to the background, and its end may come after the turn), so how it went is
// unknown. The server closes such tools as no_result when the turn ends; records saved before that still have them on started.
export type UnfinishedTool = 'running' | 'interrupted' | 'no_result'
export type ShownToolState = Schemas['ToolState'] | 'interrupted'

export interface ToolGroup {
  key: string
  toolName: string
  state: ShownToolState
  count: number
}

const ENDED: ReadonlySet<Schemas['RunStatus']> = new Set(['completed', 'failed', 'interrupted'])

export function unfinishedTool(run: { status: Schemas['RunStatus']; endReason?: Schemas['Run']['endReason'] }): UnfinishedTool {
  if (!ENDED.has(run.status)) return 'running'
  return run.status === 'interrupted' && run.endReason === 'user_interrupt' ? 'interrupted' : 'no_result'
}

// The state a tool shows: started and no_result follow the turn (see unfinishedTool); succeeded and failed are what the runtime reported.
export function shownState(state: Schemas['ToolState'], unfinished: UnfinishedTool): ShownToolState {
  if (state !== 'started' && state !== 'no_result') return state
  if (unfinished === 'interrupted') return 'interrupted'
  return state === 'no_result' || unfinished === 'no_result' ? 'no_result' : 'started'
}

// Collapse state transitions per toolCallId (preserving first appearance), then group calls with the same tool name and shown state.
export function groupTools(
  events: Schemas['ToolEvent'][],
  unfinished: UnfinishedTool,
  exclude?: ReadonlySet<string>,
): ToolGroup[] {
  const latest = new Map<string, Schemas['ToolEvent']>()
  for (const event of events) if (!exclude?.has(event.toolCallId)) latest.set(event.toolCallId, event)
  const groups = new Map<string, ToolGroup>()
  for (const tool of latest.values()) {
    const state = shownState(tool.state, unfinished)
    const key = `${tool.toolName}\0${state}`
    const existing = groups.get(key)
    if (existing) existing.count += 1
    else groups.set(key, { key, toolName: tool.toolName, state, count: 1 })
  }
  return [...groups.values()]
}
