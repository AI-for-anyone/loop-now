import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { XIcon } from "lucide-react"
import { useReturnFocus } from "@/lib/return-focus"
import { cn } from "@/lib/utils"
import { t } from "@/i18n"

// 右侧抽屉：宽 620，粉色印刷投影；顶部是孔版蓝网点色带，标题、说明和关闭按钮都在色带里。不做进出场动画。
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
          "fixed inset-y-0 right-0 z-50 flex w-full max-w-[620px] flex-col bg-paper-raised text-ink shadow-[-6px_0_0_var(--riso-pink)] outline-none",
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
      className={cn("riso-halftone flex items-start gap-3 bg-riso-blue px-[26px] pt-6 pb-[18px] text-white", className)}
      {...props}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">{children}</div>
      <DialogPrimitive.Close
        className="inline-flex size-[38px] shrink-0 items-center justify-center rounded-full bg-riso-yellow text-ink outline-none focus-visible:shadow-focus"
      >
        <XIcon className="size-4" />
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

export { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle }
