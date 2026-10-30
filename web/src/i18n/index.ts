import { zhCN } from './zh-CN'

type Widen<T> = T extends string
  ? string
  : T extends (...args: infer A) => infer R
    ? (...args: A) => Widen<R>
    : { readonly [K in keyof T]: Widen<T[K]> }

// 文案表的结构。新增语言时，按这个类型提供一份文案表。
export type Messages = Widen<typeof zhCN>

export const t: Messages = zhCN
