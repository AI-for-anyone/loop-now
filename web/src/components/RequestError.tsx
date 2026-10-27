import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { describeError, requestIdOf } from '@/lib/errors'
import { cn } from '@/lib/utils'

// 请求编号（响应头 Loop-Now-Request-Id）与复制按钮，便于把出错的请求交给开发排查。
export function RequestId({ id }: { id: string }) {
  const labelId = useId()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id)
    } catch {
      // 浏览器不允许写剪贴板时保持原样，人仍可以手动选中复制。
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <span id={labelId} className="font-black">
        {t.overview.requestId}
      </span>
      <code className="rounded-[6px] bg-white px-2.5 py-px font-mono text-[13px] [overflow-wrap:anywhere] select-all">{id}</code>
      <Button
        type="button"
        size="xs"
        variant="secondary"
        className="h-[30px] bg-white px-3 text-[12.5px] hover:bg-white"
        aria-describedby={labelId}
        onClick={() => void copy()}
      >
        {copied ? t.common.copied : t.common.copy}
      </Button>
    </span>
  )
}

// 某一块加载失败：浅红网点横幅，说明原因、请求编号（有时）和重试，只放在出错的那一块里。
export function SectionError({ title, error, onRetry, className }: { title: string; error: unknown; onRetry: () => void; className?: string }) {
  const requestId = requestIdOf(error)
  return (
    <div
      role="alert"
      className={cn(
        'riso-halftone-red flex flex-wrap items-center gap-x-5 gap-y-3 rounded-panel bg-tint-red px-[22px] py-[18px] shadow-print-ink',
        className,
      )}
    >
      <div className="flex min-w-0 flex-[1_1_420px] flex-col gap-1.5 text-[14.5px] leading-[1.75]">
        <span className="font-display-cn text-[28px] leading-[1.2] text-red-text">{t.overview.sectionFailed(title)}</span>
        <span>{t.overview.sectionFailedBody(describeError(error))}</span>
        {requestId && <RequestId id={requestId} />}
      </div>
      <Button variant="ink" size="lg" onClick={onRetry}>
        {t.common.retry}
      </Button>
    </div>
  )
}
