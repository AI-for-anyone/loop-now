import type { Schemas } from '@/api/client'
import { locale } from '@/i18n/locale'

// 插件作者写的名称与说明：字符串原样显示；按语言给出时取当前界面语言，没有时依次取 zh-CN、en-US，
// 都没有时取作者写的第一项。服务端原样返回、不翻译（plugins/SPEC.md）。
export function localizedText(text: Schemas['LocalizedText'], current: string = locale): string {
  if (typeof text === 'string') return text
  return text[current] ?? text['zh-CN'] ?? text['en-US'] ?? Object.values(text)[0] ?? ''
}
