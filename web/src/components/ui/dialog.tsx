import * as React from "react"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"
import { Dialog as DialogPrimitive } from "radix-ui"

import { Button } from "@/components/ui/button"
import { closeStampClass, dialogContentClass, dialogFooterClass, dialogPaperClass, dialogTabClass, overlayClass, type DialogKind } from "@/components/ui/dialog-style"
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

// 顶边的索引标签：写明弹窗的类别（新建带加号，危险操作带感叹号）。
function DialogTab({ kind }: { kind: DialogKind }) {
  const Icon = kind === "create" ? PlusIcon : kind === "danger" ? CircleAlertIcon : null
  return (
    <div className="relative z-[1] flex shrink-0 pl-6">
      <span data-dialog-tab={kind} className={dialogTabClass(kind)}>
        {Icon && <Icon aria-hidden="true" className="size-[13px]" strokeWidth={2.8} />}
        {t.common.dialogKinds[kind]}
      </span>
    </div>
  )
}

// 对话框（改版第 5 步，全站统一）：一张小纸，顶边贴一张写明类别的索引标签；右上角黄色圆章关闭；
// 按钮区（DialogFooter）上方一条虚线。kind 决定标签的字和颜色（危险操作的纸投影是红色）。
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
  // 对话框由状态控制开合，没有 Radix 的 Trigger；关闭时把焦点还给打开前的元素。
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
        <DialogTab kind={kind} />
        <div data-dialog-paper className={cn(dialogPaperClass(kind), paperClassName)}>
          {children}
        </div>
        {showCloseButton && (
          <DialogPrimitive.Close data-slot="dialog-close" className={cn(closeStampClass, "absolute top-[52px] right-[26px] max-sm:right-[18px]")}>
            <XIcon className="size-[15px]" strokeWidth={2.8} />
            <span className="sr-only">{t.common.close}</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5 pr-12", className)}
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
      className={cn("font-display-cn text-[30px] leading-[1.15] font-normal text-riso-blue [text-shadow:2.5px_2.5px_0_var(--riso-pink)]", className)}
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
