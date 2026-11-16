import { createContext, useContext, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { fieldsOf } from './schema-form'

type Manifest = Schemas['PluginManifest']
const text = t.plugins.confirm

export function Chips({ items, className }: { items: string[]; className?: string }) {
  if (!items.length) return null
  return (
    <ul className={cn('flex flex-wrap gap-1.5', className)}>
      {items.map((item) => (
        <li key={item} className="rounded-[6px] bg-ink/[0.06] px-2 py-0.5 text-[12px] leading-[18px] font-bold text-ink-700">
          {item}
        </li>
      ))}
    </ul>
  )
}

// table：插件详情概览里的样子（改版第 5 步）——每组一行，左边组名，右边每项一个小标签（黄色；“无”是米灰色）加一句说明；
// 表头下一道实线，行间虚线。groups：安装前确认里的样子，每组一个小标题、每项一行。
type Layout = 'groups' | 'table'
const LayoutContext = createContext<Layout>('groups')

function Row({ name, hint }: { name: ReactNode; hint?: ReactNode }) {
  const layout = useContext(LayoutContext)
  if (layout === 'table')
    return (
      <li className="flex min-w-0 flex-wrap items-baseline gap-x-2.5 gap-y-1 text-[13.5px] leading-[1.6] [overflow-wrap:anywhere]">
        <span className={cn('rounded-[4px] px-2 text-[12.5px] leading-[22px] font-black', name === text.none ? 'bg-paper-tab text-ink-700' : 'bg-riso-yellow text-ink')}>{name}</span>
        {hint && <><span className="sr-only"> · </span><span className="min-w-0 text-ink-700">{hint}</span></>}
      </li>
    )
  return (
    <li className="min-w-0 rounded-input bg-ink/[0.04] px-3 py-2 text-[13px] leading-snug [overflow-wrap:anywhere]">
      <span className="font-black text-ink">{name}</span>
      {hint && <span className="text-ink-700"> · {hint}</span>}
    </li>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  const layout = useContext(LayoutContext)
  if (layout === 'table')
    return (
      <section aria-label={title} className="grid min-w-0 gap-x-5 gap-y-1.5 border-b-[1.5px] border-dashed border-ink/25 px-1 py-2.5 sm:grid-cols-[112px_minmax(0,1fr)]">
        <h3 className="text-[13.5px] leading-[22px] font-black">{title}</h3>
        <ul className="grid min-w-0 gap-1.5">{children}</ul>
      </section>
    )
  return (
    <section className="grid min-w-0 gap-1.5">
      <h3 className="text-[13px] font-black text-ink-700">{title}</h3>
      <ul className="grid min-w-0 gap-1.5">{children}</ul>
    </section>
  )
}

const list = text.list

// 安装前确认与插件详情共用：两侧各有什么、订阅的事件、要的权限，每项一行说明。
// preview：安装或更新前的预览，Pi 扩展一行另说明注册的模型提供方在确认后才读取。
export function ManifestSummary({ manifest, columns = true, preview, layout = 'groups' }: { manifest: Manifest; columns?: boolean; preview?: 'install' | 'update'; layout?: Layout }) {
  const c = manifest.components
  const configFields = fieldsOf(manifest.configSchema)
  const secretCount = configFields.filter((field) => field.secret).length
  const platformRows = [
    c.platform && <Row key="code" name={text.platformCode} hint={text.platformCodeHint} />,
    c.channels.length > 0 && <Row key="channels" name={text.channels} hint={list(c.channels.map((channel) => channel.label))} />,
    c.channels.some((channel) => channel.manageIdentities) && (
      <Row key="manage" name={text.manageIdentities} hint={`${list(c.channels.filter((channel) => channel.manageIdentities).map((channel) => channel.label))} · ${text.manageIdentitiesHint}`} />
    ),
    ...manifest.accounts.map((account) => <Row key={`account-${account.kind}`} name={text.accountKind(account.label)} hint={list(fieldsOf(account.fields).map((field) => field.label))} />),
    configFields.length > 0 && <Row key="config" name={text.config(configFields.length)} hint={secretCount ? text.secrets(secretCount) : list(configFields.map((field) => field.label))} />,
    c.http && <Row key="http" name={text.http} hint={text.httpHint} />,
    c.schedules.length > 0 && <Row key="schedule" name={text.schedule} hint={list(c.schedules.map((schedule) => text.scheduleItem(schedule.id, schedule.cron)))} />,
    c.actions.length > 0 && <Row key="actions" name={text.actions} hint={list(c.actions.map((action) => action.label))} />,
  ].filter(Boolean)
  const agentRows = [
    c.agentExtensions > 0 && <Row key="ext" name={text.extensions(c.agentExtensions)} hint={preview ? text.extensionsPreviewHint[preview] : text.extensionsHint} />,
    c.skills.length > 0 && <Row key="skills" name={text.skills} hint={list(c.skills)} />,
    c.mcpServers.length > 0 && <Row key="mcp" name={text.mcp} hint={list(c.mcpServers)} />,
    c.bin.length > 0 && <Row key="bin" name={text.bin} hint={`${list(c.bin)} · ${text.binHint}`} />,
  ].filter(Boolean)
  const table = layout === 'table'
  return (
    <LayoutContext.Provider value={layout}>
    <div className={cn('grid min-w-0', table ? 'border-t-2 border-ink' : 'gap-4', columns && !table && 'md:grid-cols-2')}>
      <div className={cn('grid min-w-0 content-start', !table && 'gap-4')}>
        <Group title={text.platformHalf}>{platformRows.length ? platformRows : <Row name={text.none} hint={text.noPlatform} />}</Group>
        <Group title={text.agentHalf}>{agentRows.length ? agentRows : <Row name={text.none} hint={text.noAgent} />}</Group>
      </div>
      <div className={cn('grid min-w-0 content-start', !table && 'gap-4')}>
        <Group title={text.events}>
          {manifest.events.length ? (
            manifest.events.map((event) => <Row key={event} name={event} hint={t.plugins.eventHints[event as keyof typeof t.plugins.eventHints]} />)
          ) : (
            <Row name={text.none} hint={text.noEvents} />
          )}
        </Group>
        <Group title={text.permissions}>
          {manifest.permissions.length ? (
            manifest.permissions.map((permission) => <Row key={permission} name={permission} hint={t.plugins.permissionHints[permission]} />)
          ) : (
            <Row name={text.none} hint={text.noPermissions} />
          )}
        </Group>
        {manifest.failClosed && <p className={cn('text-[12.5px] font-bold text-red-text', table && 'px-1 pt-2.5')}>{text.failClosed}</p>}
      </div>
    </div>
    </LayoutContext.Provider>
  )
}
