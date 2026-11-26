import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// Buttons: crisp Swiss monochrome geometry in mono skin; pill-shaped with solid offset press shadows in Plate skin.
const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center gap-1.5 rounded-ticket border-0 font-semibold whitespace-nowrap outline-none select-none transition-colors duration-100 focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40 disabled:shadow-none motion-reduce:transition-none [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:transition-[translate,box-shadow] [[data-skin=plate]_&]:duration-[80ms] [[data-skin=plate]_&]:active:not-aria-[haspopup]:translate-x-[2px] [[data-skin=plate]_&]:active:not-aria-[haspopup]:translate-y-[2px] [[data-skin=plate]_&]:active:not-aria-[haspopup]:shadow-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          "bg-cta-bg text-cta-fg hover:bg-cta-hover [[data-skin=plate]_&]:bg-riso-blue [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--riso-pink)]",
        interrupt:
          "border border-hairline-strong bg-surface-hover text-ink hover:bg-surface-active [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-riso-pink",
        secondary:
          "border border-hairline-strong bg-surface-control text-ink hover:bg-surface-hover [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:hover:brightness-95",
        ghost:
          "bg-transparent text-ink-700 hover:bg-surface-hover hover:text-ink [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:underline [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-[3px] [[data-skin=plate]_&]:underline-offset-[5px] [[data-skin=plate]_&]:hover:bg-tint-pink-2",
        destructive:
          "bg-red text-white hover:bg-red/90 [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)]",
        "destructive-link":
          "bg-transparent text-red-text hover:bg-tint-red [[data-skin=plate]_&]:underline [[data-skin=plate]_&]:decoration-red [[data-skin=plate]_&]:decoration-2 [[data-skin=plate]_&]:underline-offset-4 [[data-skin=plate]_&]:hover:bg-tint-red/40",
        ink:
          "bg-cta-bg text-cta-fg hover:bg-cta-hover [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:text-riso-yellow [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--riso-pink)]",
        // Dangerous actions in a page header (cancel task, uninstall plugin): bordered control with red text and red tint on hover.
        "danger-outline":
          "border border-red-text/40 bg-surface-control text-red-text hover:bg-tint-red [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-red-text [[data-skin=plate]_&]:bg-paper-raised",
      },
      size: {
        default: "h-[42px] px-5 text-[14.5px]",
        xs: "h-[34px] gap-1 px-3.5 text-[13px] [&_svg:not([class*='size-'])]:size-3.5",
        sm: "h-[38px] gap-1 px-4 text-[13.5px]",
        lg: "h-11 px-6 text-[15px]",
        icon: "size-9",
        "icon-sm": "size-[30px] [&_svg:not([class*='size-'])]:size-3.5",
        "icon-xs": "size-7 [&_svg:not([class*='size-'])]:size-3",
      },
    },
    compoundVariants: [
      // Remove, delete, edit and similar actions inside lists are 36 high.
      { variant: "ghost", size: "sm", className: "h-9 px-3" },
      { variant: "destructive-link", size: "sm", className: "h-9 px-3" },
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
