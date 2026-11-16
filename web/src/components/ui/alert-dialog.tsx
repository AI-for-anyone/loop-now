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

// 确认框（改版第 5 步）：与对话框同一张小纸——顶边写明类别的索引标签（默认“确认”，危险操作浅红底红字、纸投影红色），
// 右上角黄色圆章关闭（就是“取消”，放在最前面，打开时焦点仍落在按钮区的取消上）。
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
  // 确认框由状态控制开合，没有 Radix 的 Trigger；关闭时把焦点还给打开前的元素。
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
