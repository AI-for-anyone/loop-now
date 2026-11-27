import * as React from "react"
import { RadioGroup as RadioGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Card-style radio group driven by semantic theme tokens.
function RadioCards({ className, ...props }: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return <RadioGroupPrimitive.Root data-slot="radio-cards" className={cn("grid gap-2.5", className)} {...props} />
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
        "group/radio-card flex w-full cursor-pointer items-start gap-2.5 rounded-card border border-hairline bg-surface-control px-3.5 py-3 text-left text-ink outline-none transition-[background-color,border-color,box-shadow] duration-150 hover:border-hairline-strong hover:bg-surface-subtle focus-visible:shadow-focus data-[state=checked]:border-riso-blue data-[state=checked]:bg-tint-blue/40 data-[state=checked]:shadow-[0_0_0_1px_var(--riso-blue)] disabled:cursor-not-allowed disabled:opacity-40",
        className
      )}
      {...props}
    >
      <span className="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center rounded-full border-[1.5px] border-hairline-strong bg-surface-control transition-colors group-data-[state=checked]/radio-card:border-riso-blue">
        <RadioGroupPrimitive.Indicator className="size-2 rounded-full bg-riso-blue" />
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[13.5px] leading-snug font-medium">{title}</span>
        {description && <span className="text-[12.5px] leading-[1.55] text-ink-500">{description}</span>}
      </span>
    </RadioGroupPrimitive.Item>
  )
}

export { RadioCard, RadioCards }
