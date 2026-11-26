import * as React from "react"
import { fieldClass } from "@/lib/field"
import { cn } from "@/lib/utils"

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(fieldClass, "h-9 file:mr-3 file:border-0 file:bg-transparent file:text-[13px] file:font-medium [[data-skin=plate]_&]:h-11 [[data-skin=plate]_&]:file:text-[13.5px] [[data-skin=plate]_&]:file:font-bold", className)}
      {...props}
    />
  )
}

export { Input }
