import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// Internal ID: read-only, in a monospace font, with a copy button next to it that briefly shows “已复制” (Copied) after a successful copy.
// Shared by the Agent and project config pages.
export function IdField({ label, id }: { label: string; id: string }) {
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
    // The column can shrink to the container's width: the ID does not wrap and is truncated inside the box, so it does not widen its block.
    <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] content-start gap-1.5">
      <span id={labelId} className="text-[13.5px] leading-snug font-black">
        {label}
      </span>
      {/* Read-only: a dashed box (not the solid line of an input), the ID in a monospace font, and a small yellow copy button on the right. */}
      <div className="flex h-11 items-center gap-2 rounded-input border-[1.5px] border-dashed border-ink/45 pr-1.5 pl-3.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-ink-700 select-all" title={id}>
          {id}
        </span>
        <Button
          type="button"
          size="xs"
          variant="secondary"
          className="h-8 rounded-[8px] border-2 border-ink bg-riso-yellow px-3 text-[12.5px] hover:bg-riso-yellow"
          aria-describedby={labelId}
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
    </div>
  )
}
