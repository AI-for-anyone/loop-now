import * as React from "react"
import { cn } from "@/lib/utils"

// Table: the outer container can scroll horizontally on its own, so the whole page never scrolls horizontally on narrow screens.
// Clean hairline rows and subtle header surface in mono skin; light blue header and 2px dashed row dividers in Plate skin.
// The container is relative: absolutely positioned screen-reader text (sr-only) in the table uses it as its containing block, scrolls
// along with the table and does not widen the whole page.
// card: the outer horizontal scroll area is wrapped in an elevated card (default); tables placed directly on the sheet pass false.
function Table({ className, card = true, ...props }: React.ComponentProps<"table"> & { card?: boolean }) {
  return (
    <div
      data-slot="table-container"
      className={cn(
        "relative w-full max-w-full overflow-x-auto",
        card &&
          "rounded-card border border-hairline bg-surface-elevated shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-surface-control [[data-skin=plate]_&]:shadow-print-rest"
      )}
    >
      <table data-slot="table" className={cn("w-full border-collapse text-left text-[14px]", className)} {...props} />
    </div>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <thead
      data-slot="table-header"
      className={cn("border-b border-hairline bg-surface-subtle [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:bg-tint-blue", className)}
      {...props}
    />
  )
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return <tbody data-slot="table-body" className={className} {...props} />
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn("border-b border-hairline last:border-b-0 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/15", className)}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        "px-3.5 py-2.5 text-[12.5px] font-semibold whitespace-nowrap text-ink-500 [[data-skin=plate]_&]:py-2 [[data-skin=plate]_&]:text-[14px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink",
        className
      )}
      {...props}
    />
  )
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return <td data-slot="table-cell" className={cn("px-3.5 py-2.5 align-top [[data-skin=plate]_&]:py-2", className)} {...props} />
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow }
