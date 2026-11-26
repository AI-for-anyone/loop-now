import { cn } from '@/lib/utils'

// Shared look for dialogs and drawers: crisp elevated Swiss monochrome in mono skin; printed sheets with index tabs in Plate skin.

// Overlay: translucent dark scrim in mono skin; navy at 22% with halftone dots in Plate skin.
export const overlayClass =
  'fixed inset-0 isolate z-50 bg-black/45 backdrop-blur-[1px] [[data-skin=plate]_&]:bg-ink/[0.22] [[data-skin=plate]_&]:backdrop-blur-none [[data-skin=plate]_&]:[background-image:radial-gradient(rgb(255_255_255/.08)_1px,transparent_1.3px)] [[data-skin=plate]_&]:[background-size:6px_6px]'

// The dialog's kind: rendered as a compact pill inside the non-scrolling top bar in mono skin, and as a protruding folder tab in Plate skin.
export type DialogKind = 'create' | 'danger' | 'confirm' | 'edit' | 'view' | 'token' | 'choose' | 'install' | 'update' | 'action' | 'pull'

// The outer layer only handles position and width (the caller's className goes here, e.g. sm:max-w-[560px]).
// In Plate skin, top padding leaves room inside the viewport for the protruding index tab.
export const dialogContentClass =
  'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col text-[14.5px] text-ink outline-none [[data-skin=plate]_&]:pt-9'

// Outer non-scrolling dialog container: holds the fixed topbar (kind badge + close button) above the independently scrolling body.
export const dialogPaperClass = (kind: DialogKind) =>
  cn(
    'relative flex min-h-0 flex-col rounded-dialog border border-hairline-strong bg-surface-elevated shadow-dialog [[data-skin=plate]_&]:mr-2 [[data-skin=plate]_&]:mb-2 [[data-skin=plate]_&]:rounded-panel [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised',
    kind === 'danger' ? '[[data-skin=plate]_&]:shadow-[8px_8px_0_var(--red)]' : '[[data-skin=plate]_&]:shadow-[8px_8px_0_var(--riso-blue)]',
  )

// Non-scrolling top bar inside the dialog shell so scrolled dialog content never slides underneath the close button.
export const dialogTopbarClass =
  'flex shrink-0 items-center justify-between gap-3 px-6 pt-4 pb-2 max-sm:px-[18px] [[data-skin=plate]_&]:justify-end [[data-skin=plate]_&]:px-[26px] [[data-skin=plate]_&]:pt-4 [[data-skin=plate]_&]:pb-0'

// Scrollable dialog body below the top bar.
export const dialogBodyClass =
  'riso-scrollbar grid min-h-0 flex-1 content-start gap-5 overflow-y-auto overscroll-contain rounded-b-dialog px-6 pt-2 pb-6 max-sm:px-[18px] [[data-skin=plate]_&]:rounded-b-panel [[data-skin=plate]_&]:px-[26px] [[data-skin=plate]_&]:pt-2 [[data-skin=plate]_&]:pb-[22px]'

export const dialogTabClass = (kind: DialogKind) =>
  cn(
    'inline-flex items-center gap-1.5 rounded-stamp border px-2 py-0.5 text-[11.5px] font-medium [[data-skin=plate]_&]:absolute [[data-skin=plate]_&]:bottom-full [[data-skin=plate]_&]:left-6 [[data-skin=plate]_&]:z-[1] [[data-skin=plate]_&]:h-9 [[data-skin=plate]_&]:gap-2 [[data-skin=plate]_&]:rounded-t-input [[data-skin=plate]_&]:rounded-b-none [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:px-[18px] [[data-skin=plate]_&]:py-0 [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:font-black',
    kind === 'danger' ? 'border-red-text/25 bg-tint-red text-red-text' : 'border-hairline bg-surface-subtle text-ink-500',
    kind === 'create'
      ? '[[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink'
      : kind === 'danger'
        ? '[[data-skin=plate]_&]:bg-tint-red [[data-skin=plate]_&]:text-red-text'
        : '[[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink',
  )

// Close button in the top right: clean ghost icon button in mono skin; yellow round stamp with ink border and shadow in Plate skin.
export const closeStampClass =
  'inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-ticket border border-transparent bg-transparent text-ink-500 outline-none transition-colors hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus [[data-skin=plate]_&]:size-9 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-riso-yellow [[data-skin=plate]_&]:hover:shadow-[3px_3px_0_var(--ink)]'

// Plate-only footer button overrides (mono uses standard Button tokens directly).
const plateFooterButtons =
  '[[data-skin=plate]_&_[data-variant=default]]:border-2 [[data-skin=plate]_&_[data-variant=default]]:border-ink [[data-skin=plate]_&_[data-variant=default]]:bg-riso-pink [[data-skin=plate]_&_[data-variant=default]]:text-ink [[data-skin=plate]_&_[data-variant=default]]:shadow-[3px_3px_0_var(--ink)] ' +
  '[[data-skin=plate]_&_[data-variant=secondary]]:border-2 [[data-skin=plate]_&_[data-variant=secondary]]:border-ink [[data-skin=plate]_&_[data-variant=secondary]]:bg-paper-raised [[data-skin=plate]_&_[data-variant=secondary]]:shadow-[3px_3px_0_var(--ink)] [[data-skin=plate]_&_[data-variant=secondary]]:hover:bg-surface-control ' +
  '[[data-skin=plate]_&_[data-variant=destructive]]:border-2 [[data-skin=plate]_&_[data-variant=destructive]]:border-ink [[data-skin=plate]_&_[data-slot=button]]:mr-[3px]'

export const dialogFooterClass = cn(
  'flex flex-wrap justify-end gap-2.5 border-t border-hairline pt-4 [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/30',
  plateFooterButtons,
)

// The button bar at the bottom of a drawer: subtle surface with hairline border in mono skin; light yellow with 2px ink border in Plate skin.
export const sheetFooterClass = cn(
  'flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2.5 border-t border-hairline bg-surface-subtle px-6 py-3.5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow [[data-skin=plate]_&]:px-[26px] [[data-skin=plate]_&]:pt-3.5 [[data-skin=plate]_&]:pb-4',
  plateFooterButtons,
)
