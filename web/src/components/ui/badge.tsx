import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

// Three forms: chip is a Vercel Geist pill badge; stamp marks how a reply ended (interrupted, server restart, failed);
// sticker is the round badge in the Agent header. Subtle tinted pills in mono skin; tilted Risograph layers in Plate skin.
// Every state carries text and is never told apart by color alone.
const badgeVariants = cva(
  "group/badge inline-flex w-fit shrink-0 items-center justify-center gap-1 whitespace-nowrap [&>svg]:pointer-events-none [&>svg]:size-3!",
  {
    variants: {
      variant: {
        chip: "h-[22px] rounded-full border border-transparent px-2 text-[11.5px] leading-none font-medium tracking-[-0.005em] [[data-skin=plate]_&]:h-auto [[data-skin=plate]_&]:min-h-[22px] [[data-skin=plate]_&]:gap-1.5 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:px-2.5 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:leading-[22px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-normal",
        stamp:
          "riso-tilt rounded-full border border-current/25 bg-surface-subtle px-2 py-0.5 text-[11.5px] font-medium tracking-normal before:hidden [[data-skin=plate]_&]:rounded-stamp [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:px-2.5 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-wide [[data-skin=plate]_&]:[--tilt-bg:transparent] [[data-skin=plate]_&]:[--tilt-shadow:inset_0_0_0_2px_currentColor] [[data-skin=plate]_&]:[--tilt:var(--stamp-tilt,-3deg)] [[data-skin=plate]_&]:before:block",
        sticker:
          "riso-tilt size-[112px] flex-col rounded-full border border-hairline-strong px-3 text-center text-[14px] leading-tight font-semibold whitespace-normal before:hidden [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:text-[15px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:[--tilt:8deg] [[data-skin=plate]_&]:[--tilt-shadow:4px_4px_0_var(--ink)] [[data-skin=plate]_&]:before:block",
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
        // Role marker (such as the project Manager): subtle monochrome pill in mono skin, white on navy in Plate skin.
        role: "",
      },
    },
    compoundVariants: [
      { variant: "chip", tone: "neutral", className: "border-hairline bg-surface-subtle text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink" },
      { variant: "chip", tone: "info", className: "border-riso-blue/20 bg-tint-blue text-riso-blue [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink" },
      { variant: "chip", tone: "success", className: "border-green-text/20 bg-tint-green text-green-text [[data-skin=plate]_&]:border-0" },
      { variant: "chip", tone: "danger", className: "border-red-text/20 bg-tint-red text-red-text [[data-skin=plate]_&]:border-0" },
      { variant: "chip", tone: "busy", className: "border-riso-blue/25 bg-tint-blue text-riso-blue [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:text-ink" },
      { variant: "chip", tone: "warning", className: "border-pink-text/25 bg-tint-yellow text-pink-text [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[inset_0_3px_0_var(--ink)]" },
      { variant: "chip", tone: "role", className: "border-hairline-strong bg-surface-hover text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:text-white" },
      { variant: "stamp", tone: "interrupted", className: "text-pink-text [[data-skin=plate]_&]:[--stamp-tilt:-3deg]" },
      { variant: "stamp", tone: "restart", className: "text-ink-700 [[data-skin=plate]_&]:[--stamp-tilt:2deg]" },
      { variant: "stamp", tone: "danger", className: "text-red-text [[data-skin=plate]_&]:[--stamp-tilt:-2deg]" },
      { variant: "sticker", tone: "success", className: "bg-tint-green text-green-text [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:[--tilt-bg:var(--tint-green)]" },
      { variant: "sticker", tone: "busy", className: "bg-tint-blue text-riso-blue [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:[--tilt-bg:var(--riso-pink)]" },
      { variant: "sticker", tone: "danger", className: "bg-tint-red text-red-text [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:[--tilt-bg:var(--tint-red)]" },
      { variant: "sticker", tone: "neutral", className: "bg-surface-subtle text-ink [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:[--tilt-bg:var(--riso-yellow)]" },
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
