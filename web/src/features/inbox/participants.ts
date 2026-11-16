import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

export type Participant = Schemas['InboxParticipant']

// How a participant is displayed: name, deleted mark and address.
// The console and the platform are named from kind, in the UI language (the server's name for them is fixed text in its own
// language). When an Agent or external identity was deleted, address is still the original address: the name taken from it is
// the title, and “已删除 Agent” (Deleted Agent) / “已删除身份” (Deleted identity) is the mark; without an address, the mark is the
// title. The server's name is used only when kind and deleted don't tell who it is.
// From a plugin channel (kind plugin, address plugin:<id>/<channel>): the title reads “插件 · 名称” (Plugin · name), and when the
// plugin has been uninstalled it is marked “已卸载” (Uninstalled).
export function partyDisplay(party: Participant): { title: string; deleted: string | null; address: string | null } {
  if (party.kind === 'plugin') return { title: t.plugins.chat.sender(party.deleted ? (party.pluginId ?? party.name) : party.name), deleted: party.deleted ? t.plugins.chat.uninstalled : null, address: party.address }
  if (party.kind === 'console' || party.kind === 'platform') return { title: t.inbox.kind[party.kind], deleted: null, address: party.address }
  if (!party.deleted) return { title: party.name, deleted: null, address: party.address }
  const mark = t.inbox.deletedParty[party.kind] ?? party.name
  if (!party.address) return { title: mark, deleted: null, address: null }
  const title = party.address.startsWith('@') ? party.address.slice(1) : (party.address.split('/').at(-1) ?? party.address)
  return { title, deleted: mark, address: party.address }
}

// The name of a participant in one line of text (notifications, "answered by", sender labels).
export const partyName = (party: Participant) => partyDisplay(party).title
