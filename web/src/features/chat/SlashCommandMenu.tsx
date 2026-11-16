import type { ReactElement } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { commandDescription, commandText, type Command } from './slash-commands'

const text = t.chat.commands

// What starts a row in the menu: for a platform command its usage (e.g. /compact [instructions]), for a Pi command /pi:<name>.
const usageText = (command: Command) => command.usage ?? commandText(command)

// The platform and Pi command menu above the input box: anchored to the input box's outer frame and popping upward; focus always stays
// in the input box (up/down, Enter, Tab and Esc are handled by the input box).
// The menu is a small sheet of paper (light paper fill, navy border, riso blue shadow), and the selected item is yellow.
// Each row has the completed form (/pi:<name>), a source label (platform commands white on navy; extension commands, prompt templates
// and Skills on light blue, with the plugin name added for those from a plugin)
// and the command's own description (the extension's original text, shown as plain text).
// status: a one-line explanation while loading, when loading failed, when there's no list yet, or when nothing matches; when present,
// no commands are listed.
// blocked: platform commands that match but can't be used right now (runtime doesn't support it, no session yet, session file
// missing), grayed out below with the reason, not selectable.
export function SlashCommandMenu({ open, listId, optionId, items, blocked = [], active, status, onActive, onChoose, onDismiss, children }: {
  open: boolean
  listId: string
  optionId: (index: number) => string
  items: Command[]
  blocked?: Command[]
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
          // Clicking inside the input box (to move the caret) doesn't count as clicking outside.
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
              <div className="min-h-0 overflow-y-auto">
              {items.length > 0 && <div id={listId} role="listbox" aria-label={text.menuLabel} className="p-1.5">
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
                        <code className="min-w-0 font-mono text-[13px] font-bold [overflow-wrap:anywhere]">{usageText(command)}</code>
                        <span className={cn('px-2 text-[11px] leading-[18px]', command.source === 'platform' ? 'rounded-full bg-ink font-black text-white' : 'rounded-stamp bg-tint-blue font-bold text-ink')}>
                          {[source, plugin && text.plugin(plugin)].filter(Boolean).join(' · ')}
                        </span>
                      </span>
                      {commandDescription(command) && <span className="line-clamp-2 text-[12.5px] leading-snug text-ink-700 [overflow-wrap:anywhere]">{commandDescription(command)}</span>}
                    </div>
                  )
                })}
              </div>}
              {blocked.length > 0 && (
                // Clicking a grayed-out command does nothing, and focus stays in the input box.
                <div role="group" aria-label={text.unavailable.title} onMouseDown={(event) => event.preventDefault()} className={cn('px-1.5 pt-1.5 pb-1', items.length > 0 && 'border-t-[1.5px] border-dashed border-ink/25')}>
                  <p aria-hidden="true" className="px-2.5 text-[11px] leading-[18px] font-black text-ink-500">{text.unavailable.title}</p>
                  <ul>
                    {blocked.map((command) => (
                      <li key={commandText(command)} className="grid min-w-0 gap-0.5 px-2.5 py-1.5 text-ink-500">
                        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                          <code className="min-w-0 font-mono text-[13px] font-bold [overflow-wrap:anywhere]">{usageText(command)}</code>
                          <span className="rounded-full bg-ink/45 px-2 text-[11px] leading-[18px] font-black text-white">{text.platform}</span>
                        </span>
                        {command.unavailableReason && <span className="text-[12.5px] leading-snug [overflow-wrap:anywhere]">{text.unavailable.reasons[command.unavailableReason]}</span>}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              </div>
              {/* When no command is selectable (all matches are unusable), the footer instead says Enter will send the text as typed;
                  this sentence is also read out to screen readers. */}
              {items.length > 0 ? (
                <p className="border-t-[1.5px] border-dashed border-ink/25 px-3 py-1.5 text-[11.5px] text-ink-500">{text.keys}</p>
              ) : (
                <p role="status" className="border-t-[1.5px] border-dashed border-ink/25 px-3 py-1.5 text-[11.5px] text-ink-500">{text.unavailable.onlyBlocked}</p>
              )}
            </>
          )}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
