import * as React from "react"
import { cn } from "@/lib/utils"
import { Label as LabelPrimitive } from "radix-ui"

// Form field label: 13.5 / 500 in mono skin, 900 in Plate skin.
function Label({
  className,
  ...props
}: React.ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn(
        "flex items-center gap-2 text-[13.5px] leading-snug font-medium text-ink select-none group-data-[disabled=true]:pointer-events-none group-data-[disabled=true]:opacity-40 peer-disabled:cursor-not-allowed peer-disabled:opacity-40 [[data-skin=plate]_&]:font-black",
        className
      )}
      {...props}
    />
  )
}

export { Label }
