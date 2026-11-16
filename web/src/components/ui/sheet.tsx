import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { XIcon } from "lucide-react"
import { closeStampClass, sheetFooterClass } from "@/components/ui/dialog-style"
import { useReturnFocus } from "@/lib/return-focus"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"

// 右侧抽屉（改版第 5 步）：宽 620，左缘 2px 藏青线加孔版蓝实色投影；头部是孔版蓝网点底，标题、说明和关闭按钮（黄色圆章）都在里面；
// 底部（SheetFooter）是一条浅黄色按钮栏，同对话页的输入区，危险动作在左、主按钮在右。不做进出场动画。
// 标题 24px，不加错版投影（错版只用于 ≥ 28px 的展示字）。
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
  // 抽屉由地址控制开合，没有 Radix 的 Trigger；关闭时把焦点还给打开前的元素。
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
