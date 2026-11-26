import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { describeError, requestIdOf } from '@/lib/errors'
import { cn } from '@/lib/utils'

// The request ID (response header Loop-Now-Request-Id) with a copy button, so a failed request can be handed to developers to investigate.
export function RequestId({ id }: { id: string }) {
  const labelId = useId()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id)
    } catch {
      // When the browser does not allow writing to the clipboard, leave things as they are; the person can still select and copy by hand.
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <span id={labelId} className="font-medium text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink">
        {t.overview.requestId}
      </span>
      <code className="rounded-stamp border border-hairline bg-surface-control px-2.5 py-px font-mono text-[13px] [overflow-wrap:anywhere] select-all [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white">{id}</code>
      <Button
        type="button"
        size="xs"
        variant="secondary"
        className="h-[30px] px-3 text-[12.5px] [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:hover:bg-white"
        aria-describedby={labelId}
        onClick={() => void copy()}
      >
        {copied ? t.common.copied : t.common.copy}
      </Button>
    </span>
  )
}

// One block failed to load: clean hairline error card in mono skin, light red halftone banner in Plate skin, with the reason, the request
// ID (when there is one) and a retry, placed only in the block that failed.
export function SectionError({ title, error, onRetry, className }: { title: string; error: unknown; onRetry: () => void; className?: string }) {
  const requestId = requestIdOf(error)
  return (
    <div
      role="alert"
      className={cn(
        'riso-halftone-red flex flex-wrap items-center gap-x-5 gap-y-3 rounded-card border border-red-text/25 bg-tint-red px-5 py-4 text-ink [[data-skin=plate]_&]:rounded-panel [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:px-[22px] [[data-skin=plate]_&]:py-[18px] [[data-skin=plate]_&]:shadow-print-ink',
        className,
      )}
    >
      <div className="flex min-w-0 flex-[1_1_420px] flex-col gap-1.5 text-[14px] leading-relaxed [[data-skin=plate]_&]:text-[14.5px] [[data-skin=plate]_&]:leading-[1.75]">
        <span className="text-[16px] leading-snug font-semibold text-red-text [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[28px] [[data-skin=plate]_&]:leading-[1.2] [[data-skin=plate]_&]:font-normal">
          {t.overview.sectionFailed(title)}
        </span>
        <span className="text-ink-700 [[data-skin=plate]_&]:text-ink">{t.overview.sectionFailedBody(describeError(error))}</span>
        {requestId && <RequestId id={requestId} />}
      </div>
      <Button variant="ink" size="lg" onClick={onRetry}>
        {t.common.retry}
      </Button>
    </div>
  )
}
