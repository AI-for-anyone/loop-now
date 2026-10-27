import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// 孔版印刷按钮：全部胶囊形，实色偏移投影；按下时投影收回、向右下移 2px（80ms），这是控制台仅有的三个动效之一。
const buttonVariants = cva(
  "group/button inline-flex shrink-0 items-center justify-center gap-1.5 rounded-full border-0 font-black whitespace-nowrap outline-none select-none transition-[translate,box-shadow] duration-[80ms] focus-visible:shadow-focus active:not-aria-[haspopup]:translate-x-[2px] active:not-aria-[haspopup]:translate-y-[2px] active:not-aria-[haspopup]:shadow-none disabled:pointer-events-none disabled:opacity-40 disabled:shadow-none motion-reduce:transition-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-riso-blue text-white shadow-[3px_3px_0_var(--riso-pink)]",
        interrupt: "bg-riso-pink text-ink shadow-[3px_3px_0_var(--ink)]",
        secondary: "bg-tint-blue text-ink hover:bg-[#d6ddf4]",
        ghost:
          "bg-transparent text-ink underline decoration-riso-pink decoration-[3px] underline-offset-[5px] hover:bg-tint-pink-2",
        destructive: "bg-red text-white shadow-[3px_3px_0_var(--ink)]",
        "destructive-link":
          "bg-transparent text-red-text underline decoration-red decoration-2 underline-offset-4 hover:bg-tint-red/40",
        ink: "bg-ink text-riso-yellow shadow-[2px_2px_0_var(--riso-pink)]",
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
      // 列表内的移除、删除、编辑等操作高 36。
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
