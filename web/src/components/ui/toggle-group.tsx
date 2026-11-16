import * as React from "react"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Segmented control: a light blue track, the selected item yellow with a pink shadow; single choice, the left and right arrow keys move
// focus between options, Space or Enter selects.
// Pressing the selected item again does not deselect it; one item is always selected. Used for the overview's time range and dimension,
// and reused by the inbox filters.
function ToggleGroup({
  className,
  value,
  onValueChange,
  ...props
}: Omit<React.ComponentProps<typeof ToggleGroupPrimitive.Root>, "type" | "value" | "defaultValue" | "onValueChange"> & {
  value: string
  onValueChange: (value: string) => void
}) {
  return (
    <ToggleGroupPrimitive.Root
      data-slot="toggle-group"
      type="single"
      value={value}
      onValueChange={(next) => {
        if (next) onValueChange(next)
      }}
      className={cn("inline-flex flex-wrap gap-1 rounded-full bg-tint-blue p-1", className)}
      {...props}
    />
  )
}

function ToggleGroupItem({ className, ...props }: React.ComponentProps<typeof ToggleGroupPrimitive.Item>) {
  return (
    <ToggleGroupPrimitive.Item
      data-slot="toggle-group-item"
      className={cn(
        "inline-flex min-h-[34px] items-center justify-center rounded-full px-4 text-[13.5px] font-black whitespace-nowrap text-ink-700 outline-none hover:text-ink focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40 data-[state=on]:bg-riso-yellow data-[state=on]:text-ink data-[state=on]:shadow-[2px_2px_0_var(--riso-pink)] data-[state=on]:focus-visible:shadow-focus",
        className
      )}
      {...props}
    />
  )
}

export { ToggleGroup, ToggleGroupItem }
