import { useId, useState } from 'react'
import { ChevronRightIcon, MessagesSquareIcon, ScrollTextIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Badge shown on workflow steps that opted into starting from a copy of their Agent's regular conversation.
export function WithConversationBadge({ compact = false, className }: { compact?: boolean; className?: string }) {
  if (compact) {
    return (
      <span
        data-step-with-conversation
        title={t.tasks.withConversationHint}
        className={cn(
          'mt-0.5 inline-flex w-fit max-w-full items-center gap-1 rounded-stamp border border-hairline-strong bg-surface-control px-1.5 py-0.5 text-[10.5px] leading-tight font-medium text-ink',
          className,
        )}
      >
        <MessagesSquareIcon aria-hidden="true" className="size-3 shrink-0 text-ink-700" strokeWidth={2} />
        <span className="truncate">{t.tasks.withConversation}</span>
      </span>
    )
  }
  return (
    <Badge
      variant="chip"
      tone="info"
      data-step-with-conversation
      title={t.tasks.withConversationHint}
      className={className}
    >
      <MessagesSquareIcon aria-hidden="true" strokeWidth={2.2} />
      <span>{t.tasks.withConversation}</span>
    </Badge>
  )
}

// Standing prompt written by the Manager for a workflow step: collapsed by default, expands to Markdown.
// Renders nothing when the step has no prompt (or only whitespace), leaving steps without a prompt unchanged.
export function StepPrompt({ prompt }: { prompt: string | undefined }) {
  const [open, setOpen] = useState(false)
  const panelId = useId()
  if (!prompt?.trim()) return null

  return (
    <section
      aria-label={t.tasks.stepPrompt}
      data-step-prompt
      className="border-b border-hairline bg-surface-subtle/35"
    >
      <button
        type="button"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full cursor-pointer items-center gap-2 px-4 py-1.5 text-left text-[12px] font-medium text-ink-700 transition-colors outline-none hover:bg-surface-hover/50 hover:text-ink focus-visible:shadow-focus sm:px-5"
      >
        <ChevronRightIcon
          aria-hidden="true"
          className={cn('size-3.5 shrink-0 text-ink-500 transition-transform', open && 'rotate-90')}
          strokeWidth={2.2}
        />
        <ScrollTextIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-500" strokeWidth={2} />
        <span className="font-semibold text-ink">{t.tasks.stepPrompt}</span>
        <span className="ml-auto shrink-0 text-[11.5px] font-medium text-ink-500">
          {open ? t.tasks.page.collapse : t.tasks.page.expand}
        </span>
      </button>
      {open && (
        <div
          id={panelId}
          data-step-prompt-body
          className="max-h-[38vh] overflow-y-auto overscroll-contain border-t border-hairline bg-surface-canvas px-4 py-3 sm:px-5"
        >
          <div className="max-w-[800px]">
            <Markdown text={prompt} leadLabels={false} />
          </div>
        </div>
      )}
    </section>
  )
}
