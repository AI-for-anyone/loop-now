import type { ReactElement } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { commandText, type Command } from './slash-commands'

const text = t.chat.commands

// 输入框上方的平台与 Pi 命令菜单：锚在输入框外框上、向上弹出，焦点一直留在输入框里（上下键、Enter、Tab、Esc 由输入框处理）。
// 菜单是一小张纸（浅纸底、藏青描边、孔版蓝投影），选中的一项是黄色。
// 每行是补全后的写法（/pi:<name>）、来源标签（平台命令藏青底白字；扩展命令、提示模板、技能浅蓝底，来自插件的另写插件名）
// 和命令自己的说明（扩展原文，按纯文本显示）。
// status：读取中、读取失败、还没有列表、没有匹配时的一行说明，有它时不列命令。
export function SlashCommandMenu({ open, listId, optionId, items, active, status, onActive, onChoose, onDismiss, children }: {
  open: boolean
  listId: string
  optionId: (index: number) => string
  items: Command[]
  active: number
  status: string | null
  onActive: (index: number) => void
  onChoose: (command: Command) => void
  onDismiss: () => void
  children: ReactElement
}) {
  return (
    <PopoverPrimitive.Root open={open} onOpenChange={(next) => !next && onDismiss()}>
      <PopoverPrimitive.Anchor asChild>{children}</PopoverPrimitive.Anchor>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          role="presentation"
          side="top"
          align="start"
          sideOffset={8}
          collisionPadding={8}
          onOpenAutoFocus={(event) => event.preventDefault()}
          onCloseAutoFocus={(event) => event.preventDefault()}
          // 在输入框里点击（挪光标）不算点到外面。
          onInteractOutside={(event) => {
            if (event.target instanceof Element && event.target.closest('[data-chat-composer]')) event.preventDefault()
          }}
          className="z-50 flex max-h-[min(360px,var(--radix-popover-content-available-height))] w-(--radix-popover-trigger-width) min-w-0 flex-col overflow-hidden rounded-card bg-paper-raised text-ink shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)] outline-none"
        >
          {status ? (
            <p role="status" className="px-4 py-3 text-[13.5px] text-ink-700">
              {status}
            </p>
          ) : (
            <>
              <div id={listId} role="listbox" aria-label={text.menuLabel} className="min-h-0 overflow-y-auto p-1.5">
                {items.map((command, index) => {
                  const source = command.source === 'pi' ? text.source[command.piSource] ?? command.piSource : text.platform
                  const plugin = command.pluginDisplayName !== null ? localizedText(command.pluginDisplayName) || command.pluginId : command.pluginId
                  return (
                    <div
                      key={commandText(command)}
                      id={optionId(index)}
                      role="option"
                      aria-selected={index === active}
                      onMouseDown={(event) => event.preventDefault()}
                      onMouseEnter={() => onActive(index)}
                      onClick={() => onChoose(command)}
                      className="grid min-w-0 cursor-pointer gap-0.5 rounded-ticket px-2.5 py-2 aria-selected:bg-riso-yellow"
                    >
                      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                        <code className="min-w-0 font-mono text-[13px] font-bold [overflow-wrap:anywhere]">{command.source === 'platform' ? command.usage : commandText(command)}</code>
                        <span className={cn('px-2 text-[11px] leading-[18px]', command.source === 'platform' ? 'rounded-full bg-ink font-black text-white' : 'rounded-stamp bg-tint-blue font-bold text-ink')}>
                          {[source, plugin && text.plugin(plugin)].filter(Boolean).join(' · ')}
                        </span>
                      </span>
                      {command.description && <span className="line-clamp-2 text-[12.5px] leading-snug text-ink-700 [overflow-wrap:anywhere]">{command.description}</span>}
                    </div>
                  )
                })}
              </div>
              <p className="border-t-[1.5px] border-dashed border-ink/25 px-3 py-1.5 text-[11.5px] text-ink-500">{text.keys}</p>
            </>
          )}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
