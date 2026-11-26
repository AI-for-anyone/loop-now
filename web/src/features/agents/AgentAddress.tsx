import { useState } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { AtSignIcon, CheckIcon, CopyIcon, TriangleAlertIcon } from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// The Agent's Inbox address: for an Agent in a project it is the full address (org name/project name/Agent name); for a
// platform Agent it is just the name.
// Default variant renders inline with a copy button; compact variant fits inside the 48px workspace header as a subtle icon
// trigger opening a popover with the full @address + copy action (or an amber warning indicator when the address is missing).
export function AgentAddress({ address, variant = 'default' }: { address: string | null; variant?: 'default' | 'compact' }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (!address) return
    try {
      await navigator.clipboard.writeText(address)
    } catch {
      // If the browser doesn't allow writing to the clipboard, leave things as they are; the person can still select and copy by hand.
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  if (variant === 'compact') {
    if (!address) {
      return (
        <PopoverPrimitive.Root>
          <PopoverPrimitive.Trigger
            type="button"
            aria-label={t.agents.address.label}
            title={t.agents.address.missing}
            data-address-missing
            className="inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-ticket border border-pink-text/30 bg-tint-yellow text-pink-text transition-colors outline-none hover:bg-tint-pink-2 focus-visible:shadow-focus [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink"
          >
            <TriangleAlertIcon aria-hidden="true" className="size-3.5" strokeWidth={2.2} />
          </PopoverPrimitive.Trigger>
          <PopoverPrimitive.Portal>
            <PopoverPrimitive.Content
              side="bottom"
              align="end"
              sideOffset={8}
              collisionPadding={12}
              className="z-50 w-72 rounded-card border border-hairline-strong bg-surface-elevated p-3 text-[12.5px] leading-snug text-ink-700 shadow-floating outline-none [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-bold"
            >
              <p>{t.agents.address.missing}</p>
            </PopoverPrimitive.Content>
          </PopoverPrimitive.Portal>
        </PopoverPrimitive.Root>
      )
    }

    return (
      <PopoverPrimitive.Root>
        <PopoverPrimitive.Trigger
          type="button"
          aria-label={t.agents.address.label}
          title={address}
          data-address-trigger
          className={cn(
            'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-ticket border border-hairline bg-surface-subtle px-2 text-[12px] font-medium text-ink-500 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus aria-expanded:bg-surface-active aria-expanded:text-ink',
            '[[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:hover:bg-tint-blue',
          )}
        >
          <AtSignIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
          <span className="sr-only">{address}</span>
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            side="bottom"
            align="end"
            sideOffset={8}
            collisionPadding={12}
            className="z-50 flex max-w-[min(360px,calc(100vw-24px))] flex-col gap-1.5 rounded-card border border-hairline-strong bg-surface-elevated p-3 shadow-floating outline-none [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised"
          >
            <span className="text-[11px] font-semibold tracking-wide text-ink-500 uppercase">{t.agents.address.label}</span>
            <div className="flex min-w-0 items-center gap-2 rounded-input border border-hairline bg-surface-subtle py-1 pr-1 pl-2.5 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent">
              <span className="min-w-0 flex-1 font-mono text-[12px] leading-5 [overflow-wrap:anywhere] text-ink select-all">{address}</span>
              <button
                type="button"
                aria-label={`${t.common.copy}${t.agents.address.label}`}
                title={copied ? t.common.copied : t.common.copy}
                onClick={() => void copy()}
                className="inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-stamp text-ink-700 outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus [[data-skin=plate]_&]:hover:bg-tint-blue"
              >
                {copied ? <CheckIcon aria-hidden="true" className="size-3.5 text-green-text" strokeWidth={2.8} /> : <CopyIcon aria-hidden="true" className="size-3.5" strokeWidth={2.2} />}
                <span role="status" className="sr-only">
                  {copied ? t.common.copied : ''}
                </span>
              </button>
            </div>
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>
    )
  }

  if (!address) return <p className="max-w-[560px] text-[12.5px] leading-snug font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{t.agents.address.missing}</p>
  return (
    <span className="inline-flex max-w-full min-w-0 items-center gap-2 rounded-stamp border border-hairline bg-surface-subtle pr-[3px] pl-[9px] text-[12px] leading-[22px] [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent">
      <span className="sr-only">{t.agents.address.label}</span>
      <span className="min-w-0 font-mono [overflow-wrap:anywhere] text-ink-700 select-all">{address}</span>
      <button
        type="button"
        aria-label={`${t.common.copy}${t.agents.address.label}`}
        title={copied ? t.common.copied : t.common.copy}
        onClick={() => void copy()}
        className="inline-flex size-[18px] shrink-0 cursor-pointer items-center justify-center rounded-stamp text-ink outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:hover:bg-tint-blue"
      >
        {copied ? <CheckIcon aria-hidden="true" className="size-3" strokeWidth={3} /> : <CopyIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />}
        <span role="status" className="sr-only">
          {copied ? t.common.copied : ''}
        </span>
      </button>
    </span>
  )
}
