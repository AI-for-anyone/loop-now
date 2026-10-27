import * as React from "react"
import { cn } from "@/lib/utils"
import { fieldClass } from "@/lib/field"

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(fieldClass, "flex field-sizing-content min-h-16 py-3 leading-[1.7]", className)}
      {...props}
    />
  )
}

export { Textarea }
