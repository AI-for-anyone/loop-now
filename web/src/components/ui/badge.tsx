import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// Three forms: chip is a compact pill badge; stamp marks how a reply ended (interrupted, server restart, failed);
// sticker is a large round status badge. Every state carries text and is never told apart by color alone.
const badgeVariants = cva(
  "group/badge inline-flex w-fit shrink-0 items-center justify-center gap-1 whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3!",
  {
    variants: {
      variant: {
        chip: "h-6 rounded-full border border-transparent px-2 text-[12.5px] leading-none font-medium tracking-[-0.005em]",
        stamp: "rounded-full border border-current/25 bg-surface-subtle px-2 py-0.5 text-[12.5px] font-medium tracking-normal",
        sticker: "size-[112px] flex-col rounded-full border border-hairline-strong px-3 text-center text-[15px] leading-tight font-semibold whitespace-normal",
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
        role: "",
      },
    },
    compoundVariants: [
      { variant: "chip", tone: "neutral", className: "border-hairline bg-surface-subtle text-ink-700" },
      { variant: "chip", tone: "info", className: "border-riso-blue/20 bg-tint-blue text-riso-blue" },
      { variant: "chip", tone: "success", className: "border-green-text/20 bg-tint-green text-green-text" },
      { variant: "chip", tone: "danger", className: "border-red-text/20 bg-tint-red text-red-text" },
      { variant: "chip", tone: "busy", className: "border-riso-blue/25 bg-tint-blue text-riso-blue" },
      { variant: "chip", tone: "warning", className: "border-pink-text/25 bg-tint-yellow text-pink-text" },
      { variant: "chip", tone: "role", className: "border-hairline-strong bg-surface-hover text-ink" },
      { variant: "stamp", tone: "interrupted", className: "text-pink-text" },
      { variant: "stamp", tone: "restart", className: "text-ink-700" },
      { variant: "stamp", tone: "danger", className: "text-red-text" },
      { variant: "sticker", tone: "success", className: "bg-tint-green text-green-text" },
      { variant: "sticker", tone: "busy", className: "bg-tint-blue text-riso-blue" },
      { variant: "sticker", tone: "danger", className: "bg-tint-red text-red-text" },
      { variant: "sticker", tone: "neutral", className: "bg-surface-subtle text-ink" },
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
