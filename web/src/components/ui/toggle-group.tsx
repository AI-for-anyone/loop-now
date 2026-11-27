import * as React from "react"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Segmented control: unified Vercel Geist recessed track with elevated active pill in mono skin;
// light blue pill track with yellow/pink active state in Plate skin.
// Single choice: left and right arrow keys move focus between options, Space or Enter selects.
// Pressing the selected item again does not deselect it; one item is always selected.
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
      className={cn(
        "inline-flex flex-wrap items-center gap-0.5 rounded-[8px] border border-hairline bg-segment-track p-[3px]",
        className
      )}
      {...props}
    />
  )
}

function ToggleGroupItem({ className, ...props }: React.ComponentProps<typeof ToggleGroupPrimitive.Item>) {
  return (
    <ToggleGroupPrimitive.Item
      data-slot="toggle-group-item"
      className={cn(
        "inline-flex h-7 cursor-pointer items-center justify-center rounded-[5px] px-2.5 text-[12.5px] font-medium whitespace-nowrap text-ink-500 outline-none transition-[background-color,color,box-shadow] duration-150 hover:text-ink focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40 data-[state=on]:bg-segment-active data-[state=on]:text-ink data-[state=on]:shadow-[0_1px_2px_rgb(0_0_0/0.08),0_0_0_1px_var(--hairline)] data-[state=on]:focus-visible:shadow-focus motion-reduce:transition-none",
        className
      )}
      {...props}
    />
  )
}

export { ToggleGroup, ToggleGroupItem }
