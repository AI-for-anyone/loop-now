import * as React from "react"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// Segmented control: subtle hairline track with crisp active fill in mono skin; light blue pill track with yellow/pink active state in Plate skin.
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
        "inline-flex flex-wrap gap-1 rounded-input border border-hairline bg-surface-subtle p-1 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue",
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
        "inline-flex min-h-[32px] items-center justify-center rounded-ticket px-3.5 text-[13px] font-medium whitespace-nowrap text-ink-500 outline-none transition-colors hover:text-ink focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40 data-[state=on]:bg-surface-control data-[state=on]:font-semibold data-[state=on]:text-ink data-[state=on]:shadow-surface data-[state=on]:focus-visible:shadow-focus [[data-skin=plate]_&]:min-h-[34px] [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow [[data-skin=plate]_&]:data-[state=on]:text-ink [[data-skin=plate]_&]:data-[state=on]:shadow-[2px_2px_0_var(--riso-pink)]",
        className
      )}
      {...props}
    />
  )
}

export { ToggleGroup, ToggleGroupItem }
