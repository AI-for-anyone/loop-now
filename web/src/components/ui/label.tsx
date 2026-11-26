import * as React from "react"
import { cn } from "@/lib/utils"
import { Label as LabelPrimitive } from "radix-ui"

// Form field label: crisp Vercel Geist 12.5px medium secondary ink in mono skin; 13.5px black ink in Plate skin.
function Label({
  className,
  ...props
}: React.ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn(
        "inline-flex items-center gap-1.5 text-[12.5px] leading-4 font-medium tracking-[-0.005em] text-ink-700 select-none group-data-[disabled=true]:pointer-events-none group-data-[disabled=true]:opacity-40 peer-disabled:cursor-not-allowed peer-disabled:opacity-40 [[data-skin=plate]_&]:flex [[data-skin=plate]_&]:gap-2 [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:leading-snug [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-normal [[data-skin=plate]_&]:text-ink",
        className
      )}
      {...props}
    />
  )
}

export { Label }
