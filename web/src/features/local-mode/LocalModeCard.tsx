import { useEffect, useId, useRef } from 'react'
import { useLocation } from 'react-router'
import type { Schemas } from '@/api/client'
import { useLocalModeSettings, useUpdateLocalModeSettings } from '@/api/local-mode'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'

export const LOCAL_MODE_ANCHOR = 'local-mode'
// 其他页面的提示（Git 拉取失败、本机目录清单）链到这里，打开全局设置并滚动到本机模式。
export const localModeSettingsPath = `/settings#${LOCAL_MODE_ANCHOR}`

type Key = keyof Schemas['LocalModeSettings']
const KEYS: Key[] = ['directories', 'git', 'cli']

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

// 全局设置里的“本机模式”：三个开关，按下即保存，每次只改那一项。读不到当前值时不显示开关，免得按默认值误导。
export function LocalModeCard() {
  const settings = useLocalModeSettings()
  const update = useUpdateLocalModeSettings()
  const { hash } = useLocation()
  const ref = useRef<HTMLDivElement>(null)

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
                onChange={(value) => update.mutate({ [key]: value })}
              />
            ))}
          </ul>
        )}
        <ErrorAlert error={update.error} />
        <p className="text-[12.5px] leading-[1.7] text-ink-500">{t.localMode.settings.effect}</p>
      </CardContent>
    </Card>
  )
}
