import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'

// Internal ID: read-only, in a monospace font, with a copy button next to it that briefly shows "Copied" after a successful copy.
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
      <span id={labelId} className="text-[13.5px] leading-snug font-medium text-ink [[data-skin=plate]_&]:font-black">
        {label}
      </span>
      {/* Read-only: subtle hairline container in mono skin, dashed ink box with yellow copy button in Plate skin. */}
      <div className="flex h-11 items-center gap-2 rounded-input border border-hairline bg-surface-subtle pr-1.5 pl-3.5 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent">
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-ink-700 select-all" title={id}>
          {id}
        </span>
        <Button
          type="button"
          size="xs"
          variant="secondary"
          className="h-8 px-3 text-[12.5px] [[data-skin=plate]_&]:rounded-[8px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:hover:bg-riso-yellow"
          aria-describedby={labelId}
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
    </div>
  )
}
