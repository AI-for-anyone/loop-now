import * as React from "react"
import { RadioGroup as RadioGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Card-style radio group: flat hairline cards in mono skin; white cards with tilted yellow/pink background layer when selected in Plate skin.
function RadioCards({ className, ...props }: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return <RadioGroupPrimitive.Root data-slot="radio-cards" className={cn("grid gap-3", className)} {...props} />
}

function RadioCard({
  className,
  title,
  description,
  ...props
}: Omit<React.ComponentProps<typeof RadioGroupPrimitive.Item>, "title"> & { title: React.ReactNode; description?: React.ReactNode }) {
  return (
    <RadioGroupPrimitive.Item
      data-slot="radio-card"
      className={cn(
        "group/radio-card riso-tilt flex w-full cursor-pointer items-start gap-3 rounded-card border border-hairline bg-surface-control px-4 py-3 text-left text-ink outline-none transition-colors before:hidden hover:bg-surface-hover focus-visible:shadow-focus data-[state=checked]:border-ink data-[state=checked]:bg-surface-subtle disabled:cursor-not-allowed disabled:opacity-40 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:[--tilt:0deg] [[data-skin=plate]_&]:before:block [[data-skin=plate]_&]:hover:bg-transparent [[data-skin=plate]_&]:data-[state=checked]:bg-transparent [[data-skin=plate]_&]:data-[state=checked]:[--tilt-bg:var(--riso-yellow)] [[data-skin=plate]_&]:data-[state=checked]:[--tilt-shadow:4px_4px_0_var(--riso-pink)] [[data-skin=plate]_&]:data-[state=checked]:[--tilt:-1deg]",
        className
      )}
      {...props}
    >
      <span className="mt-1 inline-flex size-[18px] shrink-0 items-center justify-center rounded-full border-2 border-hairline-strong bg-surface-control group-data-[state=checked]/radio-card:border-ink [[data-skin=plate]_&]:border-ink">
        <RadioGroupPrimitive.Indicator className="size-2.5 rounded-full bg-ink" />
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[14.5px] font-semibold [[data-skin=plate]_&]:font-black">{title}</span>
        {description && <span className="text-[13px] leading-[1.6] text-ink-700">{description}</span>}
      </span>
    </RadioGroupPrimitive.Item>
  )
}

export { RadioCard, RadioCards }
