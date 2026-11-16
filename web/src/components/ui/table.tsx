import * as React from "react"
import { cn } from "@/lib/utils"

// Table: the outer container can scroll horizontally on its own, so the whole page never scrolls horizontally on narrow screens; the header
// is light blue and bold, with 2px dashed lines between rows.
// The container is relative: absolutely positioned screen-reader text (sr-only) in the table uses it as its containing block, scrolls
// along with the table and does not widen the whole page.
// card: the outer horizontal scroll area is a white card (default); tables placed directly on the sheet pass false for no card.
function Table({ className, card = true, ...props }: React.ComponentProps<"table"> & { card?: boolean }) {
  return (
    <div data-slot="table-container" className={cn("relative w-full max-w-full overflow-x-auto", card && "rounded-card bg-white shadow-print-rest")}>
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
