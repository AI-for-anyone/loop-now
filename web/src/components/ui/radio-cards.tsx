import * as React from "react"
import { RadioGroup as RadioGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Card-style radio group: crisp Vercel Geist hairline cards with subtle Geist Blue selection state in mono skin;
// white cards with tilted yellow/pink background layer when selected in Plate skin.
function RadioCards({ className, ...props }: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return <RadioGroupPrimitive.Root data-slot="radio-cards" className={cn("grid gap-2.5 [[data-skin=plate]_&]:gap-3", className)} {...props} />
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
        "group/radio-card riso-tilt flex w-full cursor-pointer items-start gap-2.5 rounded-card border border-hairline bg-surface-control px-3.5 py-3 text-left text-ink outline-none transition-[background-color,border-color,box-shadow] duration-150 before:hidden hover:border-hairline-strong hover:bg-surface-subtle focus-visible:shadow-focus data-[state=checked]:border-riso-blue data-[state=checked]:bg-tint-blue/40 data-[state=checked]:shadow-[0_0_0_1px_var(--riso-blue)] disabled:cursor-not-allowed disabled:opacity-40 [[data-skin=plate]_&]:gap-3 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:[--tilt:0deg] [[data-skin=plate]_&]:before:block [[data-skin=plate]_&]:hover:bg-transparent [[data-skin=plate]_&]:data-[state=checked]:bg-transparent [[data-skin=plate]_&]:data-[state=checked]:shadow-none [[data-skin=plate]_&]:data-[state=checked]:[--tilt-bg:var(--riso-yellow)] [[data-skin=plate]_&]:data-[state=checked]:[--tilt-shadow:4px_4px_0_var(--riso-pink)] [[data-skin=plate]_&]:data-[state=checked]:[--tilt:-1deg]",
        className
      )}
      {...props}
    >
      <span className="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center rounded-full border-[1.5px] border-hairline-strong bg-surface-control transition-colors group-data-[state=checked]/radio-card:border-riso-blue [[data-skin=plate]_&]:mt-1 [[data-skin=plate]_&]:size-[18px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:group-data-[state=checked]/radio-card:border-ink">
        <RadioGroupPrimitive.Indicator className="size-2 rounded-full bg-riso-blue [[data-skin=plate]_&]:size-2.5 [[data-skin=plate]_&]:bg-ink" />
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[13.5px] leading-snug font-medium [[data-skin=plate]_&]:text-[14.5px] [[data-skin=plate]_&]:font-black">{title}</span>
        {description && <span className="text-[12.5px] leading-[1.55] text-ink-500 [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:text-ink-700">{description}</span>}
      </span>
    </RadioGroupPrimitive.Item>
  )
}

export { RadioCard, RadioCards }
