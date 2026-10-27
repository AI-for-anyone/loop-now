import { useOutletContext } from 'react-router'
import type { Schemas } from '@/api/client'

// 头部状态贴纸显示的状态：会话是否丢失、是否正忙，以及当前执行的阶段文字（没有进行中的执行时为 null）。
export interface AgentStickerState {
  sessionMissing: boolean
  busy: boolean
  phase: string | null
  sleep?: Schemas['SleepInfo'] | null
}

// Agent 详情页通过路由出口把当前 Agent 交给“对话”和“配置”页签。
// 对话页收到快照后用 reportLiveStatus 把实时状态交给头部贴纸，离开时传 null，头部回到轮询的状态。
export interface AgentOutletContext {
  agent: Schemas['Agent']
  reportLiveStatus: (state: AgentStickerState | null) => void
}

export function useAgentContext() {
  return useOutletContext<AgentOutletContext>()
}
