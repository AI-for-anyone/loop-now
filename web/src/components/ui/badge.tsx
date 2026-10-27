import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// 三种形态：chip 是按状态着色的胶囊；stamp 是回复结局的描边印章（只用于已打断、服务端重启中断、失败）；
// sticker 是 Agent 头部的大圆贴纸。印章与贴纸的倾斜只画在 ::before 上。每种状态都带文字，不只靠颜色区分。
const badgeVariants = cva(
  "group/badge inline-flex w-fit shrink-0 items-center justify-center gap-1.5 whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3.5!",
  {
    variants: {
      variant: {
        chip: "min-h-[22px] rounded-full px-2.5 text-[12.5px] leading-[22px] font-black",
        stamp:
          "riso-tilt rounded-stamp px-2.5 py-0.5 text-[12.5px] font-black tracking-wide [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_currentColor] [--tilt:var(--stamp-tilt,-3deg)]",
        sticker:
          "riso-tilt size-[112px] flex-col rounded-full px-3 text-center text-[15px] leading-tight font-black whitespace-normal [--tilt:8deg] [--tilt-shadow:4px_4px_0_var(--ink)]",
      },
      tone: {
        neutral: "",
        success: "",
        danger: "",
        busy: "",
        info: "",
        warning: "",
        interrupted: "",
        restart: "",
        // 身份标记（如项目 Manager）：藏青底白字，不属于任何状态色。
        role: "",
      },
    },
    compoundVariants: [
      { variant: "chip", tone: "neutral", className: "bg-tint-blue text-ink" },
      { variant: "chip", tone: "info", className: "bg-tint-blue text-ink" },
      { variant: "chip", tone: "success", className: "bg-tint-green text-green-text" },
      { variant: "chip", tone: "danger", className: "bg-tint-red text-red-text" },
      { variant: "chip", tone: "busy", className: "bg-riso-pink text-ink" },
      { variant: "chip", tone: "warning", className: "bg-tint-yellow text-ink shadow-[inset_0_3px_0_var(--ink)]" },
      { variant: "chip", tone: "role", className: "bg-ink text-white" },
      { variant: "stamp", tone: "interrupted", className: "text-pink-text [--stamp-tilt:-3deg]" },
      { variant: "stamp", tone: "restart", className: "text-ink-700 [--stamp-tilt:2deg]" },
      { variant: "stamp", tone: "danger", className: "text-red-text [--stamp-tilt:-2deg]" },
      { variant: "sticker", tone: "success", className: "text-green-text [--tilt-bg:var(--tint-green)]" },
      { variant: "sticker", tone: "busy", className: "text-ink [--tilt-bg:var(--riso-pink)]" },
      { variant: "sticker", tone: "danger", className: "text-red-text [--tilt-bg:var(--tint-red)]" },
      { variant: "sticker", tone: "neutral", className: "text-ink [--tilt-bg:var(--riso-yellow)]" },
    ],
    defaultVariants: {
      variant: "chip",
      tone: "neutral",
    },
  }
)

function Badge({
  className,
  variant = "chip",
  tone = "neutral",
  asChild = false,
  ...props
}: React.ComponentProps<"span"> &
  VariantProps<typeof badgeVariants> & { asChild?: boolean }) {
  const Comp = asChild ? Slot.Root : "span"

  return (
    <Comp
      data-slot="badge"
      data-variant={variant}
      data-tone={tone}
      className={cn(badgeVariants({ variant, tone }), className)}
      {...props}
    />
  )
}

export { Badge, badgeVariants }
