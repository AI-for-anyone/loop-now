import { useEffect, useRef, useSyncExternalStore } from 'react'
import { useQueryClient, type Query, type QueryClient } from '@tanstack/react-query'
import { RotateCwIcon, WifiOffIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api, reachability } from '@/api/client'
import { useContextInfo } from '@/api/queries'
import { config } from '@/config'
import { t } from '@/i18n'
import { isUnreachable } from '@/lib/errors'

const failedToConnect = (query: Query) => isUnreachable(query.state.error)

// 重新获取因无法连接而失败的查询。进行中的请求先取消再重发：它可能是断连时发出的，沿用它的结果会把失败
// 延续到下一次轮询；重试和重连都要向恢复后的服务端重新发请求。被取消的请求不影响可达性。
const refetchFailed = (client: QueryClient) => client.refetchQueries({ type: 'active', predicate: failedToConnect }, { cancelRefetch: true })

// 重试时另外探测一次健康检查，只有修改失败时也能确认连接是否恢复。
async function retry(client: QueryClient) {
  await Promise.allSettled([refetchFailed(client), api.GET('/health')])
}

// 任一请求没有到达服务端时显示全局的连接提示，之后任一请求收到响应即消失。
export function ConnectionBanner() {
  // 启动时读取默认上下文，同时作为第一次连接探测。
  useContextInfo()
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const client = useQueryClient()
  const wasReachable = useRef(reachable)

  // 连接恢复后重新获取断连期间失败的查询，不必等窗口重新获得焦点。
  useEffect(() => {
    if (reachable && !wasReachable.current) void refetchFailed(client)
    wasReachable.current = reachable
  }, [reachable, client])

  if (reachable) return null
  // 全宽红色横幅：浅红网点底、底部藏青线。断线图标的倾斜画在 ::before 上。
  return (
    <div
      role="alert"
      className="riso-halftone-red flex shrink-0 flex-wrap items-center gap-x-6 gap-y-3.5 bg-tint-red px-4 py-[22px] text-ink shadow-[inset_0_-3px_0_var(--ink)] sm:px-[30px]"
    >
      <span
        aria-hidden="true"
        className="riso-tilt flex size-16 flex-none items-center justify-center rounded-full text-red-text [--tilt-shadow:3px_3px_0_var(--red-text)] [--tilt:-6deg]"
      >
        <WifiOffIcon className="size-[30px]" strokeWidth={2.4} />
      </span>
      <div className="flex min-w-0 flex-[1_1_520px] flex-col gap-1">
        <h2 className="font-display-cn text-[34px] leading-[1.15] font-normal text-red-text">{t.connection.unreachableTitle}</h2>
        <p className="text-[14.5px] leading-[1.75] break-words">{t.connection.unreachableBody(config.apiBase, window.location.origin)}</p>
      </div>
      <Button variant="ink" size="lg" className="flex-none shadow-[3px_3px_0_var(--riso-pink)]" onClick={() => void retry(client)}>
        <RotateCwIcon />
        {t.common.retry}
      </Button>
    </div>
  )
}
