import * as React from "react"
import { RadioGroup as RadioGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// 卡片式单选：白底卡片；选中时背景层换成黄底加粉色投影并轻微倾斜（只转 ::before，卡片本体不转）。
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
        "riso-tilt flex w-full items-start gap-3 rounded-card px-4 py-3 text-left text-ink outline-none [--tilt:0deg] focus-visible:shadow-focus data-[state=checked]:[--tilt-bg:var(--riso-yellow)] data-[state=checked]:[--tilt-shadow:4px_4px_0_var(--riso-pink)] data-[state=checked]:[--tilt:-1deg] disabled:opacity-40",
        className
      )}
      {...props}
    >
      <span className="mt-1 inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-white shadow-[inset_0_0_0_2px_var(--ink)]">
        <RadioGroupPrimitive.Indicator className="size-2.5 rounded-full bg-ink" />
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[14.5px] font-black">{title}</span>
        {description && <span className="text-[13px] leading-[1.6] text-ink-700">{description}</span>}
      </span>
    </RadioGroupPrimitive.Item>
  )
}

export { RadioCard, RadioCards }
