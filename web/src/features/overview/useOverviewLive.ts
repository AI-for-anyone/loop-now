import { useEffect, useReducer, useState } from 'react'
import { connectOverviewEvents } from '@/api/events'
import type { ScopeQuery } from '@/lib/scope'
import { initialOverviewState, overviewReducer } from './overview-state'

// 订阅概览事件流。概览事件只在运行状态变化时推送，频率低，逐条更新界面。
// 永久断开后一直按退避间隔重连，新连接由服务端先推送快照。
// 范围在挂载时确定：概览页按范围给这一块设 key，换范围时重新挂载并重新连接。
export function useOverviewLive(scope: ScopeQuery = {}) {
  const [state, dispatch] = useReducer(overviewReducer, initialOverviewState)
  const [initialScope] = useState(scope)
  useEffect(() => {
    return connectOverviewEvents({
      scope: initialScope,
      onEvent: (event) => dispatch({ type: 'event', event, at: new Date().toISOString() }),
      onState: (connection) => dispatch({ type: 'connection', state: connection }),
      onClosed: async () => true,
    })
  }, [initialScope])
  return state
}
