import { cn } from '@/lib/utils'

// The look shared by dialogs and drawers (redesign step 5, the same across the whole site).

// Overlay: navy at 22% plus halftone dots. No enter or exit animation.
export const overlayClass =
  'fixed inset-0 isolate z-50 bg-ink/[0.22] [background-image:radial-gradient(rgb(255_255_255/.08)_1px,transparent_1.3px)] [background-size:6px_6px]'

// The dialog's kind, written on the index tab on the top edge: yellow for create; red text on light red for danger (the sheet's shadow
// turns red too); light blue for the rest.
export type DialogKind = 'create' | 'danger' | 'confirm' | 'edit' | 'view' | 'token' | 'choose' | 'install' | 'update' | 'action' | 'pull'

// The outer layer only handles position and width (the caller's className goes here, e.g. sm:max-w-[560px]); inside are the tab and the sheet.
export const dialogContentClass =
  'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col text-[14.5px] text-ink outline-none'

export const dialogTabClass = (kind: DialogKind) =>
  cn(
    'inline-flex h-9 items-center gap-2 rounded-t-input border-2 border-b-0 border-ink px-[18px] text-[13px] font-black',
    kind === 'create' ? 'bg-riso-yellow text-ink' : kind === 'danger' ? 'bg-tint-red text-red-text' : 'bg-tint-blue text-ink',
  )

// Sheet: 2px navy border, light paper background, 18 corner radius, 8px solid offset shadow (riso blue; red for danger). When content
// overflows, the sheet scrolls itself.
export const dialogPaperClass = (kind: DialogKind) =>
  cn(
    'riso-scrollbar relative -mt-0.5 mr-2 mb-2 grid min-h-0 content-start gap-5 overflow-y-auto overscroll-contain rounded-panel border-2 border-ink bg-paper-raised px-[26px] pt-6 pb-[22px] max-sm:px-[18px]',
    kind === 'danger' ? 'shadow-[8px_8px_0_var(--red)]' : 'shadow-[8px_8px_0_var(--riso-blue)]',
  )

// Close button in the top right: a yellow round stamp (navy border, small shadow). In dialogs it is positioned relative to the outer layer
// (18px below the tab), so it stays put when the sheet scrolls.
export const closeStampClass =
  'inline-flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-full border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none hover:shadow-[3px_3px_0_var(--ink)] focus-visible:shadow-focus'

// Button area: a dashed line above it. The primary button inside is fluorescent pink (at most one per dialog) and secondary buttons have a
// light paper background, all with a navy border plus shadow; the confirm button of a danger action is red.
const footerButtons =
  '[&_[data-variant=default]]:border-2 [&_[data-variant=default]]:border-ink [&_[data-variant=default]]:bg-riso-pink [&_[data-variant=default]]:text-ink [&_[data-variant=default]]:shadow-[3px_3px_0_var(--ink)] ' +
  '[&_[data-variant=secondary]]:border-2 [&_[data-variant=secondary]]:border-ink [&_[data-variant=secondary]]:bg-paper-raised [&_[data-variant=secondary]]:shadow-[3px_3px_0_var(--ink)] [&_[data-variant=secondary]]:hover:bg-white ' +
  '[&_[data-variant=destructive]]:border-2 [&_[data-variant=destructive]]:border-ink [&_[data-slot=button]]:mr-[3px]'

export const dialogFooterClass = cn('flex flex-wrap justify-end gap-3 border-t-2 border-dashed border-ink/30 pt-4', footerButtons)

// The button bar at the bottom of a drawer: light yellow (like the input area on the conversation page), with a 2px navy line on the top
// edge; dangerous actions on the left, the primary button on the right (the caller gives the primary button ml-auto).
export const sheetFooterClass = cn('flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2.5 border-t-2 border-ink bg-tint-yellow px-[26px] pt-3.5 pb-4', footerButtons)
