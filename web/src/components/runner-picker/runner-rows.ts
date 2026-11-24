import type { Schemas } from '@/api/client'

type Provider = Schemas['ModelProvider']
type Model = Schemas['Model']

// The rows of the picker's model list. A runtime with one model provider (a CLI) lists its models directly; a runtime with several (the
// bundled Pi) puts a provider row above each provider's models, and a provider shows its models only when expanded or when a search
// finds some of them. When the catalog says which providers can be used now (ModelProvider.usable), a heading goes above each of the
// two groups.
export type Row =
  | { kind: 'section'; key: string; usable: boolean }
  // matched: while searching, how many of the provider's models the search found.
  | { kind: 'provider'; key: string; provider: Provider; expanded: boolean; matched: number | null }
  | { kind: 'model'; key: string; provider: Provider; model: Model; grouped: boolean }

export const providerKey = (providerId: string) => `p:${providerId}`
export const modelKey = (providerId: string, modelId: string) => `m:${providerId}/${modelId}`

const words = (query: string) => query.trim().toLowerCase().split(/\s+/).filter(Boolean)
const haystack = (provider: Provider, model?: Model) => [provider.id, provider.name ?? '', model?.name ?? '', model?.id ?? ''].join(' ').toLowerCase()
const matches = (terms: string[], text: string) => terms.every((term) => text.includes(term))

export interface RowsInput {
  providers: readonly Provider[]
  // Each provider's models; undefined while they are being read.
  models: ReadonlyMap<string, readonly Model[] | undefined>
  expanded: ReadonlySet<string>
  grouped: boolean
  query: string
  // At most this many model rows; the rest are counted in hidden.
  limit: number
  // Whether a provider goes under the "can use now" heading; by default what the catalog says (usable). A provider that cannot be chosen
  // at all belongs with the rest, whatever the catalog says.
  usableNow?: (provider: Provider) => boolean
}

export function buildRows({ providers, models, expanded, grouped, query, limit, usableNow = (provider) => provider.usable === true }: RowsInput): { rows: Row[]; hidden: number } {
  const terms = words(query)
  const rows: Row[] = []
  const sectioned = grouped && providers.some((provider) => provider.usable !== null)
  let shown = 0, hidden = 0, section: boolean | null = null
  for (const provider of providers) {
    const list = models.get(provider.id)
    const open = terms.length > 0 || expanded.has(provider.id)
    const found = terms.length === 0 ? (list ?? []) : (list ?? []).filter((model) => matches(terms, haystack(provider, model)))
    if (grouped) {
      const show = terms.length === 0 || found.length > 0 || (list === undefined && matches(terms, haystack(provider)))
      if (!show) continue
      const usable = usableNow(provider)
      if (sectioned && section !== usable) {
        section = usable
        rows.push({ kind: 'section', key: `s:${usable}`, usable })
      }
      rows.push({ kind: 'provider', key: providerKey(provider.id), provider, expanded: open, matched: terms.length > 0 ? found.length : null })
      if (!open) continue
    }
    for (const model of found) {
      if (shown >= limit) {
        hidden += 1
        continue
      }
      shown += 1
      rows.push({ kind: 'model', key: modelKey(provider.id, model.id), provider, model, grouped })
    }
  }
  return { rows, hidden }
}

// The order of the providers under a runtime: those the Agent can use now first (when the catalog says), the rest after, and those that
// cannot be chosen at all last; within a group the one in use leads, then the catalog's order.
export function orderProviders(providers: readonly Provider[], current: string | undefined, blocked: (provider: Provider) => boolean): Provider[] {
  const group = (provider: Provider) => (blocked(provider) ? 2 : provider.usable === true ? 0 : 1)
  const rank = (provider: Provider) => group(provider) * 2 + (provider.id === current ? 0 : 1)
  return providers.map((provider, index) => ({ provider, index })).sort((a, b) => rank(a.provider) - rank(b.provider) || a.index - b.index).map(({ provider }) => provider)
}
