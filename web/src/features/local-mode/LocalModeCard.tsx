import { useEffect, useId, useRef, useState } from 'react'
import { useLocation } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings, useUpdateLocalModeSettings } from '@/api/local-mode'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'

export const LOCAL_MODE_ANCHOR = 'local-mode'
// 其他页面的提示（Git 拉取失败、本机目录清单）链到这里，打开全局设置并滚动到本机模式。
export const localModeSettingsPath = `/settings#${LOCAL_MODE_ANCHOR}`

type Key = keyof Schemas['LocalModeSettings']
const KEYS: Key[] = ['directories', 'git', 'cli', 'logins']

function SwitchRow({ name, checked, disabled, onChange }: { name: Key; checked: boolean; disabled: boolean; onChange: (value: boolean) => void }) {
  const id = useId()
  const copy = t.localMode.settings.items[name]
  return (
    <li className="flex items-start gap-4 rounded-card bg-white px-4 py-3.5">
      <div className="grid min-w-0 flex-1 gap-1">
        <label htmlFor={id} className="cursor-pointer text-[15px] font-black">
          {copy.label}
        </label>
        <p id={`${id}-description`} className="text-[13.5px] leading-[1.7] text-ink-700">
          {copy.description}
        </p>
      </div>
      <Switch id={id} className="mt-0.5" checked={checked} disabled={disabled} aria-describedby={`${id}-description`} onCheckedChange={onChange} />
    </li>
  )
}

// 全局设置里的“本机模式”：四个开关，按下即保存，每次只改那一项。读不到当前值时不显示开关，免得按默认值误导。
// “用本机的登录状态”等于让 Agent 以你本人的身份使用本机工具，打开前先确认一次；关闭不用确认。
export function LocalModeCard() {
  const settings = useLocalModeSettings()
  const update = useUpdateLocalModeSettings()
  const { hash } = useLocation()
  const ref = useRef<HTMLDivElement>(null)
  const [confirmingLogins, setConfirmingLogins] = useState(false)
  const confirm = t.localMode.settings.loginsConfirm

  const change = (key: Key, value: boolean) => {
    if (key === 'logins' && value) setConfirmingLogins(true)
    else update.mutate({ [key]: value })
  }

  // 带着 #local-mode 打开时滚动到这里；等开关读到后再滚，免得卡片还没撑开。
  const loaded = settings.isSuccess
  useEffect(() => {
    if (hash === `#${LOCAL_MODE_ANCHOR}`) ref.current?.scrollIntoView({ block: 'start' })
  }, [hash, loaded])

  return (
    <Card ref={ref} id={LOCAL_MODE_ANCHOR} className="scroll-mt-6 gap-4" role="region" aria-labelledby={`${LOCAL_MODE_ANCHOR}-title`}>
      <CardHeader>
        <CardTitle id={`${LOCAL_MODE_ANCHOR}-title`}>{t.localMode.settings.title}</CardTitle>
        <CardDescription className="text-[14px] text-ink-700">{t.localMode.settings.intro}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <ErrorAlert error={settings.error} />
        {settings.data && (
          <ul className="grid gap-2.5">
            {KEYS.map((key) => (
              <SwitchRow
                key={key}
                name={key}
                checked={settings.data[key]}
                disabled={update.isPending}
                onChange={(value) => change(key, value)}
              />
            ))}
          </ul>
        )}
        <ErrorAlert error={confirmingLogins ? null : update.error} />
        <p className="text-[12.5px] leading-[1.7] text-ink-500">{t.localMode.settings.effect}</p>
      </CardContent>
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
    </Card>
  )
}
