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
      <span id={labelId} className="text-[12.5px] leading-4 font-medium tracking-[-0.005em] text-ink-700 [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:leading-snug [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-normal [[data-skin=plate]_&]:text-ink">
        {label}
      </span>
      {/* Read-only: subtle hairline container matching h-9 Geist inputs in mono skin, dashed h-11 ink box with yellow copy button in Plate skin. */}
      <div className="flex h-9 items-center gap-2 rounded-input border border-hairline bg-surface-subtle pr-1 pl-3 [[data-skin=plate]_&]:h-11 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:pr-1.5 [[data-skin=plate]_&]:pl-3.5">
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-ink-700 select-all" title={id}>
          {id}
        </span>
        <Button
          type="button"
          size="xs"
          variant="secondary"
          className="h-6.5 px-2.5 text-[12px] [[data-skin=plate]_&]:h-8 [[data-skin=plate]_&]:rounded-[8px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:px-3 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:hover:bg-riso-yellow"
          aria-describedby={labelId}
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
    </div>
  )
}
