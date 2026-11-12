import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useRefreshRuntimes, useRuntimes } from '@/api/queries'
import { t } from '@/i18n'
import { runtimeProblem } from '@/lib/runtime'

const text = t.runtime.status
const loginTone = { logged_in: 'success', logged_out: 'warning', unknown: 'neutral' } as const

// 全局设置里的 Agent 运行时：Pi 随平台安装；Claude Code 看本机有没有 claude、能不能读出版本、有没有登录。
// 服务端缓存 30 秒，“重新检查”不用缓存。登录只给方式与订阅类型，邮箱与组织服务端不返回。
// 改版后是全局设置纸上一个编号分区的内容：说明与“重新检查”一行，下面每个运行时一张卡片。
export function RuntimesCard() {
  const runtimes = useRuntimes()
  const refresh = useRefreshRuntimes()
  return (
    <div className="grid gap-3.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <p className="min-w-0 flex-[1_1_280px] text-[13.5px] text-ink-700">{text.intro}</p>
        <Button
          size="sm"
          variant="secondary"
          className="h-[34px] rounded-full border-2 border-ink bg-paper-raised px-5 text-[13.5px] font-black"
          disabled={refresh.isPending || runtimes.isPending}
          onClick={() => refresh.mutate()}
        >
          {refresh.isPending ? text.refreshing : text.refresh}
        </Button>
      </div>
      <ErrorAlert error={refresh.error ?? runtimes.error} />
      {runtimes.isPending && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
      {runtimes.data?.runtimes.map((runtime) => <RuntimeRow key={runtime.id} runtime={runtime} />)}
    </div>
  )
}

function RuntimeRow({ runtime }: { runtime: Schemas['RuntimeStatus'] }) {
  const name = t.runtime.names[runtime.id]
  const problem = runtimeProblem(runtime)
  const login = runtime.login
  const account = login ? [login.authMethod, login.subscriptionType && text.subscription(login.subscriptionType)].filter(Boolean).join(' · ') : ''
  return (
    // 改版：白底、2px 藏青描边、藏青实色投影的卡片，名称是展示字，后面跟着状态胶囊与等宽的版本号。
    <section aria-label={name} className="mr-[5px] mb-[5px] flex min-w-0 flex-col gap-2.5 rounded-card border-2 border-ink bg-white px-[18px] py-3.5 shadow-[5px_5px_0_var(--ink)]">
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <span className="font-display-cn text-[26px] leading-[1.1]">{name}</span>
        {runtime.version && (
          <span className="text-[12.5px] text-ink-700">
            {text.version} <span className="font-mono text-ink">{runtime.version}</span>
          </span>
        )}
        <span className="ml-auto flex flex-wrap gap-1.5">
          <Badge tone={runtime.available ? 'success' : 'danger'}>{runtime.available ? text.available : text.unavailable}</Badge>
          {login && <Badge tone={loginTone[login.state]}>{text.login[login.state]}</Badge>}
        </span>
      </div>
      {(runtime.binaryPath || account) && (
        <dl className="grid grid-cols-[minmax(60px,auto)_minmax(0,1fr)] gap-x-3 gap-y-1 text-[13.5px]">
          {runtime.binaryPath && <><dt className="font-black text-ink-500">{text.path}</dt><dd className="min-w-0 font-mono text-[12.5px] break-all">{runtime.binaryPath}</dd></>}
          {account && <><dt className="font-black text-ink-500">{text.account}</dt><dd className="min-w-0 break-words">{account}</dd></>}
        </dl>
      )}
      {problem && (
        <Alert variant="warning" role="note" className="text-[13.5px]">
          <AlertDescription className="text-[13.5px] text-ink">{text.problems[problem]}</AlertDescription>
        </Alert>
      )}
      {login?.state === 'unknown' && <p className="text-[13px] text-ink-700">{text.unknownLogin}</p>}
      <p className="text-[12px] text-ink-500">{text.checkedAt(t.chat.time(runtime.checkedAt))}</p>
    </section>
  )
}
