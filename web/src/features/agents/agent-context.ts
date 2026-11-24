import { useOutletContext } from 'react-router'
import type { Schemas } from '@/api/client'

// The state shown by the status sticker in the header: whether the Agent is busy, and the phase text of the current run (null when no
// run is in progress).
export interface AgentStickerState {
  busy: boolean
  phase: string | null
  sleep?: Schemas['SleepInfo'] | null
}

// The Agent detail page passes the current Agent to the “对话” (Chat) and “配置” (Config) tabs through the route outlet.
// Once the Chat tab receives a snapshot, it hands the live status to the header sticker with reportLiveStatus; when it is
// left it passes null, and the header goes back to the polled status.
export interface AgentOutletContext {
  agent: Schemas['Agent']
  reportLiveStatus: (state: AgentStickerState | null) => void
}

export function useAgentContext() {
  return useOutletContext<AgentOutletContext>()
}
