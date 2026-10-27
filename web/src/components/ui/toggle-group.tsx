import * as React from "react"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// 分段控件：浅蓝轨道，选中项黄底加粉色投影；单选，左右方向键在选项之间移动焦点，空格或回车选中。
// 选中项不能通过再按一次取消，总有一项选中。用于概览的时间段与维度，收件箱的筛选也复用它。
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
