import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

// Alert banners: clean 1px hairline callouts in mono skin; sticky note / top-bar warning / tinted banners in Plate skin.
const alertVariants = cva(
  "group/alert relative grid w-full gap-0.5 px-4 py-3 text-left text-[14px] leading-[1.7] text-ink has-data-[slot=alert-action]:relative has-data-[slot=alert-action]:pr-18 has-[>svg]:grid-cols-[auto_1fr] has-[>svg]:gap-x-2.5 *:[svg]:row-span-2 *:[svg]:mt-1 *:[svg]:text-current *:[svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        note: "riso-tilt rounded-card border border-hairline bg-surface-subtle before:hidden [[data-skin=plate]_&]:rounded-[2px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:[--tilt-bg:var(--riso-yellow)] [[data-skin=plate]_&]:[--tilt:-0.5deg] [[data-skin=plate]_&]:[--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [[data-skin=plate]_&]:before:block [[data-skin=plate]_&]:after:absolute [[data-skin=plate]_&]:after:-top-2 [[data-skin=plate]_&]:after:left-6 [[data-skin=plate]_&]:after:h-4 [[data-skin=plate]_&]:after:w-14 [[data-skin=plate]_&]:after:-rotate-[4deg] [[data-skin=plate]_&]:after:bg-white/60 [[data-skin=plate]_&]:after:content-['']",
        warning:
          "rounded-card border border-pink-text/30 bg-tint-yellow text-ink [[data-skin=plate]_&]:rounded-ticket [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:pt-4 [[data-skin=plate]_&]:shadow-[inset_0_6px_0_var(--ink)]",
        error:
          "rounded-card border border-red-text/25 bg-tint-red text-red-text *:data-[slot=alert-description]:text-red-text [[data-skin=plate]_&]:rounded-ticket [[data-skin=plate]_&]:border-0",
        info:
          "rounded-card border border-hairline bg-surface-subtle [[data-skin=plate]_&]:rounded-ticket [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue",
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
        "font-semibold group-has-[>svg]/alert:col-start-2 [[data-skin=plate]_&]:font-black [&_a]:underline [&_a]:underline-offset-3",
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
