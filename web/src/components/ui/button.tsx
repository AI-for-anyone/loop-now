import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// Buttons: compact Vercel Geist geometry (36px default, 32px sm, 28px xs, 6px corners, subtle surface shadows) in mono skin;
// pill-shaped with solid offset press shadows in Plate skin.
const buttonVariants = cva(
  "group/button inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 rounded-ticket border-0 font-medium whitespace-nowrap outline-none select-none transition-[background-color,border-color,color,box-shadow,transform] duration-150 active:scale-[0.99] focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40 disabled:shadow-none motion-reduce:transition-none motion-reduce:active:scale-100 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "bg-cta-bg text-cta-fg shadow-[0_1px_2px_rgb(0_0_0/0.08)] hover:bg-cta-hover",
        interrupt:
          "border border-hairline-strong bg-surface-control text-ink shadow-[0_1px_2px_rgb(0_0_0/0.04)] hover:border-ink/25 hover:bg-surface-hover",
        secondary:
          "border border-hairline-strong bg-surface-control text-ink shadow-[0_1px_2px_rgb(0_0_0/0.04)] hover:border-ink/25 hover:bg-surface-hover",
        ghost:
          "bg-transparent text-ink-700 hover:bg-surface-hover hover:text-ink",
        destructive:
          "bg-red text-white shadow-[0_1px_2px_rgb(0_0_0/0.08)] hover:bg-red/90",
        "destructive-link":
          "bg-transparent text-red-text hover:bg-tint-red",
        ink:
          "bg-ink text-surface-canvas shadow-[0_1px_2px_rgb(0_0_0/0.08)] hover:bg-ink/88",
        // Dangerous actions in a page header (cancel task, uninstall plugin): hairline control with red text and red tint on hover.
        "danger-outline":
          "border border-red-text/30 bg-surface-control text-red-text shadow-[0_1px_2px_rgb(0_0_0/0.03)] hover:border-red-text/50 hover:bg-tint-red",
      },
      size: {
        default: "h-9 px-3.5 text-[13.5px]",
        xs: "h-7 gap-1 px-2.5 text-[12px] [&_svg:not([class*='size-'])]:size-3.5",
        sm: "h-8 gap-1.5 px-3 text-[13px] [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 px-4 text-[14px]",
        icon: "size-8",
        "icon-sm": "size-7 [&_svg:not([class*='size-'])]:size-3.5",
        "icon-xs": "size-6 [&_svg:not([class*='size-'])]:size-3",
      },
    },
    compoundVariants: [
      // Remove, delete, edit and similar actions inside lists: 32px in mono Geist, 36px in Plate.
      { variant: "ghost", size: "sm", className: "h-8 px-2.5" },
      { variant: "destructive-link", size: "sm", className: "h-8 px-2.5" },
    ],
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot.Root : "button"

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }
