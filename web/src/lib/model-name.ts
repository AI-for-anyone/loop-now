// Produce a concise, human-friendly display label for a model while keeping version numbers and tiers distinct.
// Full raw model IDs remain searchable and visible in hover tooltips / aria-labels.
const DATE_SUFFIX = /[-_](?:20\d{6}|\d{4})$/
const LATEST_TAG = /\s*\(latest\)\s*$|[-_]latest$/i
const PREVIEW_SUFFIX = /\s+preview(?:\s+\d[\d-]*)?$|[-_]preview(?:[-_]\d[\d-]*)?$/i

function cleanSlug(id: string): string {
  // Take the last path segment for namespaced IDs (e.g. "anthropic/claude-sonnet-4-5").
  const leaf = id.includes('/') ? id.slice(id.lastIndexOf('/') + 1) : id
  return leaf.replace(DATE_SUFFIX, '').replace(LATEST_TAG, '').replace(PREVIEW_SUFFIX, '').trim()
}

export function shortModelName(model: { id: string; name?: string | null } | string): string {
  const id = typeof model === 'string' ? model : model.id
  const rawName = typeof model === 'string' ? null : model.name?.trim() || null
  const slug = cleanSlug(id)
  const lower = slug.toLowerCase()

  // Anthropic Claude family: "Claude Sonnet 4.5" / "claude-sonnet-4-5" / "claude-3-7-sonnet" / "haiku" -> "Sonnet 4.5", "Haiku"
  const claudeNew = lower.match(/^(?:claude[-_\s]+)?(opus|sonnet|haiku)(?:[-_\s]+(\d+(?:[.-]\d+)?))?$/)
  if (claudeNew) {
    const tier = claudeNew[1][0].toUpperCase() + claudeNew[1].slice(1)
    const ver = claudeNew[2]?.replace(/-/g, '.')
    return ver ? `${tier} ${ver}` : tier
  }
  const claudeLegacy = lower.match(/^claude[-_\s]+(\d+(?:[.-]\d+)?)[-_\s]+(opus|sonnet|haiku)$/)
  if (claudeLegacy) {
    const tier = claudeLegacy[2][0].toUpperCase() + claudeLegacy[2].slice(1)
    return `${tier} ${claudeLegacy[1].replace(/-/g, '.')}`
  }

  // OpenAI GPT / Codex / o-series: "gpt-5.4-codex" -> "GPT-5.4", "gpt-5-codex-mini" -> "GPT-5 Mini", "gpt-4.1-mini" -> "GPT-4.1 Mini"
  const gpt = lower.match(/^gpt[-_\s]?(\d+(?:\.\d+)?[a-z]?)(?:[-_\s]+codex)?(?:[-_\s]+(mini|nano|pro|turbo|max))?$/)
  if (gpt) {
    const base = `GPT-${gpt[1]}`
    const suffix = gpt[2] ? ` ${gpt[2][0].toUpperCase() + gpt[2].slice(1)}` : ''
    return `${base}${suffix}`
  }
  const oSeries = lower.match(/^(o\d+)(?:[-_\s]+(mini|pro|high))?$/)
  if (oSeries) {
    return oSeries[2] ? `${oSeries[1]}-${oSeries[2]}` : oSeries[1]
  }

  // Google Gemini family: "gemini-2.5-pro" -> "Gemini 2.5 Pro", "gemini-2.5-flash-lite" -> "Gemini 2.5 Flash Lite"
  const gemini = lower.match(/^gemini[-_\s]+(\d+(?:\.\d+)?)[-_\s]+(.+)$/)
  if (gemini) {
    const variant = gemini[2]
      .split(/[-_\s]+/)
      .filter((part) => part && part !== 'exp' && part !== 'thinking')
      .map((part) => part[0].toUpperCase() + part.slice(1))
      .join(' ')
    return variant ? `Gemini ${gemini[1]} ${variant}` : `Gemini ${gemini[1]}`
  }

  // DeepSeek family: "deepseek-reasoner" -> "DeepSeek R1", "deepseek-chat" -> "DeepSeek V3"
  if (lower === 'deepseek-reasoner' || lower === 'deepseek-r1') return 'DeepSeek R1'
  if (lower === 'deepseek-chat' || lower === 'deepseek-v3') return 'DeepSeek V3'

  // Kimi / Moonshot: "kimi-k2" / "kimi-k2-0711" -> "Kimi K2"
  const kimi = lower.match(/^kimi[-_\s]+(k\d+(?:\.\d+)?)(?:[-_\s]+(thinking|turbo|preview))?$/)
  if (kimi) {
    const tag = kimi[2] && kimi[2] !== 'preview' ? ` ${kimi[2][0].toUpperCase() + kimi[2].slice(1)}` : ''
    return `Kimi ${kimi[1].toUpperCase()}${tag}`
  }

  // Qwen family: "qwen3-coder-plus" -> "Qwen3 Coder"
  const qwen = lower.match(/^qwen(\d+(?:\.\d+)?)[-_\s]+(coder|max|plus|turbo|vl)(?:[-_\s]+(plus|instruct|thinking))?$/)
  if (qwen) {
    return `Qwen${qwen[1]} ${qwen[2][0].toUpperCase() + qwen[2].slice(1)}`
  }

  // GLM / Zhipu: "glm-4.7" -> "GLM-4.7"
  const glm = lower.match(/^glm[-_\s]?(\d+(?:\.\d+)?)(?:[-_\s]+(air|flash|plus))?$/)
  if (glm) {
    const suffix = glm[2] ? ` ${glm[2][0].toUpperCase() + glm[2].slice(1)}` : ''
    return `GLM-${glm[1]}${suffix}`
  }

  // Grok family: "grok-3-mini" -> "Grok 3 Mini"
  const grok = lower.match(/^grok[-_\s]+(\d+(?:\.\d+)?)(?:[-_\s]+(mini|fast|code))?$/)
  if (grok) {
    const suffix = grok[2] ? ` ${grok[2][0].toUpperCase() + grok[2].slice(1)}` : ''
    return `Grok ${grok[1]}${suffix}`
  }

  // Fallback: clean display name if provided (strip redundant "Claude " prefix & "(latest)"), otherwise cleaned slug.
  if (rawName) {
    const trimmed = rawName
      .replace(/^Claude\s+(?=(?:Opus|Sonnet|Haiku)\b)/i, '')
      .replace(LATEST_TAG, '')
      .replace(PREVIEW_SUFFIX, '')
      .trim()
    if (trimmed) return trimmed
  }
  return slug || id
}
