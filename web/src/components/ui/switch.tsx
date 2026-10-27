import * as React from "react"
import { Switch as SwitchPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

// 开关：藏青描边的胶囊，打开时孔版蓝底、圆钮在右。名称用 aria-label 或关联的 label 给出。
function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-ink bg-white p-0.5 outline-none transition-colors focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:bg-riso-blue motion-reduce:transition-none",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="block size-4 rounded-full border-2 border-ink bg-white transition-transform data-[state=checked]:translate-x-5 motion-reduce:transition-none"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
