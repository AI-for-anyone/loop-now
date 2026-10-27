import * as React from "react"
import { fieldClass } from "@/lib/field"
import { cn } from "@/lib/utils"

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(fieldClass, "h-11 file:mr-3 file:border-0 file:bg-transparent file:text-[13.5px] file:font-bold", className)}
      {...props}
    />
  )
}

export { Input }
