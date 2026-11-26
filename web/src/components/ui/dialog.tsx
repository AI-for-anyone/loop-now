import * as React from "react"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"
import { Dialog as DialogPrimitive } from "radix-ui"

import { Button } from "@/components/ui/button"
import {
  closeStampClass,
  dialogBodyClass,
  dialogContentClass,
  dialogFooterClass,
  dialogPaperClass,
  dialogTabClass,
  dialogTopbarClass,
  overlayClass,
  type DialogKind,
} from "@/components/ui/dialog-style"
import { useReturnFocus } from "@/lib/return-focus"
import { CircleAlertIcon, PlusIcon, XIcon } from "lucide-react"

function Dialog({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />
}

function DialogTrigger({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Trigger>) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Portal>) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Close>) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(overlayClass, className)}
      {...props}
    />
  )
}

// Kind badge (mono skin) / protruding top index tab (Plate skin).
function DialogTab({ kind }: { kind: DialogKind }) {
  const Icon = kind === "create" ? PlusIcon : kind === "danger" ? CircleAlertIcon : null
  return (
    <span data-dialog-tab={kind} className={dialogTabClass(kind)}>
      {Icon && <Icon aria-hidden="true" className="size-[13px]" strokeWidth={2.4} />}
      {t.common.dialogKinds[kind]}
    </span>
  )
}

// Dialog: elevated card with a non-scrolling top bar holding the kind label and close button above the scrolling body, so scrolled
// content never slides underneath the close button. Plate skin restores the protruding folder tab, yellow round stamp and offset shadow.
function DialogContent({
  className,
  paperClassName,
  children,
  kind = "edit",
  showCloseButton = true,
  onOpenAutoFocus,
  onCloseAutoFocus,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content> & {
  showCloseButton?: boolean
  kind?: DialogKind
  paperClassName?: string
}) {
  // The dialog is opened and closed by state, without a Radix Trigger; on close, focus returns to the element that had it before opening.
  const focusHandlers = useReturnFocus(onOpenAutoFocus, onCloseAutoFocus)
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        data-kind={kind}
        className={cn(dialogContentClass, "sm:max-w-[540px]", className)}
        {...focusHandlers}
        {...props}
      >
        <div data-dialog-paper className={dialogPaperClass(kind)}>
          <div data-dialog-topbar className={dialogTopbarClass}>
            <DialogTab kind={kind} />
            {showCloseButton && (
              <DialogPrimitive.Close data-slot="dialog-close" className={closeStampClass}>
                <XIcon className="size-[15px]" strokeWidth={2.4} />
                <span className="sr-only">{t.common.close}</span>
              </DialogPrimitive.Close>
            )}
          </div>
          <div data-dialog-body className={cn(dialogBodyClass, paperClassName)}>
            {children}
          </div>
        </div>
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(dialogFooterClass, className)}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close asChild>
          <Button variant="secondary">{t.common.close}</Button>
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn(
        "text-[19px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[30px] [[data-skin=plate]_&]:leading-[1.15] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal [[data-skin=plate]_&]:text-riso-blue [[data-skin=plate]_&]:[text-shadow:2.5px_2.5px_0_var(--riso-pink)]",
        className
      )}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn("text-[13.5px] leading-[1.7] text-ink-700 *:[a]:underline *:[a]:underline-offset-3", className)}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTab,
  DialogTrigger,
}
