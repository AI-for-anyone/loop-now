import { useSyncExternalStore } from 'react'
import { reachability } from '@/api/client'
import { useHealth } from '@/api/queries'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type ConnectionState = 'checking' | 'connected' | 'degraded' | 'unreachable'

// 每种状态都有文字；颜色只作辅助。
const stateStyle: Record<ConnectionState, { label: string; chip: string; dot: string }> = {
  checking: { label: t.app.topbar.checking, chip: 'text-white', dot: 'bg-white/60' },
  connected: { label: t.app.topbar.connected, chip: 'text-white', dot: 'bg-tint-green' },
  degraded: { label: t.settings.healthDegraded, chip: 'bg-tint-yellow px-3 text-ink shadow-[inset_0_3px_0_var(--ink)]', dot: 'bg-ink' },
  unreachable: { label: t.app.topbar.unreachable, chip: 'bg-tint-red px-3 text-red-text', dot: 'bg-red' },
}

// 只显示 API 地址的主机和端口；地址无法解析时原样显示。
function apiHost(apiBase: string): string {
  try {
    return new URL(apiBase).host
  } catch {
    return apiBase
  }
}

// 左侧栏底部的连接状态：请求没有到达服务端，或健康接口不可用（503 以外的错误）时为无法连接；
// 健康检查报告有依赖不可用（503）时为部分依赖不可用；首次查询还没有结果时为检查中。
export function ConnectionStatus({ className }: { className?: string }) {
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const health = useHealth()
  const state: ConnectionState =
    !reachable || health.isError ? 'unreachable' : health.isPending ? 'checking' : health.data.ok ? 'connected' : 'degraded'
  const style = stateStyle[state]

  return (
    <div
      role="status"
      aria-label={t.settings.health}
      className={cn('inline-flex min-h-7 items-center gap-2 rounded-full text-[12.5px] font-black', style.chip, className)}
    >
      <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full', style.dot)} />
      <span>{style.label}</span>
      <span className="font-mono text-[11.5px] font-bold">{apiHost(config.apiBase)}</span>
    </div>
  )
}
