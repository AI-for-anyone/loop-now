import { enUS } from './en-US'
import { locale } from './locale'
import { zhCN } from './zh-CN'

type Widen<T> = T extends string
  ? string
  : T extends (...args: infer A) => infer R
    ? (...args: A) => Widen<R>
    : { readonly [K in keyof T]: Widen<T[K]> }

// 文案表的结构。新增语言时，按这个类型提供一份文案表。
export type Messages = Widen<typeof zhCN>

// 当前界面语言的文案表，页面加载时选定。
export const t: Messages = locale === 'en-US' ? enUS : zhCN
