import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { XIcon } from "lucide-react"
import { closeStampClass, sheetFooterClass } from "@/components/ui/dialog-style"
import { useReturnFocus } from "@/lib/return-focus"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"

// Right-side drawer (redesign step 5): 620 wide, with a 2px navy line on the left edge plus a riso-blue solid offset shadow; the header has
// a riso-blue halftone background and holds the title, description and close button (a yellow round stamp);
// the bottom (SheetFooter) is a light yellow button bar, like the input area on the conversation page, with dangerous actions on the left
// and the primary button on the right. No enter or exit animation.
// The title is 24px, without the misregistered shadow (misregistration is only used for display type ≥ 28px).
function Sheet(props: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="sheet" {...props} />
}

function SheetContent({
  className,
  children,
  onOpenAutoFocus,
  onCloseAutoFocus,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content>) {
  // The drawer is opened and closed by the URL, without a Radix Trigger; on close, focus returns to the element that had it before opening.
  const focusHandlers = useReturnFocus(onOpenAutoFocus, onCloseAutoFocus)
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-ink/20 [background-image:radial-gradient(rgb(255_255_255/.06)_1px,transparent_1.3px)] [background-size:6px_6px]" />
      <DialogPrimitive.Content
        data-slot="sheet-content"
        className={cn(
          "fixed inset-y-0 right-0 z-50 flex w-full max-w-[620px] flex-col border-l-2 border-ink bg-paper-raised text-ink shadow-[-8px_0_0_var(--riso-blue)] outline-none",
          className
        )}
        {...focusHandlers}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

function SheetHeader({ className, children, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("riso-halftone flex items-start gap-3 border-b-2 border-ink bg-riso-blue px-[26px] pt-6 pb-[18px] text-white", className)}
      {...props}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">{children}</div>
      <DialogPrimitive.Close className={cn(closeStampClass, "size-[38px]")}>
        <XIcon className="size-[15px]" strokeWidth={2.8} />
        <span className="sr-only">{t.common.close}</span>
      </DialogPrimitive.Close>
    </div>
  )
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="sheet-title"
      className={cn("text-[24px] leading-tight font-bold [overflow-wrap:anywhere]", className)}
      {...props}
    />
  )
}

function SheetDescription({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="sheet-description"
      className={cn("text-[14px] leading-[1.7] text-white", className)}
      {...props}
    />
  )
}

function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-body"
      className={cn("flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-[26px] py-[22px]", className)}
      {...props}
    />
  )
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="sheet-footer" className={cn(sheetFooterClass, className)} {...props} />
}

export { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle }
