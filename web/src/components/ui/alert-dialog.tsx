"use client"

import * as React from "react"
import { cn } from "@/lib/utils"
import { AlertDialog as AlertDialogPrimitive } from "radix-ui"

import { XIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { DialogTab } from "@/components/ui/dialog"
import { closeStampClass, dialogContentClass, dialogFooterClass, dialogPaperClass, overlayClass, type DialogKind } from "@/components/ui/dialog-style"
import { t } from "@/i18n"
import { useReturnFocus } from "@/lib/return-focus"

function AlertDialog({
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Root>) {
  return <AlertDialogPrimitive.Root data-slot="alert-dialog" {...props} />
}

function AlertDialogTrigger({
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Trigger>) {
  return (
    <AlertDialogPrimitive.Trigger data-slot="alert-dialog-trigger" {...props} />
  )
}

function AlertDialogPortal({
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Portal>) {
  return (
    <AlertDialogPrimitive.Portal data-slot="alert-dialog-portal" {...props} />
  )
}

function AlertDialogOverlay({
  className,
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Overlay>) {
  return (
    <AlertDialogPrimitive.Overlay
      data-slot="alert-dialog-overlay"
      className={cn(overlayClass, className)}
      {...props}
    />
  )
}

// Confirmation dialog (redesign step 5): the same small sheet as the dialog, with an index tab on the top edge naming the kind (“确认”
// (Confirm) by default; danger is red text on light red, with a red sheet shadow),
// and a yellow round stamp in the top right that closes it (it acts as “取消” (Cancel), comes first, and on open focus still lands on
// Cancel in the button area).
function AlertDialogContent({
  className,
  paperClassName,
  children,
  size = "default",
  kind = "confirm",
  onOpenAutoFocus,
  onCloseAutoFocus,
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Content> & {
  size?: "default" | "sm"
  kind?: DialogKind
  paperClassName?: string
}) {
  // The confirmation dialog is opened and closed by state, without a Radix Trigger; on close, focus returns to the element that had it
  // before opening.
  const focusHandlers = useReturnFocus(onOpenAutoFocus, onCloseAutoFocus)
  return (
    <AlertDialogPortal>
      <AlertDialogOverlay />
      <AlertDialogPrimitive.Content
        data-slot="alert-dialog-content"
        data-size={size}
        data-kind={kind}
        className={cn(
          dialogContentClass,
          "group/alert-dialog-content data-[size=default]:sm:max-w-[480px] data-[size=sm]:sm:max-w-sm",
          className
        )}
        {...focusHandlers}
        {...props}
      >
        <AlertDialogPrimitive.Cancel data-slot="alert-dialog-close" className={cn(closeStampClass, "absolute top-[52px] right-[26px] z-[1] max-sm:right-[18px]")}>
          <XIcon className="size-[15px]" strokeWidth={2.8} />
          <span className="sr-only">{t.common.close}</span>
        </AlertDialogPrimitive.Cancel>
        <DialogTab kind={kind} />
        <div data-dialog-paper className={cn(dialogPaperClass(kind), paperClassName)}>
          {children}
        </div>
      </AlertDialogPrimitive.Content>
    </AlertDialogPortal>
  )
}

function AlertDialogHeader({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-header"
      className={cn(
        "grid gap-1.5 text-left",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogFooter({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-footer"
      className={cn(dialogFooterClass, className)}
      {...props}
    />
  )
}

function AlertDialogMedia({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-dialog-media"
      className={cn(
        "mb-2 inline-flex size-16 items-center justify-center rounded-full bg-tint-blue *:[svg:not([class*='size-'])]:size-8",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Title>) {
  return (
    <AlertDialogPrimitive.Title
      data-slot="alert-dialog-title"
      className={cn(
        "font-display-cn text-[28px] leading-[1.15] font-normal text-riso-blue [text-shadow:2.5px_2.5px_0_var(--riso-pink)]",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Description>) {
  return (
    <AlertDialogPrimitive.Description
      data-slot="alert-dialog-description"
      className={cn(
        "text-[14px] leading-[1.7] text-ink-700 *:[a]:underline *:[a]:underline-offset-3",
        className
      )}
      {...props}
    />
  )
}

function AlertDialogAction({
  className,
  variant = "default",
  size = "default",
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Action> &
  Pick<React.ComponentProps<typeof Button>, "variant" | "size">) {
  return (
    <Button variant={variant} size={size} asChild>
      <AlertDialogPrimitive.Action
        data-slot="alert-dialog-action"
        className={cn(className)}
        {...props}
      />
    </Button>
  )
}

function AlertDialogCancel({
  className,
  variant = "secondary",
  size = "default",
  ...props
}: React.ComponentProps<typeof AlertDialogPrimitive.Cancel> &
  Pick<React.ComponentProps<typeof Button>, "variant" | "size">) {
  return (
    <Button variant={variant} size={size} asChild>
      <AlertDialogPrimitive.Cancel
        data-slot="alert-dialog-cancel"
        className={cn(className)}
        {...props}
      />
    </Button>
  )
}

export {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogTitle,
  AlertDialogTrigger,
}
