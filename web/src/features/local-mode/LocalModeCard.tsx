import { useEffect, useId, useState } from 'react'
import { useLocation } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings, useUpdateLocalModeSettings } from '@/api/local-mode'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'

export const LOCAL_MODE_ANCHOR = 'local-mode'
// Hints on other pages (failed Git pull, the local directories list) link here, which opens global settings and scrolls to
// local mode.
export const localModeSettingsPath = `/settings/workspace#${LOCAL_MODE_ANCHOR}`

type Key = keyof Schemas['LocalModeSettings']
const KEYS: Key[] = ['directories', 'git', 'cli', 'logins']

// One switch: name and description on the left, the switch on the right with “开” (On) or “关” (Off) next to it; 1.5px dashed
// lines between rows (redesign).
function SwitchRow({ name, checked, disabled, onChange }: { name: Key; checked: boolean; disabled: boolean; onChange: (value: boolean) => void }) {
  const id = useId()
  const copy = t.localMode.settings.items[name]
  return (
    <li className="flex items-center gap-4 border-b border-hairline px-1 py-3">
      <div className="grid min-w-0 flex-1 gap-0.5 leading-[1.5]">
        <label htmlFor={id} className="cursor-pointer text-[14.5px] font-semibold">
          {copy.label}
        </label>
        <p id={`${id}-description`} className="text-[12.5px] text-ink-700">
          {copy.description}
        </p>
      </div>
      <Switch id={id} checked={checked} disabled={disabled} aria-describedby={`${id}-description`} onCheckedChange={onChange} />
      <span aria-hidden="true" className="-ml-2 w-4 text-[12px] font-medium text-ink-700">
        {checked ? t.localMode.settings.on : t.localMode.settings.off}
      </span>
    </li>
  )
}

// “本机模式” (Local mode) in global settings: four switches, each saved as soon as it is toggled, changing only that one item.
// When the current values can't be read, the switches aren't shown, so the defaults don't mislead.
// “用本机的登录状态” (Use local logins) amounts to letting Agents use the tools on this computer as you, so turning it on asks
// for confirmation once; turning it off doesn't.
// After the redesign this is the content of one numbered section on the global settings sheet (the section's id is
// LOCAL_MODE_ANCHOR, and the section provides the title).
export function LocalModeCard() {
  const settings = useLocalModeSettings()
  const update = useUpdateLocalModeSettings()
  const { hash } = useLocation()
  const [confirmingLogins, setConfirmingLogins] = useState(false)
  const confirm = t.localMode.settings.loginsConfirm

  const change = (key: Key, value: boolean) => {
    if (key === 'logins' && value) setConfirmingLogins(true)
    else update.mutate({ [key]: value })
  }

  // When opened with #local-mode, scroll to this section; scroll again once the switches have loaded, in case the content
  // hadn't expanded yet.
  const loaded = settings.isSuccess
  useEffect(() => {
    if (hash === `#${LOCAL_MODE_ANCHOR}`) document.getElementById(LOCAL_MODE_ANCHOR)?.scrollIntoView({ block: 'start' })
  }, [hash, loaded])

  return (
    <div className="grid gap-3">
      <p className="text-[13.5px] text-ink-700">{t.localMode.settings.intro}</p>
      <ErrorAlert error={settings.error} />
      {settings.data && (
        <ul className="grid">
          {KEYS.map((key) => (
            <SwitchRow key={key} name={key} checked={settings.data[key]} disabled={update.isPending} onChange={(value) => change(key, value)} />
          ))}
        </ul>
      )}
      <ErrorAlert error={confirmingLogins ? null : update.error} />
      <p className="text-[12.5px] leading-[1.7] text-ink-500">{t.localMode.settings.effect}</p>
      {/* A save failure inside the confirmation dialog stays in the dialog; it is cleared when the dialog closes so the card
          doesn't show it a second time. */}
      <ConfirmDialog
        open={confirmingLogins}
        onOpenChange={(open) => {
          setConfirmingLogins(open)
          if (!open && update.isError) update.reset()
        }}
        title={confirm.title}
        body={confirm.body}
        confirmLabel={confirm.confirm}
        onConfirm={() => update.mutateAsync({ logins: true })}
      >
        <ul className="list-disc pl-5 text-[14.5px] leading-[1.75]">
          {confirm.points.map((point) => (
            <li key={point}>{point}</li>
          ))}
        </ul>
      </ConfirmDialog>
    </div>
  )
}
