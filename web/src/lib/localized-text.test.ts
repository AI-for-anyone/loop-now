import { describe, expect, it } from 'vitest'
import { localizedText } from './localized-text'

describe('localizedText', () => {
  it('字符串原样返回', () => {
    expect(localizedText('Webhook 通知', 'en-US')).toBe('Webhook 通知')
  })

  it('按语言给出时取当前界面语言，没有时依次取 zh-CN、en-US，再取第一项', () => {
    const both = { 'en-US': 'Lark', 'zh-CN': '飞书' }
    expect(localizedText(both, 'en-US')).toBe('Lark')
    expect(localizedText(both, 'zh-CN')).toBe('飞书')
    expect(localizedText(both, 'fr-FR')).toBe('飞书')
    expect(localizedText({ ja: '週報', 'en-US': 'Weekly report' }, 'zh-CN')).toBe('Weekly report')
    expect(localizedText({ ja: '週報', fr: 'Rapport' }, 'zh-CN')).toBe('週報')
  })
})
