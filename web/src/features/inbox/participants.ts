import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

export type Participant = Schemas['InboxParticipant']

// How a participant is displayed: name, deleted mark and address.
// When the participant was deleted, the server's name is “已删除身份” (Deleted identity) or “已删除 Agent” (Deleted Agent), and
// address is still the original address: then the name taken from the original address is the title, and the server's
// “已删除…” (Deleted …) name is the mark; without an address, only the server's name is shown.
// From a plugin channel (kind plugin, address plugin:<id>/<channel>): the title reads “插件 · 名称” (Plugin · name), and when the
// plugin has been uninstalled it is marked “已卸载” (Uninstalled).
export function partyDisplay(party: Participant): { title: string; deleted: string | null; address: string | null } {
  if (party.kind === 'plugin') return { title: t.plugins.chat.sender(party.deleted ? (party.pluginId ?? party.name) : party.name), deleted: party.deleted ? t.plugins.chat.uninstalled : null, address: party.address }
  if (!party.deleted) return { title: party.name, deleted: null, address: party.address }
  if (!party.address) return { title: party.name, deleted: null, address: null }
  const title = party.address.startsWith('@') ? party.address.slice(1) : (party.address.split('/').at(-1) ?? party.address)
  return { title, deleted: party.name, address: party.address }
}
