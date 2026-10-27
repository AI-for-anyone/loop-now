import * as React from "react"
import { Popover as PopoverPrimitive } from "radix-ui"
import { ChevronDownIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { fieldClass } from "@/lib/field"

export interface ComboboxOption {
  value: string
  label: string
  // 第二行的说明，例如模型 ID，用等宽字体显示。
  hint?: string
  // 分组标题：相邻且同组的选项排在一个标题下（例如收件人中的 Agent 与外部身份）。
  group?: string
}

interface ComboboxProps {
  id?: string
  value: string | null
  options: ComboboxOption[]
  onChange: (value: string) => void
  placeholder: string
  filterLabel: string
  emptyText: string
  invalid?: boolean
  disabled?: boolean
  "aria-labelledby"?: string
  "aria-describedby"?: string
  // 选项的完整说明，用作触发器上显示的文字和选项的无障碍名称；默认是 label。
  describe?: (option: ComboboxOption) => string
}

// 可过滤的下拉：触发器的外观同输入框；浮层内是过滤框和候选列表，支持上下键、回车与 Esc。
// 浮层为纸白底、2px 藏青描边加孔版蓝投影，选中项为黄色。
export function Combobox({
  id,
  value,
  options,
  onChange,
  placeholder,
  filterLabel,
  emptyText,
  invalid,
  disabled,
  describe = (option) => option.label,
  ...aria
}: ComboboxProps) {
  const [open, setOpen] = React.useState(false)
  const [filter, setFilter] = React.useState("")
  const [active, setActive] = React.useState(0)
  const listId = React.useId()
  const selected = options.find((option) => option.value === value)
  const keyword = filter.trim().toLowerCase()
  const visible = keyword
    ? options.filter((option) => `${option.label} ${option.hint ?? ""} ${option.value}`.toLowerCase().includes(keyword))
    : options

  const openChange = (next: boolean) => {
    setOpen(next)
    if (next) {
      setFilter("")
      setActive(Math.max(0, options.findIndex((option) => option.value === value)))
    }
  }

  const choose = (option: ComboboxOption | undefined) => {
    if (!option) return
    onChange(option.value)
    setOpen(false)
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault()
      setActive((index) => Math.min(index + 1, visible.length - 1))
    } else if (event.key === "ArrowUp") {
      event.preventDefault()
      setActive((index) => Math.max(index - 1, 0))
    } else if (event.key === "Enter") {
      event.preventDefault()
      choose(visible[active])
    }
  }

  const optionId = (index: number) => `${listId}-${index}`

  return (
    <PopoverPrimitive.Root open={open} onOpenChange={openChange}>
      <PopoverPrimitive.Trigger
        id={id}
        type="button"
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-invalid={invalid || undefined}
        disabled={disabled}
        className={cn(fieldClass, "flex h-11 items-center gap-2 text-left aria-expanded:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)]")}
        {...aria}
      >
        <span className={cn("min-w-0 flex-1 truncate", !selected && "text-ink-500")}>
          {selected ? describe(selected) : (value ?? placeholder)}
        </span>
        <ChevronDownIcon className="size-4 shrink-0" />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="start"
          sideOffset={8}
          className="z-50 flex w-(--radix-popover-trigger-width) min-w-64 flex-col gap-1.5 rounded-card bg-paper-raised p-2 text-ink shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)] outline-none"
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            ;(event.currentTarget as HTMLElement).querySelector("input")?.focus()
          }}
        >
          <input
            type="search"
            aria-label={filterLabel}
            placeholder={filterLabel}
            aria-controls={listId}
            aria-activedescendant={visible[active] ? optionId(active) : undefined}
            value={filter}
            onChange={(event) => {
              setFilter(event.target.value)
              setActive(0)
            }}
            onKeyDown={onKeyDown}
            className="h-[38px] w-full rounded-full border-0 bg-tint-blue px-3.5 text-[13.5px] text-ink outline-none placeholder:text-ink-500 focus-visible:shadow-[inset_0_0_0_2px_var(--ink)]"
          />
          <ul id={listId} role="listbox" aria-label={filterLabel} className="flex max-h-72 flex-col gap-0.5 overflow-y-auto">
            {visible.length === 0 && <li className="px-3 py-2 text-[13.5px] text-ink-500">{emptyText}</li>}
            {visible.map((option, index) => [
              option.group && option.group !== visible[index - 1]?.group && (
                <li key={`group:${option.group}`} role="presentation" className="px-3 pt-1.5 text-[12px] font-black text-ink-500">
                  {option.group}
                </li>
              ),
              <li
                key={option.value}
                id={optionId(index)}
                role="option"
                aria-label={describe(option)}
                aria-selected={option.value === value}
                onMouseDown={(event) => event.preventDefault()}
                onMouseEnter={() => setActive(index)}
                onClick={() => choose(option)}
                className={cn(
                  "flex min-h-[38px] cursor-pointer flex-wrap items-baseline gap-x-2.5 rounded-ticket px-3 py-1.5 text-[13.5px]",
                  option.value === value && "bg-riso-yellow font-bold",
                  index === active && option.value !== value && "bg-tint-blue"
                )}
              >
                <span className="font-bold">{option.label}</span>
                {option.hint && <span className="font-mono text-[12px] text-ink-500">{option.hint}</span>}
              </li>,
            ])}
          </ul>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
