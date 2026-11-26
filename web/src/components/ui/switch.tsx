import * as React from "react"
import { Switch as SwitchPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Switch: compact Vercel Geist toggle (20x36px, neutral off track, Geist Blue on track, crisp white thumb) in mono skin;
// 2px ink-bordered pill with yellow checked fill in Plate skin. Its name comes from aria-label or an associated label.
function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border border-transparent bg-switch-track p-0.5 outline-none transition-colors duration-150 focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:bg-cta-bg motion-reduce:transition-none [[data-skin=plate]_&]:h-[26px] [[data-skin=plate]_&]:w-[46px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper [[data-skin=plate]_&]:data-[state=checked]:border-ink [[data-skin=plate]_&]:data-[state=checked]:bg-riso-yellow",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="block size-4 rounded-full bg-white shadow-[0_1px_2px_rgb(0_0_0/0.24)] transition-transform duration-150 data-[state=checked]:translate-x-4 motion-reduce:transition-none [[data-skin=plate]_&]:size-[18px] [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:shadow-none [[data-skin=plate]_&]:data-[state=checked]:translate-x-5 [[data-skin=plate]_&]:data-[state=checked]:bg-ink"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
