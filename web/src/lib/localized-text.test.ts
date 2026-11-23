import { describe, expect, it } from 'vitest'
import { localizedText } from './localized-text'

describe('localizedText', () => {
  it('returns a string as is', () => {
    expect(localizedText('Webhook notifications', 'en-US')).toBe('Webhook notifications')
  })

  it('per-language text takes the current UI language; without it zh-CN, then en-US, then the first entry', () => {
    const both = { 'en-US': 'Lark', 'zh-CN': '飞书' }  // check-english: zh (LocalizedText: per-language data, including Chinese)
    expect(localizedText(both, 'en-US')).toBe('Lark')
    expect(localizedText(both, 'zh-CN')).toBe('飞书')  // check-english: zh (LocalizedText: per-language data, including Chinese)
    expect(localizedText(both, 'fr-FR')).toBe('飞书')  // check-english: zh (LocalizedText: per-language data, including Chinese)
    expect(localizedText({ ja: '週報', 'en-US': 'Weekly report' }, 'zh-CN')).toBe('Weekly report')  // check-english: zh (a language that is neither the UI language nor en-US)
    expect(localizedText({ ja: '週報', fr: 'Rapport' }, 'zh-CN')).toBe('週報')  // check-english: zh (a language that is neither the UI language nor en-US)
  })
})
