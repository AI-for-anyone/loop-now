import * as React from "react"
import { cn } from "@/lib/utils"

// 表格：外层容器可以单独横向滚动，窄屏时整页不出现横向滚动；表头浅蓝加粗，行之间是 2px 虚线。
// 容器带 relative：表格里绝对定位的读屏文字（sr-only）以它为包含块，随表格一起滚动，不会把整页撑宽。
function Table({ className, ...props }: React.ComponentProps<"table">) {
  return (
    <div data-slot="table-container" className="relative w-full max-w-full overflow-x-auto rounded-card bg-white shadow-print-rest">
      <table data-slot="table" className={cn("w-full border-collapse text-left text-[14px]", className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return <thead data-slot="table-header" className={cn("bg-tint-blue", className)} {...props} />
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return <tbody data-slot="table-body" className={className} {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn("border-b-2 border-dashed border-ink/15 last:border-b-0", className)}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return <th data-slot="table-head" className={cn("px-3.5 py-2 font-black whitespace-nowrap", className)} {...props} />
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return <td data-slot="table-cell" className={cn("px-3.5 py-2 align-top", className)} {...props} />
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow }
