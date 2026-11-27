import { cn } from '@/lib/utils'

// Shared look for dialogs and drawers driven by semantic theme tokens.

export const overlayClass = 'fixed inset-0 isolate z-50 bg-black/45 backdrop-blur-[1px]'

export type DialogKind = 'create' | 'danger' | 'confirm' | 'edit' | 'view' | 'token' | 'choose' | 'install' | 'update' | 'action' | 'pull'

// The outer layer only handles position and width (the caller's className goes here, e.g. sm:max-w-[560px]).
export const dialogContentClass =
  'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col text-[15px] text-ink outline-none'

// Outer non-scrolling dialog container: holds the fixed topbar (kind badge + close button) above the independently scrolling body.
export const dialogPaperClass =
  'relative flex min-h-0 flex-col rounded-dialog border border-hairline-strong bg-surface-elevated shadow-dialog'

// Non-scrolling top bar inside the dialog shell so scrolled dialog content never slides underneath the close button.
export const dialogTopbarClass = 'flex shrink-0 items-center justify-between gap-3 px-6 pt-4 pb-2 max-sm:px-[18px]'

// Scrollable dialog body below the top bar.
export const dialogBodyClass =
  'riso-scrollbar grid min-h-0 flex-1 content-start gap-5 overflow-y-auto overscroll-contain rounded-b-dialog px-6 pt-2 pb-6 max-sm:px-[18px]'

export const dialogTabClass = (kind: DialogKind) =>
  cn(
    'inline-flex items-center gap-1.5 rounded-stamp border px-2 py-0.5 text-[11.5px] font-medium',
    kind === 'danger' ? 'border-red-text/25 bg-tint-red text-red-text' : 'border-hairline bg-surface-subtle text-ink-500',
  )

// Close button in the top right: clean ghost icon button.
export const closeStampClass =
  'inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-ticket border border-transparent bg-transparent text-ink-500 outline-none transition-colors hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus'

export const dialogFooterClass = 'flex flex-wrap justify-end gap-2.5 border-t border-hairline pt-4'

// The button bar at the bottom of a drawer.
export const sheetFooterClass = 'flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2.5 border-t border-hairline bg-surface-subtle px-6 py-3.5'
