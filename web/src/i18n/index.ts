import { enUS } from './en-US'
import { locale } from './locale'
import { zhCN } from './zh-CN'

type Widen<T> = T extends string
  ? string
  : T extends (...args: infer A) => infer R
    ? (...args: A) => Widen<R>
    : { readonly [K in keyof T]: Widen<T[K]> }

// Shape of the copy table. To add a language, provide a copy table of this type.
export type Messages = Widen<typeof zhCN>

// Copy table for the current UI language, chosen when the page loads.
export const t: Messages = locale === 'en-US' ? enUS : zhCN
