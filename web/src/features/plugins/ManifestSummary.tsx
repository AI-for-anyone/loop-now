import { createContext, useContext, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { fieldsOf } from './schema-form'
import { localizedText } from '@/lib/localized-text'

type Manifest = Schemas['PluginManifest']
const text = t.plugins.confirm

export function Chips({ items, className }: { items: string[]; className?: string }) {
  if (!items.length) return null
  return (
    <ul className={cn('flex flex-wrap gap-1.5', className)}>
      {items.map((item) => (
        <li key={item} className="rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 text-[12px] leading-[18px] font-medium text-ink-700 [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.06] [[data-skin=plate]_&]:font-bold">
          {item}
        </li>
      ))}
    </ul>
  )
}

// table: how it looks in the plugin detail overview (redesign step 5): one row per group, group name on the left, and on the right each
// item as a small tag plus a one-sentence explanation;
// groups: how it looks in the pre-install confirmation, with a small heading per group and one row per item.
type Layout = 'groups' | 'table'
const LayoutContext = createContext<Layout>('groups')

function Row({ name, hint }: { name: ReactNode; hint?: ReactNode }) {
  const layout = useContext(LayoutContext)
  if (layout === 'table')
    return (
      <li className="flex min-w-0 flex-wrap items-baseline gap-x-2.5 gap-y-1 text-[13.5px] leading-[1.6] [overflow-wrap:anywhere]">
        <span
          className={cn(
            'rounded-stamp px-2 text-[12.5px] leading-[22px] font-medium [[data-skin=plate]_&]:rounded-[4px] [[data-skin=plate]_&]:font-black',
            name === text.none
              ? 'border border-hairline bg-surface-subtle text-ink-500 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-tab [[data-skin=plate]_&]:text-ink-700'
              : 'border border-hairline-strong bg-surface-active text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-yellow',
          )}
        >
          {name}
        </span>
        {hint && <><span className="sr-only"> · </span><span className="min-w-0 text-ink-700">{hint}</span></>}
      </li>
    )
  return (
    <li className="min-w-0 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13px] leading-snug [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.04]">
      <span className="font-semibold text-ink [[data-skin=plate]_&]:font-black">{name}</span>
      {hint && <span className="text-ink-700"> · {hint}</span>}
    </li>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  const layout = useContext(LayoutContext)
  if (layout === 'table')
    return (
      <section aria-label={title} className="grid min-w-0 gap-x-5 gap-y-1.5 border-b border-hairline px-1 py-2.5 sm:grid-cols-[112px_minmax(0,1fr)] [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
        <h3 className="text-[13.5px] leading-[22px] font-semibold [[data-skin=plate]_&]:font-black">{title}</h3>
        <ul className="grid min-w-0 gap-1.5">{children}</ul>
      </section>
    )
  return (
    <section className="grid min-w-0 gap-1.5">
      <h3 className="text-[13px] font-semibold text-ink-700 [[data-skin=plate]_&]:font-black">{title}</h3>
      <ul className="grid min-w-0 gap-1.5">{children}</ul>
    </section>
  )
}

const list = text.list

// Shared by the pre-install confirmation and the plugin details: what each side contains, the events it subscribes to and the permissions
// it asks for, one line of explanation per item.
// preview: the preview before installing or updating; the Pi extension row additionally explains that the model providers it registers are
// read only after confirming.
export function ManifestSummary({ manifest, columns = true, preview, layout = 'groups' }: { manifest: Manifest; columns?: boolean; preview?: 'install' | 'update'; layout?: Layout }) {
  const c = manifest.components
  const configFields = fieldsOf(manifest.configSchema)
  const secretCount = configFields.filter((field) => field.secret).length
  const platformRows = [
    c.platform && <Row key="code" name={text.platformCode} hint={text.platformCodeHint} />,
    c.channels.length > 0 && <Row key="channels" name={text.channels} hint={list(c.channels.map((channel) => localizedText(channel.label)))} />,
    c.channels.some((channel) => channel.manageIdentities) && (
      <Row key="manage" name={text.manageIdentities} hint={`${list(c.channels.filter((channel) => channel.manageIdentities).map((channel) => localizedText(channel.label)))} · ${text.manageIdentitiesHint}`} />
    ),
    ...manifest.accounts.map((account) => <Row key={`account-${account.kind}`} name={text.accountKind(localizedText(account.label))} hint={list(fieldsOf(account.fields).map((field) => field.label))} />),
    configFields.length > 0 && <Row key="config" name={text.config(configFields.length)} hint={secretCount ? text.secrets(secretCount) : list(configFields.map((field) => field.label))} />,
    c.http && <Row key="http" name={text.http} hint={text.httpHint} />,
    c.schedules.length > 0 && <Row key="schedule" name={text.schedule} hint={list(c.schedules.map((schedule) => text.scheduleItem(schedule.id, schedule.cron)))} />,
    c.actions.length > 0 && <Row key="actions" name={text.actions} hint={list(c.actions.map((action) => localizedText(action.label)))} />,
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
    <div className={cn('grid min-w-0', table ? 'border-t border-hairline-strong [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink' : 'gap-4', columns && !table && 'md:grid-cols-2')}>
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
