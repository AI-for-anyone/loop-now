import { useEffect, useId, useState } from 'react'
import { useLocation } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings, useUpdateLocalModeSettings } from '@/api/local-mode'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'

export const LOCAL_MODE_ANCHOR = 'local-mode'
// 其他页面的提示（Git 拉取失败、本机目录清单）链到这里，打开全局设置并滚动到本机模式。
export const localModeSettingsPath = `/settings#${LOCAL_MODE_ANCHOR}`

type Key = keyof Schemas['LocalModeSettings']
const KEYS: Key[] = ['directories', 'git', 'cli', 'logins']

// 一项开关：名称与说明在左，开关在右，旁边写着“开”或“关”；行与行之间 1.5px 虚线（改版）。
function SwitchRow({ name, checked, disabled, onChange }: { name: Key; checked: boolean; disabled: boolean; onChange: (value: boolean) => void }) {
  const id = useId()
  const copy = t.localMode.settings.items[name]
  return (
    <li className="flex items-center gap-4 border-b-[1.5px] border-dashed border-ink/25 px-1 py-3">
      <div className="grid min-w-0 flex-1 gap-0.5 leading-[1.5]">
        <label htmlFor={id} className="cursor-pointer text-[14.5px] font-black">
          {copy.label}
        </label>
        <p id={`${id}-description`} className="text-[12.5px] text-ink-700">
          {copy.description}
        </p>
      </div>
      <Switch id={id} checked={checked} disabled={disabled} aria-describedby={`${id}-description`} onCheckedChange={onChange} />
      <span aria-hidden="true" className="-ml-2 w-4 text-[12px] font-black">
        {checked ? t.localMode.settings.on : t.localMode.settings.off}
      </span>
    </li>
  )
}

// 全局设置里的“本机模式”：四个开关，按下即保存，每次只改那一项。读不到当前值时不显示开关，免得按默认值误导。
// “用本机的登录状态”等于让 Agent 以你本人的身份使用本机工具，打开前先确认一次；关闭不用确认。
// 改版后是全局设置纸上的一个编号分区的内容（分区的 id 是 LOCAL_MODE_ANCHOR，标题由分区给出）。
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

  // 带着 #local-mode 打开时滚动到这一区；等开关读到后再滚一次，免得内容还没撑开。
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
      {/* 确认框里的保存失败留在确认框里显示；关掉确认框时清掉，免得卡片上再显示一遍。 */}
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
