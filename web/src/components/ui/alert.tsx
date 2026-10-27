import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

// note：黄色便签，背景层轻微倾斜，顶部贴一条半透明白胶带；warning：浅黄底加 6px 藏青顶条；error：浅红底；info：浅蓝底。
const alertVariants = cva(
  "group/alert relative grid w-full gap-0.5 px-4 py-3 text-left text-[14px] leading-[1.7] text-ink has-data-[slot=alert-action]:relative has-data-[slot=alert-action]:pr-18 has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2.5 *:[svg]:row-span-2 *:[svg]:mt-1 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        note: "riso-tilt rounded-[2px] [--tilt-bg:var(--riso-yellow)] [--tilt:-0.5deg] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] after:absolute after:-top-2 after:left-6 after:h-4 after:w-14 after:-rotate-[4deg] after:bg-white/60 after:content-['']",
        warning: "rounded-ticket bg-tint-yellow pt-4 shadow-[inset_0_6px_0_var(--ink)]",
        error: "rounded-ticket bg-tint-red text-red-text *:data-[slot=alert-description]:text-red-text",
        info: "rounded-ticket bg-tint-blue",
      },
    },
    defaultVariants: {
      variant: "info",
    },
  }
)

function Alert({
  className,
  variant,
  role = "alert",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  return (
    <div
      data-slot="alert"
      data-variant={variant ?? "info"}
      role={role}
      className={cn(alertVariants({ variant }), className)}
      {...props}
    />
  )
}

function AlertTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        "font-black group-has-[>svg]/alert:col-start-2 [&_a]:underline [&_a]:underline-offset-3",
        className
      )}
      {...props}
    />
  )
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        "text-[14px] text-ink-700 [&_a]:underline [&_a]:underline-offset-3 [&_p:not(:last-child)]:mb-4",
        className
      )}
      {...props}
    />
  )
}

function AlertAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="alert-action"
      className={cn("absolute top-2.5 right-3", className)}
      {...props}
    />
  )
}

export { Alert, AlertTitle, AlertDescription, AlertAction }
