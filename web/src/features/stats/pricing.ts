import type { Schemas } from '@/api/client'

// Cost estimate for the stats page: converted at standard-tier API prices (USD per million tokens), for reference only.
// Taken from Anthropic's and OpenAI's public price lists (2026-10); cache writes use the 5-minute tier (Claude) or the
// 30-minute tier (OpenAI), and models whose price list doesn't list cache writes are charged at the input price. Models not in
// the table are left out of the estimate, and the page says which ones were not counted.
type Price = { input: number; output: number; cacheRead: number; cacheWrite?: number }

const claude = (input: number, output: number, cacheRead: number): Price => ({ input, output, cacheRead, cacheWrite: input * 1.25 })

const PRICES: Record<string, Price> = {
  'claude-fable-5-1': claude(10, 50, 0.25),
  'claude-fable-5': claude(10, 50, 1),
  'claude-opus-5-5': claude(4, 20, 0.2),
  'claude-opus-5': claude(5, 25, 0.5),
  'claude-opus-4-8': claude(5, 25, 0.5),
  'claude-opus-4-7': claude(5, 25, 0.5),
  'claude-opus-4-6': claude(5, 25, 0.5),
  'claude-opus-4-5': claude(5, 25, 0.5),
  'claude-sonnet-5-5': claude(2, 10, 0.2),
  'claude-sonnet-5': claude(2, 10, 0.2),
  'claude-sonnet-4-6': claude(3, 15, 0.3),
  'claude-sonnet-4-5': claude(3, 15, 0.3),
  'claude-haiku-5-5': claude(0.1, 0.5, 0.01),
  'claude-haiku-4-5': claude(1, 5, 0.1),
  'gpt-6-astra': { input: 10, output: 50, cacheRead: 1, cacheWrite: 12.5 },
  'gpt-6.1-sol': { input: 2, output: 10, cacheRead: 0.1, cacheWrite: 2.5 },
  'gpt-6.1-terra': { input: 1, output: 5, cacheRead: 0.05, cacheWrite: 1.25 },
  'gpt-6-sol': { input: 2, output: 10, cacheRead: 0.2, cacheWrite: 2.5 },
  'gpt-6-luna': { input: 0.1, output: 0.5, cacheRead: 0.01, cacheWrite: 0.125 },
  'gpt-5.6-sol': { input: 4, output: 20, cacheRead: 0.4, cacheWrite: 5 },
  'gpt-5.6-terra': { input: 2, output: 12, cacheRead: 0.2, cacheWrite: 2.5 },
  'gpt-5.6-luna': { input: 0.2, output: 1.2, cacheRead: 0.02, cacheWrite: 0.25 },
  'gpt-5.5': { input: 5, output: 30, cacheRead: 0.5 },
  'gpt-5.4': { input: 2.5, output: 15, cacheRead: 0.25 },
  'gpt-5.4-mini': { input: 0.75, output: 4.5, cacheRead: 0.075 },
  'gpt-5.3-codex': { input: 1.75, output: 14, cacheRead: 0.175 },
  'gpt-5.2': { input: 1.75, output: 14, cacheRead: 0.175 },
  'gpt-5.1': { input: 1.25, output: 10, cacheRead: 0.125 },
  'gpt-5': { input: 1.25, output: 10, cacheRead: 0.125 },
  'gpt-5-mini': { input: 0.25, output: 2, cacheRead: 0.025 },
}

// Strip the provider prefix, the date suffix (claude-sonnet-4-5-20250929) and the context marker ([1m]) from the model name;
// case-insensitive.
function priceOf(model: string): Price | null {
  const id = model
    .toLowerCase()
    .replace(/^.*\//, '')
    .replace(/\[[^\]]*\]$/, '')
    .replace(/-\d{8}$/, '')
  return PRICES[id] ?? null
}

export type CostEstimate = { usd: number; priced: boolean; unpriced: string[] }

// Per model the server only gives total tokens, with no breakdown: each model is converted using the breakdown ratios
// (input, output, cache read, cache write) of all usage in the time window.
// priced is false when there is no model that can be estimated; unpriced lists the models without a price (an unknown model,
// recorded as null, is included without listing a name).
export function estimateCost(tokens: Schemas['StatsTokens']): CostEstimate {
  const { input, output, cacheRead, cacheWrite, total } = tokens.totals
  const parts = input + output + cacheRead + cacheWrite
  let usd = 0
  let priced = false
  const unpriced: string[] = []
  for (const row of tokens.byModel) {
    const price = row.model === null ? null : priceOf(row.model)
    if (!price) {
      if (row.total > 0) unpriced.push(row.model ?? '')
      continue
    }
    priced = true
    if (parts === 0 || total === 0) continue
    const perToken =
      (input * price.input + output * price.output + cacheRead * price.cacheRead + cacheWrite * (price.cacheWrite ?? price.input)) / parts
    usd += (row.total * perToken) / 1_000_000
  }
  return { usd, priced, unpriced }
}

// The family a model belongs to: Claude, GPT (including Codex), and other for everything else.
export type ModelFamily = 'claude' | 'gpt' | 'other'
export function modelFamily(provider: string | null, model: string | null): ModelFamily {
  const id = (model ?? '').toLowerCase()
  if (provider === 'anthropic' || id.startsWith('claude')) return 'claude'
  if (provider?.startsWith('openai') || id.startsWith('gpt') || id.includes('codex')) return 'gpt'
  return 'other'
}
