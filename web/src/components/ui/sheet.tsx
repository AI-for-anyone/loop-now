import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { XIcon } from "lucide-react"
import { closeStampClass, sheetFooterClass } from "@/components/ui/dialog-style"
import { useReturnFocus } from "@/lib/return-focus"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"

// Right-side drawer: clean hairline monochrome panel in mono skin; 2px ink border, riso-blue halftone header, yellow footer and offset
// shadow in Plate skin. No enter or exit animation.
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
      <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[1px]" />
      <DialogPrimitive.Content
        data-slot="sheet-content"
        className={cn(
          "fixed inset-y-0 right-0 z-50 flex w-full max-w-[620px] flex-col border-l border-hairline-strong bg-surface-elevated text-ink shadow-dialog outline-none",
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
      className={cn(
        "riso-halftone flex shrink-0 items-start gap-3 border-b border-hairline bg-surface-elevated px-6 pt-5 pb-4 text-ink",
        className
      )}
      {...props}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">{children}</div>
      <DialogPrimitive.Close className={cn(closeStampClass, "")}>
        <XIcon className="size-[15px]" strokeWidth={2.4} />
        <span className="sr-only">{t.common.close}</span>
      </DialogPrimitive.Close>
    </div>
  )
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="sheet-title"
      className={cn(
        "text-[19px] leading-snug font-semibold tracking-tight [overflow-wrap:anywhere]",
        className
      )}
      {...props}
    />
  )
}

function SheetDescription({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="sheet-description"
      className={cn("text-[13.5px] leading-[1.6] text-ink-500", className)}
      {...props}
    />
  )
}

function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-body"
      className={cn("riso-scrollbar flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-6 py-5", className)}
      {...props}
    />
  )
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="sheet-footer" className={cn(sheetFooterClass, className)} {...props} />
}

export { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle }
