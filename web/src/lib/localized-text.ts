import type { Schemas } from '@/api/client'
import { locale } from '@/i18n/locale'

// Names and descriptions written by plugin authors: a string is shown as is; when given per language, take the current UI language, else
// zh-CN, then en-US,
// and when none of those exist, the first entry the author wrote. The server returns them as they are, without translating
// (plugins/SPEC.md).
export function localizedText(text: Schemas['LocalizedText'], current: string = locale): string {
  if (typeof text === 'string') return text
  return text[current] ?? text['zh-CN'] ?? text['en-US'] ?? Object.values(text)[0] ?? ''
}
