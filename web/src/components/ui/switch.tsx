import * as React from "react"
import { Switch as SwitchPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// 开关（改版）：2px 藏青描边的胶囊，里面一个藏青圆钮；关时浅纸底、圆钮在左，开时黄底（选中态一律黄色）、圆钮在右。
// 名称用 aria-label 或关联的 label 给出。
function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer inline-flex h-[26px] w-[46px] shrink-0 cursor-pointer items-center rounded-full border-2 border-ink bg-paper p-0.5 outline-none transition-colors focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:bg-riso-yellow motion-reduce:transition-none",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="block size-[18px] rounded-full bg-ink transition-transform data-[state=checked]:translate-x-5 motion-reduce:transition-none"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
